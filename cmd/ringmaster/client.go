package main

// The daemon client: when `ringmaster serve` is running, the other subcommands
// talk to it over REST/WebSocket instead of the M1 file-based layer.
// Discovery works via ringmaster.daemon.json written next to ringmaster.state.json.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
)

// daemonInfo is the discovery record written by `ringmaster serve`.
type daemonInfo struct {
	Addr string `json:"addr"` // host:port the API listens on
	PID  int    `json:"pid"`
	// Token is the API access token; set only when the daemon listens beyond
	// loopback.
	Token string `json:"token,omitempty"`
}

// daemonPath is the discovery file location, next to the state file.
func (a *app) daemonPath() string {
	return filepath.Join(filepath.Dir(a.statePath), "ringmaster.daemon.json")
}

func writeDaemonInfo(path string, info daemonInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	// Owner-only: the file may hold the access token.
	return os.WriteFile(path, data, 0o600)
}

// daemon returns a client for the running daemon, or nil if none is
// reachable (missing/stale discovery file, or the API does not answer).
func (a *app) daemon() *daemonClient {
	data, err := os.ReadFile(a.daemonPath())
	if err != nil {
		return nil
	}
	var info daemonInfo
	if err := json.Unmarshal(data, &info); err != nil || info.Addr == "" {
		return nil
	}
	c := &daemonClient{
		base:  "http://" + localAddr(info.Addr),
		token: info.Token,
		// Actions can legitimately take a while (each task stop allows a
		// grace period before the hard kill), so the timeout is generous.
		http: &http.Client{Timeout: 60 * time.Second},
	}
	// Reachability probe with a short deadline of its own.
	probe := &http.Client{Timeout: 2 * time.Second}
	res, err := c.do(probe, http.MethodGet, "/api/status")
	if err != nil {
		return nil // stale file (daemon crashed); fall back to file layer
	}
	res.Body.Close()
	return c
}

type daemonClient struct {
	base  string
	token string
	http  *http.Client
}

// do sends a body-less request to the daemon, with the access token when the
// daemon requires one.
func (c *daemonClient) do(client *http.Client, method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req.Header)
	return client.Do(req)
}

func (c *daemonClient) authorize(h http.Header) {
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
}

// statusResponse mirrors the /api/status JSON shape.
type statusResponse struct {
	Projects []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Tasks []struct {
			ID     string `json:"id"`
			Key    string `json:"key"`
			Status string `json:"status"`
			PID    int    `json:"pid"`
			Port   int    `json:"port"`
		} `json:"tasks"`
	} `json:"projects"`
}

func (c *daemonClient) status() (*statusResponse, error) {
	res, err := c.do(c.http, http.MethodGet, "/api/status")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: status %d", res.StatusCode)
	}
	var out statusResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// post sends an action request and surfaces the daemon's error message.
func (c *daemonClient) post(path string) error {
	res, err := c.do(c.http, http.MethodPost, path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.Error == "" {
		body.Error = res.Status
	}
	return fmt.Errorf("daemon: %s", body.Error)
}

// shutdown asks the daemon to stop everything and exit, then waits for it
// to go away.
func (c *daemonClient) shutdown() error {
	if err := c.post("/api/shutdown"); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.status(); err != nil {
			return nil // API gone: daemon exited
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("daemon: still running 15s after shutdown request")
}

// psRemote prints live task statuses from the daemon.
func (c *daemonClient) psRemote() error {
	st, err := c.status()
	if err != nil {
		return err
	}
	fmt.Println("(via ringmaster serve daemon)")
	fmt.Printf("%-24s %-10s %-8s %-6s\n", "KEY", "STATUS", "PID", "PORT")
	for _, p := range st.Projects {
		for _, t := range p.Tasks {
			pid, port := "", ""
			if t.PID != 0 {
				pid = fmt.Sprintf("%d", t.PID)
			}
			if t.Port != 0 {
				port = fmt.Sprintf("%d", t.Port)
			}
			fmt.Printf("%-24s %-10s %-8s %-6s\n", t.Key, t.Status, pid, port)
		}
	}
	return nil
}

// logsRemote streams a task's log over WebSocket until Ctrl+C.
func (c *daemonClient) logsRemote(project, task string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := "ws" + c.base[len("http"):] + "/api/tasks/" + project + "/" + task + "/logs/ws"
	header := http.Header{}
	c.authorize(header)
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusNotFound {
			return fmt.Errorf("no logs for %s:%s (task not started yet)", project, task)
		}
		return fmt.Errorf("daemon: %w", err)
	}
	defer conn.CloseNow()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // Ctrl+C
			}
			return fmt.Errorf("daemon log stream: %w", err)
		}
		var m struct {
			Entry struct {
				TS     time.Time `json:"ts"`
				Stream string    `json:"stream"`
				Line   string    `json:"line"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out := io.Writer(os.Stdout)
		if m.Entry.Stream == "err" {
			out = os.Stderr
		}
		fmt.Fprintf(out, "%s [%s] %s\n", m.Entry.TS.Format("15:04:05"), m.Entry.Stream, m.Entry.Line)
	}
}

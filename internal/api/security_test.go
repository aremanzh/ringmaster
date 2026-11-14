package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/coder/websocket"
)

// do sends one request with the given headers and returns the status code.
func do(t *testing.T, method, url, body string, headers map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// TestCrossOriginWritesRejected is the drive-by case: a page on another
// origin posts a project with its own argv. Neither a form-style post nor a
// fetch may get through, and nothing may reach ringmaster.yaml.
func TestCrossOriginWritesRejected(t *testing.T) {
	srv, cfgPath := configTestServer(t)
	body := `{"id":"evil","path":` + jsonString(t.TempDir()) + `,"tasks":[{"id":"t","argv":["x"]}]}`

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"foreign Origin, text/plain", map[string]string{"Origin": "https://evil.example", "Content-Type": "text/plain"}, 403},
		{"foreign Origin, json", map[string]string{"Origin": "https://evil.example", "Content-Type": "application/json"}, 403},
		{"Sec-Fetch-Site cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Content-Type": "application/json"}, 403},
		{"no Origin, text/plain", map[string]string{"Content-Type": "text/plain"}, 415},
		{"no Origin, no Content-Type", nil, 415},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := do(t, "POST", srv.URL+"/api/config/projects", body, tt.headers); got != tt.want {
				t.Fatalf("add project: status %d, want %d", got, tt.want)
			}
		})
	}
	// A body-less action is cross-origin protected too.
	if got := do(t, "POST", srv.URL+"/api/projects/demo/start", "", map[string]string{"Origin": "https://evil.example"}); got != 403 {
		t.Fatalf("cross-origin start: status %d, want 403", got)
	}

	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Projects) != 1 {
		t.Fatalf("config was modified: %+v", saved.Projects)
	}
}

// TestSameOriginWritesAllowed: the UI's own requests and plain CLI/curl
// requests still work.
func TestSameOriginWritesAllowed(t *testing.T) {
	srv, _ := configTestServer(t)
	body := `{"id":"ok1","path":` + jsonString(t.TempDir()) + `,"tasks":[{"id":"t","argv":["x"]}]}`
	if got := do(t, "POST", srv.URL+"/api/config/projects", body, map[string]string{
		"Origin": srv.URL, "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json; charset=utf-8",
	}); got != 200 {
		t.Fatalf("same-origin add: status %d, want 200", got)
	}
	if got := do(t, "DELETE", srv.URL+"/api/config/projects/ok1", "", nil); got != 200 {
		t.Fatalf("non-browser delete: status %d, want 200", got)
	}
}

// TestHostHeaderAllowlist is the DNS-rebinding case: the request looks
// same-origin to the browser, but its Host is the attacker's domain.
func TestHostHeaderAllowlist(t *testing.T) {
	srv, _ := configTestServer(t)
	hostname, _ := os.Hostname()
	tests := []struct {
		host string
		want int
	}{
		{"attacker.example:9090", 403},
		{"localhost.attacker.example", 403},
		{"localhost:9090", 200},
		{"LOCALHOST", 200},
		{"127.0.0.1:9090", 200},
		{"192.168.1.20:9090", 200},
		{"[::1]:9090", 200},
	}
	if hostname != "" {
		tests = append(tests, struct {
			host string
			want int
		}{hostname + ":9090", 200})
	}
	for _, tt := range tests {
		if got := do(t, "GET", srv.URL+"/api/status", "", map[string]string{"Host": tt.host}); got != tt.want {
			t.Errorf("Host %q: status %d, want %d", tt.host, got, tt.want)
		}
	}
}

// TestWebSocketRejectsForeignOrigin: another page cannot read the event or
// log streams.
func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	srv, _ := configTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/events"

	_, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("cross-origin WebSocket handshake was accepted")
	}
	if res == nil || res.StatusCode != 403 {
		t.Fatalf("handshake response = %v, want 403", res)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {srv.URL}},
	})
	if err != nil {
		t.Fatalf("same-origin WebSocket: %v", err)
	}
	conn.CloseNow()
}

// TestTokenRequired covers the non-loopback mode: nothing is served without
// the token; the CLI sends it as a bearer header; a browser trades ?token=
// for a cookie once.
func TestTokenRequired(t *testing.T) {
	s := testServer(t)
	s.RequireToken("s3cret")
	srv := newTestHTTPServer(t, s)

	for _, path := range []string{"/", "/api/status", "/backend.js"} {
		if got := do(t, "GET", srv.URL+path, "", nil); got != 401 {
			t.Errorf("GET %s without token: status %d, want 401", path, got)
		}
	}
	if got := do(t, "GET", srv.URL+"/api/status?token=wrong", "", nil); got != 401 {
		t.Errorf("wrong ?token=: status %d, want 401", got)
	}
	if got := do(t, "GET", srv.URL+"/api/status", "", map[string]string{"Authorization": "Bearer wrong"}); got != 401 {
		t.Errorf("wrong bearer: status %d, want 401", got)
	}
	if got := do(t, "GET", srv.URL+"/api/status", "", map[string]string{"Authorization": "Bearer s3cret"}); got != 200 {
		t.Errorf("bearer: status %d, want 200", got)
	}
	// ?token= on a state-changing request is not an accepted credential.
	if got := do(t, "POST", srv.URL+"/api/projects/demo/stop?token=s3cret", "", nil); got != 401 {
		t.Errorf("POST with ?token=: status %d, want 401", got)
	}

	// Browser flow: /?token= sets the cookie and redirects to a clean URL;
	// later requests need nothing else.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	res, err := browser.Get(srv.URL + "/?token=s3cret")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Request.URL.RawQuery != "" {
		t.Fatalf("token exchange: status %d, final URL %s", res.StatusCode, res.Request.URL)
	}
	res, err = browser.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("cookie session: status %d, want 200", res.StatusCode)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:9090":    true,
		"localhost:9090":    true,
		"[::1]:9090":        true,
		"0.0.0.0:9090":      false,
		":9090":             false,
		"192.168.1.20:9090": false,
		"mypc:9090":         false,
		"garbage":           false,
	} {
		if got := LoopbackOnly(addr); got != want {
			t.Errorf("LoopbackOnly(%q) = %v, want %v", addr, got, want)
		}
	}
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

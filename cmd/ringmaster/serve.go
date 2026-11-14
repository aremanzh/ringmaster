package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/aremanzh/ringmaster/internal/api"
)

// serve runs the M2 daemon: starts every project's autostart tasks, then
// serves the REST/WebSocket API and the embedded web UI until Ctrl+C. The
// assembly + lifecycle live in internal/daemon, shared with the native GUI.
func (a *app) serve(rest []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9090", "address for the web UI / API")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	if !a.cfgExists {
		fmt.Printf("no config yet: starting empty. Add a project in the web UI; it is saved to %s\n", a.cfgPath)
	}
	run, err := a.dmn().Start()
	if err != nil {
		return err
	}

	// shutdownReq is closed by POST /api/shutdown (`ringmaster down` remote path).
	shutdownReq := make(chan struct{})
	var shutdownOnce sync.Once
	onShutdown := func() { shutdownOnce.Do(func() { close(shutdownReq) }) }

	// Beyond loopback, anyone who can reach the port could run commands on
	// this machine, so the API then requires a per-run access token.
	apiSrv := api.NewWithEngine(run.Engine, onShutdown)
	token := ""
	if !api.LoopbackOnly(*listen) {
		if token, err = newToken(); err != nil {
			run.Stop()
			return err
		}
		apiSrv.RequireToken(token)
	}

	srv := &http.Server{
		Addr:    *listen,
		Handler: apiSrv.Handler(),
	}
	serveErr := make(chan error, 1)
	go func() {
		if token == "" {
			fmt.Printf("web UI on http://%s — Ctrl+C to stop\n", *listen)
		} else {
			fmt.Printf("web UI on http://%s/?token=%s — Ctrl+C to stop\n", localAddr(*listen), token)
			fmt.Printf("%s is reachable from the network, so the UI and API need that token.\n", *listen)
			fmt.Println("From another device, use this PC's address with the same ?token=... (it changes on every start).")
		}
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	// Publish the discovery record so the other subcommands find us.
	if err := writeDaemonInfo(a.daemonPath(), daemonInfo{Addr: *listen, PID: os.Getpid(), Token: token}); err != nil {
		fmt.Fprintf(os.Stderr, "ringmaster: write %s: %v\n", a.daemonPath(), err)
	}
	defer os.Remove(a.daemonPath())

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	select {
	case err := <-serveErr:
		run.Stop()
		return fmt.Errorf("http server: %w", err)
	case <-interrupted:
		fmt.Println("\nstopping...")
	case <-shutdownReq:
		fmt.Println("shutdown requested via API — stopping...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	run.Stop()
	fmt.Println("stopped")
	return nil
}

// newToken returns a random 128-bit access token in hex.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// localAddr turns a listen address into one this machine can dial: a
// wildcard or empty host (":9090", "0.0.0.0:9090") becomes loopback.
func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/aremanzh/byob/internal/api"
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

	run, err := a.dmn().Start()
	if err != nil {
		return err
	}

	// shutdownReq is closed by POST /api/shutdown (`byob down` remote path).
	shutdownReq := make(chan struct{})
	var shutdownOnce sync.Once
	onShutdown := func() { shutdownOnce.Do(func() { close(shutdownReq) }) }

	srv := &http.Server{
		Addr:    *listen,
		Handler: api.NewWithEngine(run.Engine, onShutdown).Handler(),
	}
	serveErr := make(chan error, 1)
	go func() {
		fmt.Printf("web UI on http://%s — Ctrl+C to stop\n", *listen)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	// Publish the discovery record so the other subcommands find us.
	if err := writeDaemonInfo(a.daemonPath(), daemonInfo{Addr: *listen, PID: os.Getpid()}); err != nil {
		fmt.Fprintf(os.Stderr, "byob: write %s: %v\n", a.daemonPath(), err)
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

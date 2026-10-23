// Command byob is a "bring your own binaries" dev process supervisor (M1:
// Windows + PHP/Laravel, CLI only).
//
// Architecture note (M1, no daemon): `byob up` runs the supervisor in the
// FOREGROUND until Ctrl+C. Cross-process commands work via byob.state.json:
// `byob down` kills the recorded (identity-verified) supervisor PID — its Job
// Object handles close on death and the kernel reaps every task tree.
// `byob ps` verifies recorded PIDs; `byob logs` tails the log files the up
// process writes. When the `byob serve` daemon is running (discovered via
// byob.daemon.json), subcommands route over its REST/WS API instead.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aremanzh/byob/internal/config"
	"github.com/aremanzh/byob/internal/daemon"
)

// version is set at release build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "byob:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("byob", flag.ContinueOnError)
	configPath := fs.String("config", "byob.yaml", "path to the byob.yaml registry")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = usage
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // usage already printed
		}
		return err
	}
	rest := fs.Args()
	if *showVersion || (len(rest) > 0 && rest[0] == "version") {
		fmt.Println("byob", version)
		return nil
	}
	if len(rest) == 0 {
		usage()
		return fmt.Errorf("missing subcommand")
	}
	cmd, rest := rest[0], rest[1:]
	if cmd == "help" {
		usage()
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	app := &app{cfg: cfg, cfgPath: *configPath, statePath: filepath.Join(filepath.Dir(*configPath), "byob.state.json")}

	switch cmd {
	case "up":
		return app.up(rest)
	case "serve":
		return app.serve(rest)
	case "down":
		return app.down(rest)
	case "restart":
		return app.restart(rest)
	case "ps":
		return app.ps()
	case "logs":
		return app.logs(rest)
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `byob — bring-your-own-binaries dev process supervisor

Usage:
  byob [-config byob.yaml] <command> [args]
  byob -version

Commands:
  up [projectId]              start project tasks and supervise (foreground; Ctrl+C stops)
  serve [-listen host:port]   start all projects and serve the web UI / REST / WebSocket API
  down [projectId]            stop a project's supervisor and reap its processes
  restart <projectId>         down then up
  ps                          show recorded tasks and their live status
  logs <projectId>[:<taskId>] print buffered logs and tail live
`)
}

// app carries the loaded config and derived paths through subcommands.
type app struct {
	cfg       *config.Config
	cfgPath   string
	statePath string
}

// dmn builds a daemon.Daemon view over this app's config and paths. The
// assembly + lifecycle logic lives there, shared with the native GUI.
func (a *app) dmn() *daemon.Daemon {
	return &daemon.Daemon{Cfg: a.cfg, CfgPath: a.cfgPath, StatePath: a.statePath}
}

// project looks up a project by id.
func (a *app) project(id string) (config.Project, error) {
	for _, p := range a.cfg.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return config.Project{}, fmt.Errorf("unknown project %q (known: %s)", id, strings.Join(a.projectIDs(), ", "))
}

func (a *app) projectIDs() []string {
	ids := make([]string, len(a.cfg.Projects))
	for i, p := range a.cfg.Projects {
		ids[i] = p.ID
	}
	return ids
}

// targets resolves an optional [projectId] argument to project list.
func (a *app) targets(rest []string) ([]config.Project, error) {
	if len(rest) == 0 {
		return a.cfg.Projects, nil
	}
	p, err := a.project(rest[0])
	if err != nil {
		return nil, err
	}
	return []config.Project{p}, nil
}

// logDir is where the up process mirrors ring-buffer logs for `byob logs`.
func (a *app) logDir() string {
	return filepath.Join(filepath.Dir(a.statePath), ".byob", "logs")
}

// logPath maps a task key "proj:task" to its log file.
func (a *app) logPath(key string) string {
	return filepath.Join(a.logDir(), strings.ReplaceAll(key, ":", "_")+".log")
}

// supervisorKey is the state-file key recording the up process itself.
func supervisorKey(projectID string) string {
	return daemon.SupervisorKey(projectID)
}

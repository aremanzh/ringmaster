// Command ringmaster is a "bring your own binaries" dev process supervisor (M1:
// Windows + PHP/Laravel, CLI only).
//
// Architecture note (M1, no daemon): `ringmaster up` runs the supervisor in the
// FOREGROUND until Ctrl+C. Cross-process commands work via ringmaster.state.json:
// `ringmaster down` kills the recorded (identity-verified) supervisor PID — its Job
// Object handles close on death and the kernel reaps every task tree.
// `ringmaster ps` verifies recorded PIDs; `ringmaster logs` tails the log files the up
// process writes. When the `ringmaster serve` daemon is running (discovered via
// ringmaster.daemon.json), subcommands route over its REST/WS API instead.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/daemon"
)

// version is set at release build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ringmaster:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ringmaster", flag.ContinueOnError)
	configPath := fs.String("config", "ringmaster.yaml", "path to the ringmaster.yaml registry")
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
		fmt.Println("ringmaster", version)
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
	app := &app{cfg: cfg, cfgPath: *configPath, statePath: filepath.Join(filepath.Dir(*configPath), "ringmaster.state.json")}

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
	fmt.Fprint(os.Stderr, `ringmaster — bring-your-own-binaries dev process supervisor

Usage:
  ringmaster [-config ringmaster.yaml] <command> [args]
  ringmaster -version

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

// logDir is where the up process mirrors ring-buffer logs for `ringmaster logs`.
func (a *app) logDir() string {
	return filepath.Join(filepath.Dir(a.statePath), ".ringmaster", "logs")
}

// logPath maps a task key "proj:task" to its log file.
func (a *app) logPath(key string) string {
	return filepath.Join(a.logDir(), strings.ReplaceAll(key, ":", "_")+".log")
}

// supervisorKey is the state-file key recording the up process itself.
func supervisorKey(projectID string) string {
	return daemon.SupervisorKey(projectID)
}

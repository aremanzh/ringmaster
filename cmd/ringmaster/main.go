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
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// version is set at release build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// `ringmaster run` passes its command's exit code through untouched.
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "ringmaster:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ringmaster", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to ringmaster.yaml (default: the current folder, then next to ringmaster.exe, then the per-user config folder)")
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

	configPath, err := config.ResolvePath(*configFlag)
	if err != nil {
		return err
	}
	// A missing config is a normal first start for `serve`: it comes up empty
	// and the file is created when the first project is added in the UI. The
	// other commands accept a missing default config too, so `ps` and `down`
	// still reach a daemon that has no project yet; a missing file named with
	// -config is an error for them.
	var cfg *config.Config
	cfgExists := true
	if cmd == "serve" || *configFlag == "" {
		cfg, cfgExists, err = config.LoadOrEmpty(configPath)
	} else {
		cfg, err = config.Load(configPath)
	}
	if err != nil {
		return err
	}
	app := &app{cfg: cfg, cfgPath: configPath, cfgExists: cfgExists, statePath: filepath.Join(filepath.Dir(configPath), "ringmaster.state.json")}

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
	case "run":
		return app.runCommand(rest, os.Stdin, os.Stdout, os.Stderr)
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
  run <projectId> [--] <cmd>  run a one-off command in the project folder with its PHP
                              first on PATH, e.g. ringmaster run shop php artisan migrate
`)
}

// app carries the loaded config and derived paths through subcommands.
type app struct {
	cfg     *config.Config
	cfgPath string
	// cfgExists is false when cfgPath has not been created yet and cfg is the
	// empty config.
	cfgExists bool
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
	if len(a.cfg.Projects) == 0 {
		return config.Project{}, a.noProjects()
	}
	return config.Project{}, fmt.Errorf("unknown project %q (known: %s)", id, strings.Join(a.projectIDs(), ", "))
}

// noProjects is the error for a command that needs a project when the config
// has none, saying where the config is and how to fill it.
func (a *app) noProjects() error {
	if !a.cfgExists {
		return fmt.Errorf("no config yet at %s — start `ringmaster serve` and add a project in the web UI, or copy ringmaster.example.yaml there", a.cfgPath)
	}
	return fmt.Errorf("no projects in %s — add one in the web UI (`ringmaster serve`) or in that file", a.cfgPath)
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

// logDir is where each task's log file is kept.
func (a *app) logDir() string {
	return a.dmn().LogDir()
}

// logPath maps a task key "proj:task" to its log file.
func (a *app) logPath(key string) string {
	return filepath.Join(a.logDir(), supervisor.LogFileName(key))
}

// supervisorKey is the state-file key recording the up process itself.
func supervisorKey(projectID string) string {
	return daemon.SupervisorKey(projectID)
}

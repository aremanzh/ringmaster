//go:build gui

// Command ringmaster-gui is the native desktop front-end for ringmaster. It assembles the
// same core.Engine as `ringmaster serve` (via internal/daemon) and renders the same
// embedded web UI inside a WebView2 window through Wails — no HTTP server, no
// browser required. Build it with the gui tag plus Wails' own desktop,production
// tags (without them Wails links a stub that just shows an error and exits):
//
//	go build -tags gui,desktop,production -ldflags "-H windowsgui" -o ringmaster-gui.exe ./cmd/ringmaster-gui
//
// A WebView2 runtime (bundled with Windows 11) is required at run time.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aremanzh/ringmaster/internal/api"
	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/daemon"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// version is set at release build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ringmaster-gui:", err)
		showError(err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ringmaster-gui", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to ringmaster.yaml (default: the current folder, then next to the app, then the per-user config folder)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// No config file yet is a normal first start: the window opens empty and
	// the file is created when the first project is added.
	configPath, err := config.ResolvePath(*configFlag)
	if err != nil {
		return err
	}
	cfg, _, err := config.LoadOrEmpty(configPath)
	if err != nil {
		return err
	}

	d := &daemon.Daemon{
		Cfg:       cfg,
		CfgPath:   configPath,
		StatePath: filepath.Join(filepath.Dir(configPath), "ringmaster.state.json"),
	}
	run, err := d.Start()
	if err != nil {
		return err
	}

	app := NewApp(run.Engine)
	return wails.Run(&options.App{
		Title:  "Ringmaster " + version,
		Width:  1180,
		Height: 760,
		AssetServer: &assetserver.Options{
			Assets: api.Assets(),
		},
		OnStartup:  app.startup,
		OnShutdown: func(ctx context.Context) { run.Stop() },
		Bind:       []interface{}{app},
	})
}

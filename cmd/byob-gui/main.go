//go:build gui

// Command byob-gui is the native desktop front-end for byob. It assembles the
// same core.Engine as `byob serve` (via internal/daemon) and renders the same
// embedded web UI inside a WebView2 window through Wails — no HTTP server, no
// browser required. Build it with the gui tag plus Wails' own desktop,production
// tags (without them Wails links a stub that just shows an error and exits):
//
//	go build -tags gui,desktop,production -ldflags "-H windowsgui" -o byob-gui.exe ./cmd/byob-gui
//
// A WebView2 runtime (bundled with Windows 11) is required at run time.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aremanzh/byob/internal/api"
	"github.com/aremanzh/byob/internal/config"
	"github.com/aremanzh/byob/internal/daemon"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "byob-gui:", err)
		showError(err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("byob-gui", flag.ContinueOnError)
	configPath := fs.String("config", "byob.yaml", "path to the byob.yaml registry")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	d := &daemon.Daemon{
		Cfg:       cfg,
		CfgPath:   *configPath,
		StatePath: filepath.Join(filepath.Dir(*configPath), "byob.state.json"),
	}
	run, err := d.Start()
	if err != nil {
		return err
	}

	app := NewApp(run.Engine)
	return wails.Run(&options.App{
		Title:  "byob",
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

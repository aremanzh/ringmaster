package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed ui
var uiFS embed.FS

// uiHandler serves the embedded web UI at /.
func uiHandler() http.Handler {
	return http.FileServerFS(Assets())
}

// Assets returns the embedded web UI as a filesystem, shared with the native
// GUI (cmd/byob-gui) which serves the same assets through Wails.
func Assets() fs.FS {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err) // embed layout is fixed at build time
	}
	return sub
}

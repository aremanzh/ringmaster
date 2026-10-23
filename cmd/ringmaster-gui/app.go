//go:build gui

package main

import (
	"context"
	"sync"

	"github.com/aremanzh/ringmaster/internal/core"
	"github.com/aremanzh/ringmaster/internal/supervisor"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound facade over a core.Engine. Every method mirrors an
// Engine operation 1:1 (Wails exposes them at window.go.main.App.*), so the
// native GUI and the web API drive identical behaviour. Live status and log
// transitions are pushed to the frontend as Wails events instead of the web
// UI's WebSockets.
type App struct {
	eng *core.Engine
	ctx context.Context

	mu         sync.Mutex
	tailCancel func()
}

// NewApp binds the GUI to an assembled Engine.
func NewApp(eng *core.Engine) *App { return &App{eng: eng} }

// startup captures the Wails context and starts the status event pump.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.pumpStatus()
}

// shutdown cancels any live log tail. The daemon lifecycle is stopped by the
// caller's OnShutdown (run.Stop).
func (a *App) shutdown(ctx context.Context) {
	a.stopTail()
}

// pumpStatus forwards every supervisor status transition to the frontend as a
// "ringmaster:status" event — the GUI equivalent of the /api/events WebSocket.
func (a *App) pumpStatus() {
	events, cancel := a.eng.SubscribeEvents()
	defer cancel()
	for e := range events {
		wruntime.EventsEmit(a.ctx, "ringmaster:status", e)
	}
}

// --- bound operations: 1:1 with core.Engine ---

func (a *App) Status() []core.ProjectView { return a.eng.Status() }

func (a *App) TaskAction(project, task, action string) error {
	return a.eng.TaskAction(project, task, action)
}

func (a *App) ProjectAction(project, action string) error {
	return a.eng.ProjectAction(project, action)
}

func (a *App) LogsSnapshot(project, task string) ([]supervisor.LogEntry, error) {
	return a.eng.LogsSnapshot(project, task)
}

func (a *App) GetProject(id string) (core.ProjectSpec, error) { return a.eng.GetProject(id) }

func (a *App) AddProject(spec core.ProjectSpec) error { return a.eng.AddProject(spec) }

func (a *App) UpdateProject(id string, spec core.ProjectSpec) error {
	return a.eng.UpdateProject(id, spec)
}

func (a *App) DeleteProject(id string) error { return a.eng.DeleteProject(id) }

func (a *App) AddPHPRoot(path string) error { return a.eng.AddPHPRoot(path) }

func (a *App) DeletePHPRoot(path string) error { return a.eng.DeletePHPRoot(path) }

// PHP returns the same {roots, installs} shape as GET /api/php.
func (a *App) PHP() (map[string]any, error) {
	roots, installs, err := a.eng.PHP()
	if err != nil {
		return nil, err
	}
	return map[string]any{"roots": roots, "installs": installs}, nil
}

// TailLog streams one task's buffered snapshot plus live lines as "ringmaster:log"
// events until StopTail or the next TailLog call. It mirrors the per-task log
// WebSocket: only one tail is active at a time.
func (a *App) TailLog(project, task string) error {
	a.stopTail()
	snap, follow, cancel, err := a.eng.TailLogs(project, task)
	if err != nil {
		return err
	}
	key := project + ":" + task

	a.mu.Lock()
	a.tailCancel = cancel
	a.mu.Unlock()

	for _, e := range snap {
		wruntime.EventsEmit(a.ctx, "ringmaster:log", map[string]any{"key": key, "entry": e})
	}
	go func() {
		for e := range follow {
			wruntime.EventsEmit(a.ctx, "ringmaster:log", map[string]any{"key": key, "entry": e})
		}
	}()
	return nil
}

// StopTail ends the active log tail, if any.
func (a *App) StopTail() { a.stopTail() }

func (a *App) stopTail() {
	a.mu.Lock()
	cancel := a.tailCancel
	a.tailCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

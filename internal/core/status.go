package core

import (
	"fmt"
	"net"
	"time"

	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// Status merges the static config with the supervisor's live snapshot. Tasks
// that were never started report "stopped"; externally-managed tasks report
// "running" only if a port probe succeeds.
func (e *Engine) Status() []ProjectView {
	live := map[string]supervisor.TaskInfo{}
	for _, info := range e.sup.List() {
		live[info.Key] = info
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	host := e.cfg.Defaults.BindHost()
	out := make([]ProjectView, 0, len(e.cfg.Projects))
	for _, p := range e.cfg.Projects {
		pv := ProjectView{ID: p.ID, Label: p.Label, Path: p.Path, PHP: p.PHP, Error: e.projectErrLocked(p.ID)}
		for _, t := range p.Tasks {
			key := p.ID + ":" + t.ID
			tv := TaskView{
				ID: t.ID, Key: key, Primary: t.Primary,
				Autostart: t.AutostartEnabled(), Restart: t.Restart,
				Status: supervisor.StatusStopped, External: t.External,
			}
			if t.External {
				port := t.Port
				if port == 0 {
					port = p.Port
				}
				tv.Port = port
				if probeExternal(host, port) {
					tv.Status = supervisor.StatusRunning
				}
				pv.Tasks = append(pv.Tasks, tv)
				continue
			}
			if spec, ok := e.specs[key]; ok {
				tv.Argv = spec.Argv
				tv.Port = spec.Port
			}
			if info, ok := live[key]; ok {
				tv.Status = info.Status
				tv.PID = info.PID
				if !info.StartedAt.IsZero() {
					ts := info.StartedAt
					tv.StartedAt = &ts
				}
			}
			if tv.Status != supervisor.StatusStarting && tv.Status != supervisor.StatusRunning {
				tv.StartError = e.startErrs[key]
			}
			pv.Tasks = append(pv.Tasks, tv)
		}
		out = append(out, pv)
	}
	return out
}

// probeExternal reports whether an externally-managed service accepts TCP
// connections on the given port. A zero port cannot be probed.
func probeExternal(host string, port int) bool {
	if port == 0 {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

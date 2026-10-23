package core

import (
	"errors"
	"sort"
	"strings"

	"github.com/aremanzh/byob/internal/config"
	"github.com/aremanzh/byob/internal/supervisor"
)

// TaskAction starts, stops, or restarts one task. Externally-managed tasks are
// observe-only and reject every action.
func (e *Engine) TaskAction(project, task, action string) error {
	key := project + ":" + task
	if action != "stop" {
		e.refreshHost()
	}
	e.mu.RLock()
	spec, ok := e.specs[key]
	external := false
	if p, found := e.findProjectLocked(project); found {
		for _, t := range p.Tasks {
			if t.ID == task && t.External {
				external = true
			}
		}
	}
	projErr := e.projectErrLocked(project)
	e.mu.RUnlock()

	if external {
		return conflict("task %s is externally managed — byob only observes it", key)
	}
	if projErr != "" && action != "stop" {
		return conflict("%s", projErr)
	}
	if !ok {
		return notFound("unknown task %s", key)
	}
	switch action {
	case "start":
		if err := e.sup.Start(spec); err != nil {
			return conflict("%s", err)
		}
		e.onStart(key)
	case "stop":
		if err := e.sup.Stop(key); err != nil {
			return conflict("%s", err)
		}
	case "restart":
		if err := e.restartTask(spec); err != nil {
			return conflict("%s", err)
		}
	default:
		return notFound("unknown action %q", action)
	}
	return nil
}

// restartTask restarts a running task, or simply starts it if it has never run
// (the supervisor's Restart requires a registered task).
func (e *Engine) restartTask(spec supervisor.TaskSpec) error {
	for _, info := range e.sup.List() {
		if info.Key == spec.Key {
			return e.sup.Restart(spec.Key)
		}
	}
	err := e.sup.Start(spec)
	if err == nil {
		e.onStart(spec.Key)
	}
	return err
}

// ProjectAction applies start/stop/restart to a whole project. start starts
// its autostart tasks; stop stops every registered task.
func (e *Engine) ProjectAction(project, action string) error {
	p, ok := e.findProject(project)
	if !ok {
		return notFound("unknown project %s", project)
	}
	if action != "start" && action != "stop" && action != "restart" {
		return notFound("unknown action %q", action)
	}

	var errs []string
	if action == "stop" || action == "restart" {
		for _, info := range e.sup.List() {
			if strings.HasPrefix(info.Key, project+":") {
				if err := e.sup.Stop(info.Key); err != nil {
					errs = append(errs, err.Error())
				}
			}
		}
	}
	if action == "start" || action == "restart" {
		e.refreshHost()
		e.mu.RLock()
		projErr := e.projectErrLocked(project)
		e.mu.RUnlock()
		if projErr != "" {
			errs = append(errs, projErr)
		}
		busy := map[string]bool{}
		for _, info := range e.sup.List() {
			if info.Status == supervisor.StatusStarting || info.Status == supervisor.StatusRunning || info.Status == supervisor.StatusStopping {
				busy[info.Key] = true
			}
		}
		for _, t := range p.Tasks {
			if !t.AutostartEnabled() {
				continue
			}
			key := project + ":" + t.ID
			e.mu.RLock()
			spec, ok := e.specs[key]
			e.mu.RUnlock()
			if !ok || busy[key] {
				continue
			}
			if err := e.sup.Start(spec); err != nil {
				errs = append(errs, err.Error())
			} else {
				e.onStart(key)
			}
		}
	}
	if len(errs) > 0 {
		return conflict("%s", strings.Join(errs, "; "))
	}
	return nil
}

// LogsSnapshot returns the buffered log of one task, oldest first.
func (e *Engine) LogsSnapshot(project, task string) ([]supervisor.LogEntry, error) {
	key := project + ":" + task
	snap, _, cancel, err := e.sup.Logs(key)
	if err != nil {
		return nil, notFound("%s", err)
	}
	cancel()
	sort.SliceStable(snap, func(i, j int) bool { return snap[i].TS.Before(snap[j].TS) })
	return snap, nil
}

// SubscribeEvents returns a channel of every task status transition and a
// cancel func to unsubscribe. Passthrough to the supervisor.
func (e *Engine) SubscribeEvents() (<-chan supervisor.Event, func()) {
	return e.sup.Subscribe()
}

// TailLogs returns one task's buffered snapshot plus a live follow channel and
// a cancel func. Passthrough to the supervisor.
func (e *Engine) TailLogs(project, task string) ([]supervisor.LogEntry, <-chan supervisor.LogEntry, func(), error) {
	key := project + ":" + task
	snap, follow, cancel, err := e.sup.Logs(key)
	if err != nil {
		return nil, nil, nil, notFound("%s", err)
	}
	return snap, follow, cancel, nil
}

// refreshHost rebuilds every spec when defaults.host is "lan" and the machine's
// LAN address has changed since the specs were built (e.g. a new DHCP lease
// after a router restart), so a (re)start binds the current address instead of
// a dead one. Running tasks are untouched until restarted.
func (e *Engine) refreshHost() {
	if e.cb.Rebuild == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cfg.Defaults.DynamicHost() {
		return
	}
	host := e.cfg.Defaults.BindHost()
	stale := false
	for _, s := range e.specs {
		stale = s.Host != host
		break
	}
	if !stale {
		return
	}
	specs, err := e.cb.Rebuild(e.cfg)
	var projErrs ProjectErrors
	if err != nil && !errors.As(err, &projErrs) {
		return // keep the old specs; the start will surface its own error
	}
	e.specs = specs
	e.projErrs = projErrs
}

func (e *Engine) onStart(key string) {
	if e.cb.OnStart != nil {
		e.cb.OnStart(key)
	}
}

func (e *Engine) findProject(id string) (config.Project, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.findProjectLocked(id)
}

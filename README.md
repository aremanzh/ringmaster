# byob — bring your own binaries

A lightweight local dev process & environment manager that supervises your
projects using the runtime binaries **already on your machine**. byob does
not download, install, or bundle runtimes — that is what FlyEnv/Laragon do,
and byob deliberately doesn't.

**Status: Milestone 3** — Windows-first + PHP/Laravel, CLI + web UI, with
macOS/Linux support (process groups, identity checks, port-holder lookup)
compiled in via build tags.

## What it does (M1)

- Reads a `byob.yaml` registry of your projects and their tasks
  (`serve`, `vite`, `queue`, …).
- Discovers PHP installs from configured roots and PATH, and resolves a
  version prefix (`php: "8.2"`) to a concrete `php.exe`.
- Supervises tasks with **correct process-tree kill on Windows** via Job
  Objects: stopping a task can never leave orphan `php.exe`/`node.exe`/
  `esbuild.exe` holding your ports.
- Checks a primary task's port before starting and tells you **which
  PID/image** is holding it if busy.
- Persists spawn state (`byob.state.json`) and reaps orphans after a crash —
  with an image-name + start-time identity check so a recycled Windows PID is
  never killed by mistake.
- Readiness probes: poll the port, or match a substring on stdout.
- Per-task in-memory log ring buffer (2000 lines / 1 MB) mirrored to files
  for cross-process tailing.

## What it does (M2)

- `byob serve` runs a daemon: starts every project's autostart tasks, then
  serves a web UI + JSON REST API + WebSocket streams (default
  `127.0.0.1:9090`, `-listen` to change).
- REST: `GET /api/status`, `POST /api/projects/{id}/{start|stop|restart}`,
  `POST /api/tasks/{project}/{task}/{start|stop|restart}`,
  `GET /api/tasks/{project}/{task}/logs`.
- WebSocket: `/api/events` streams task status transitions;
  `/api/tasks/{project}/{task}/logs/ws` streams the buffered log then follows
  live.
- **CLI talks to the daemon**: while `byob serve` runs (discovered via
  `byob.daemon.json`), `ps`/`up`/`down`/`restart`/`logs` route over REST/WS —
  richer statuses (`starting`, `failed`) and live log streaming. Bare
  `byob down` shuts the whole daemon down; `byob down <project>` stops just
  that project. Without a daemon, the M1 file-based layer still works.
- The web UI (embedded via `embed.FS`, single static binary preserved) shows
  every project/task with live status badges, start/stop/restart buttons, and
  a live log pane. Non-autostart tasks (e.g. a queue worker) can be started
  from the UI.
- **Manage the registry from the UI**: add a project (folder + PHP version +
  a Laravel or custom-command template) or remove one (its tasks are stopped
  first; the folder is never deleted), and add/remove `php_roots` with a live
  list of discovered PHP installs. Changes are validated, written back to
  `byob.yaml` atomically (temp file + rename), and picked up by the daemon
  immediately — no restart needed. NOTE: the file is regenerated on save, so
  hand-written comments in `byob.yaml` are not preserved.

## What it does (M3)

- **Graceful stop on Windows**: `Stop` first sends CTRL_BREAK to the task's
  process group (it can flush/exit cleanly), then falls back to the Job
  Object kill after the grace period.
- **Console codepage decoding**: non-UTF-8 console output (cp1252/cp850 …)
  is decoded via the active codepage before it reaches logs and the UI.
- **macOS/Linux**: POSIX process groups (setpgid; SIGTERM → SIGKILL to the
  whole group), `ps`-based process identity for safe orphan reaping,
  `lsof`-based port-holder lookup, and Unix-aware PHP discovery
  (`php-*/php` and `php-*/bin/php` layouts). Cross-compiles for
  linux/darwin; note a double-forking daemon can escape a POSIX group —
  the identity-checked state reaping remains the second line of defence.
- **`external: true` tasks**: services byob observes but does not manage
  (system Apache, a Windows service, a Docker container). Status comes from
  probing the project port; start/stop are rejected.
- **`requires_docker: true` tasks**: a cached `docker version` preflight
  fails the start with a clear message when the engine is down, instead of
  letting the task crash-loop.

## Explicitly out of scope

- No runtime installation of any kind — ever.
- No SSL/DNS/`*.test`/reverse-proxy features.
- No auth on the API — it binds loopback by default; don't `-listen 0.0.0.0`
  on untrusted networks.

## Quickstart

```powershell
go build -o byob.exe ./cmd/byob

copy byob.example.yaml byob.yaml
# edit byob.yaml: php_roots, project path, php version, port

.\byob.exe serve             # start everything + web UI on http://127.0.0.1:9090
.\byob.exe up myapp          # or: start + supervise (foreground, Ctrl+C stops)
.\byob.exe ps                # recorded tasks with verified live status
.\byob.exe logs myapp:serve  # print buffer, then tail
.\byob.exe down myapp        # stop everything, free the port
.\byob.exe restart myapp
```

## CLI

| Command | Description |
| --- | --- |
| `byob serve [-listen host:port]` | Start every project's `autostart` tasks and serve the web UI / REST / WebSocket API. Ctrl+C stops everything cleanly. |
| `byob up [projectId]` | Reap orphans, start all `autostart` tasks, supervise in the foreground. Ctrl+C stops everything cleanly. |
| `byob down [projectId]` | Kill the recorded supervisor (its Job Objects tear down every task tree) and reap any remaining recorded processes. |
| `byob restart <projectId>` | `down` then `up`. |
| `byob ps` | Show recorded tasks; each PID is verified (running / dead / recycled). |
| `byob logs <projectId>[:<taskId>]` | Print the buffered log and tail live. Bare project id defaults to the primary task. |

`-config path\to\byob.yaml` selects a config; `byob.state.json` and log files
live next to it.

## Recipe: a dedicated Apache instance per project

Because tasks are just argv arrays, a project can run its own Apache on its
own port without touching any other Apache (e.g. Laragon's on :80):

1. Copy the stock `httpd.conf` of the Apache build you want, then in the
   copy: fix `Define SRVROOT`, change `Listen` to the project port, REMOVE
   any shared `Include`/`IncludeOptional` lines (a shared `sites-enabled/`
   or `fcgid.conf` would leak other projects' vhosts and PHP handlers into
   this instance), and append your `LoadModule php…`/`PHPIniDir`,
   `DocumentRoot`, and a unique `PidFile`.
2. Validate with `httpd.exe -t -f your.conf`.
3. Add the task:

```yaml
- id: apache
  primary: true
  argv: ["C:\\...\\bin\\httpd.exe", "-f", "D:\\...\\myproject.conf"]
  ready: { type: port }
```

byob's port check guarantees the port is free before starting, and the Job
Object kills the parent and worker `httpd.exe` together on stop.

## How orphan safety works

1. Every task is spawned suspended, assigned to a **Job Object** with
   `KILL_ON_JOB_CLOSE`, then resumed — descendants can never escape the job.
2. If the `byob` process itself dies, the kernel closes its handles and
   terminates every job — the trees die with it.
3. As a second layer, every spawn is recorded in `byob.state.json` with the
   PID's image name and OS start time. `byob up`/`down` verify each recorded
   PID against that identity before killing: a PID the OS recycled is
   dropped from state but **never killed**.

## Config

See [`byob.example.yaml`](byob.example.yaml). Notes:

- `argv` is always an array and is spawned with `shell: false` — no shell
  interpolation, by design. `.cmd`/`.bat` (e.g. `npm.cmd`) are transparently
  routed through `cmd.exe /c`.
- Placeholders `{php}`, `{host}`, `{port}` are interpolated into `argv`.
- `defaults.host: lan` binds to this machine's current LAN IPv4 (the adapter
  with the default route, skipping WSL/Hyper-V adapters), detected at startup
  and again on every start/restart. If the address changed (e.g. a router
  restart handed out a new DHCP lease), specs are rebuilt, so restarting a
  task picks up the new IP. Tasks already running stay on the old address
  until restarted. Falls back to `127.0.0.1` when no LAN is up.
- `cleanup: ["public/hot"]` on a task deletes those files (relative to the
  project path, never outside it) before the task starts and after it exits.
  Vite only removes `public/hot` on SIGINT/SIGTERM; byob's Windows stop
  (CTRL_BREAK, then the Job Object kill) skips that, and a leftover `hot`
  file makes Laravel load assets from a dead `:5173` instead of
  `public/build`.
- Duplicate project ids or duplicate declared ports are validation errors.
- A project without `port` gets a **unique** port from `defaults.port_range`
  each run: declared ports and ports already handed to other projects are
  skipped, so two port-less Laravel apps never collide on 8000. Declare
  `port` explicitly if the project must always run on the same port.

## Development

```powershell
go vet ./...
go test ./...
```

Package layout: `internal/config` (YAML + validation + interpolation),
`internal/discover` (PHP discovery + version resolution), `internal/proc`
(ProcessGroup + Windows Job Object), `internal/portcheck` (free-port +
holder lookup), `internal/supervisor` (lifecycle, readiness, logs, state,
reaping, status events), `internal/api` (REST + WebSocket + embedded UI),
`cmd/byob` (CLI + serve daemon).

## License

MIT, see [LICENSE](LICENSE).

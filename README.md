# byob

Bring your own binaries. byob runs all your local Laravel and PHP projects from one place on Windows, using the PHP and Node already installed on your machine.

## The problem

Each Laravel app you work on wants three or four processes: `php artisan serve`, `npm run dev`, a queue worker, sometimes a Node API or its own Apache. With five client projects open, that is a wall of terminals.

Windows makes this worse. Close a terminal and `php.exe`, `node.exe` or `esbuild.exe` can keep running in the background, still holding the port. The next start fails with "address already in use", and you go hunting with `netstat -ano` and `taskkill`. Two apps that both default to port 8000 collide. If you test on your phone over the LAN, a router restart hands your PC a new IP and the config you wrote yesterday stops working.

Laravel's `php artisan dev` helps with one project in one terminal. byob is for the case where you have many projects running at the same time.

## What byob does

- One daemon supervises every project. You control it from a web UI, a desktop app or the CLI, with live status, start/stop/restart per task, and live logs.
- Stopping a task stops its whole process tree. byob puts every task in a Windows Job Object, so child processes such as Vite's `esbuild.exe` die with it and the port is freed.
- Before starting a project, byob checks its port. If something holds it, byob tells you the PID and the program name.
- Projects without a fixed port get a unique one from a range, so two Laravel apps never both land on 8000.
- `php: "8.3"` in the config resolves to a real `php.exe` from your PHP folders (Laragon's `bin\php` works) or from PATH.
- `host: lan` binds to your current LAN IP and re-detects it on every start, so phone testing survives a DHCP change.
- After a crash, byob cleans up the processes it started, and checks each PID's identity first so it never kills an unrelated program that reused the number.

byob does not install PHP, Node or databases, and it has no SSL, `*.test` domains or reverse proxy. Laragon or Laravel Herd handle that, and byob runs alongside them.

## Quickstart

You need Go to build byob.

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

The desktop app is a separate build:

```powershell
go build -tags gui,desktop,production -ldflags "-H windowsgui" -o byob-gui.exe ./cmd/byob-gui
```

A typical Laravel project in `byob.yaml`:

```yaml
version: 1
php_roots:
  - C:\laragon\bin\php
defaults:
  host: 127.0.0.1
  port_range: [8000, 8099]
projects:
  - id: shop
    path: C:\code\shop
    php: "8.3"
    tasks:
      - id: serve
        argv: ["{php}", "artisan", "serve", "--host={host}", "--port={port}"]
        primary: true
        ready: { type: port }
      - id: vite
        argv: ["npm.cmd", "run", "dev", "--", "--host={host}"]
        ready: { type: stdout, match: "ready in" }
        cleanup: ["public/hot"]
      - id: queue
        argv: ["{php}", "artisan", "queue:work"]
        autostart: false
        restart: on-failure
```

See [`byob.example.yaml`](byob.example.yaml) for every option.

## CLI

| Command | Description |
| --- | --- |
| `byob serve [-listen host:port]` | Start every project's `autostart` tasks and serve the web UI / REST / WebSocket API. Ctrl+C stops everything cleanly. |
| `byob up [projectId]` | Reap orphans, start all `autostart` tasks, supervise in the foreground. Ctrl+C stops everything cleanly. |
| `byob down [projectId]` | Kill the recorded supervisor (its Job Objects tear down every task tree) and reap any remaining recorded processes. |
| `byob restart <projectId>` | `down` then `up`. |
| `byob ps` | Show recorded tasks; each PID is verified (running / dead / recycled). |
| `byob logs <projectId>[:<taskId>]` | Print the buffered log and tail live. Bare project id defaults to the primary task. |

`-config path\to\byob.yaml` selects a config; `byob.state.json` and log files live next to it.

While `byob serve` runs (found through `byob.daemon.json`), `ps`, `up`, `down`, `restart` and `logs` go through the daemon, which gives richer statuses (`starting`, `failed`) and live log streaming. Bare `byob down` shuts the whole daemon down; `byob down <project>` stops only that project. Without a daemon, the CLI works from the state file.

## Web UI and API

`byob serve` listens on `127.0.0.1:9090` by default. The web UI is embedded in the binary. It lists every project and task with live status, start/stop/restart buttons and a live log pane, and it can start tasks that don't autostart, such as a queue worker.

You can also manage the registry from the UI: add a project (folder, PHP version, and a Laravel or custom-command template), remove one (its tasks are stopped first and the folder is never deleted), and add or remove `php_roots` with a live list of the PHP installs found. byob validates each change, writes `byob.yaml` atomically (temp file + rename) and applies it without a restart. The file is regenerated on save, so hand-written comments in `byob.yaml` are lost.

The same daemon serves a JSON API:

- `GET /api/status`
- `POST /api/projects/{id}/{start|stop|restart}`
- `POST /api/tasks/{project}/{task}/{start|stop|restart}`
- `GET /api/tasks/{project}/{task}/logs`
- WebSocket `/api/events` streams task status changes.
- WebSocket `/api/tasks/{project}/{task}/logs/ws` sends the buffered log, then follows it live.

The API has no authentication. It binds to loopback by default; don't use `-listen 0.0.0.0` on a network you don't trust.

## Config notes

- `argv` is always an array and is spawned with `shell: false`, so there is no shell interpolation. `.cmd` and `.bat` files (for example `npm.cmd`) run through `cmd.exe /c` automatically.
- `{php}`, `{host}` and `{port}` are replaced inside `argv`.
- Readiness probes either poll the port (`type: port`, 30 s) or wait for a substring on stdout (`type: stdout`, 60 s).
- Each task keeps its last 2000 lines (up to 1 MB) of output in memory, mirrored to a file so another process can tail it. Console output that isn't UTF-8 (cp1252, cp850 and so on) is decoded with the active codepage first.
- `restart: on-failure` restarts a task that exits with a non-zero code, at most 5 times in a row.
- `defaults.host: lan` binds to the current LAN IPv4: the adapter with the default route, skipping WSL and Hyper-V adapters. byob detects it at startup and again on every start or restart. When the address has changed, restarting a task picks up the new IP; tasks already running stay on the old one until restarted. With no LAN, it falls back to `127.0.0.1`.
- With `host: lan`, pages open on the LAN IP, and Vite's default CORS policy only allows `localhost` origins, so the browser blocks scripts from `:5173`. Allow private-network origins in your project's `vite.config.js`:

  ```js
  server: {
    cors: {
      origin: /^https?:\/\/(localhost|127\.0\.0\.1|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+)(:\d+)?$/,
    },
  },
  ```

- `cleanup: ["public/hot"]` deletes those files, relative to the project path and never outside it, before the task starts and after it exits. Vite only removes `public/hot` on SIGINT or SIGTERM, which byob's Windows stop doesn't send. A leftover `hot` file makes Laravel load assets from a dead `:5173` instead of `public/build`.
- A project without `port` gets a unique port from `defaults.port_range` on each run. byob skips declared ports and ports already given to other projects. Declare `port` if a project must always use the same one.
- Duplicate project ids or duplicate declared ports are validation errors.
- `external: true` marks a service byob only watches, such as a system Apache, a Windows service or a Docker container. Its status comes from probing the project port, or the task's own `port`; start and stop are rejected.
- `requires_docker: true` runs a cached `docker version` check first, so the task fails with a clear message when the Docker engine is down, before it can crash-loop.

## Recipe: a dedicated Apache instance per project

Tasks are plain argv arrays, so a project can run its own Apache on its own port without touching any other Apache (for example Laragon's on :80):

1. Copy the stock `httpd.conf` of the Apache build you want. In the copy, fix `Define SRVROOT`, change `Listen` to the project port, and remove any shared `Include`/`IncludeOptional` lines: a shared `sites-enabled/` or `fcgid.conf` would leak other projects' vhosts and PHP handlers into this instance. Then append your `LoadModule php…`/`PHPIniDir`, `DocumentRoot` and a unique `PidFile`.
2. Validate with `httpd.exe -t -f your.conf`.
3. Add the task:

```yaml
- id: apache
  primary: true
  argv: ["C:\\...\\bin\\httpd.exe", "-f", "D:\\...\\myproject.conf"]
  ready: { type: port }
```

byob's port check makes sure the port is free before starting, and the Job Object kills the parent and worker `httpd.exe` processes together on stop.

## How stopping and orphan cleanup work

1. byob spawns every task suspended, assigns it to a Job Object with `KILL_ON_JOB_CLOSE`, then resumes it. The task cannot start a child before it is inside the job, so no descendant can escape.
2. A stop first sends CTRL_BREAK to the task's process group so it can exit cleanly, then kills the job after a 3-second grace period. The desktop app has no console, so CTRL_BREAK can't reach its tasks and every stop there ends with the kill.
3. If byob itself dies, Windows closes its handles and terminates every job, taking the process trees with it.
4. As a second layer, byob records each spawn in `byob.state.json` with the program name and OS start time. `byob up` and `byob down` check each recorded PID against that before killing it. A PID that Windows has given to another program is dropped from the state file and left alone.

On macOS and Linux, byob uses POSIX process groups (setpgid, then SIGTERM and SIGKILL to the whole group), `ps` for process identity, `lsof` to find what holds a port, and finds PHP in `php-*/php` and `php-*/bin/php` layouts. A daemon that double-forks can leave its process group; the identity-checked state file still catches it.

## Development

```powershell
go vet ./...
go test ./...
```

Package layout: `internal/config` (YAML, validation, interpolation, LAN host detection), `internal/discover` (PHP discovery and version resolution), `internal/proc` (process groups and the Windows Job Object), `internal/portcheck` (free-port check and port-holder lookup), `internal/supervisor` (lifecycle, readiness, logs, state, reaping, status events), `internal/core` and `internal/daemon` (the engine shared by `byob serve` and the desktop app), `internal/api` (REST, WebSocket, embedded UI), `cmd/byob` (CLI and serve daemon), `cmd/byob-gui` (desktop app).

## License

MIT, see [LICENSE](LICENSE).

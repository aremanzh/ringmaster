<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/mark-dark.svg">
    <img src="assets/logo/mark.svg" width="40" height="40" alt="">
  </picture>
  Ringmaster
</h1>

Bring your own binaries. Ringmaster runs all your local Laravel and PHP projects from one place on Windows, using the PHP and Node already installed on your machine.

## The problem

Your projects don't all run on the same PHP. A new app is on 8.4, last year's client site is pinned to 8.2, and an older system can't be upgraded yet. Laragon and most Windows stacks give you one active PHP at a time, so working on two of them means switching versions, restarting, and remembering which terminal used which `php.exe`.

Each of those apps also wants three or four processes: `php artisan serve`, `npm run dev`, a queue worker, sometimes a Node API or its own Apache. With five client projects open, that is a wall of terminals.

Windows makes this worse. Close a terminal and `php.exe`, `node.exe` or `esbuild.exe` can keep running in the background, still holding the port. The next start fails with "address already in use", and you go hunting with `netstat -ano` and `taskkill`. Two apps that both default to port 8000 collide. If you test on your phone over the LAN, a router restart hands your PC a new IP and the config you wrote yesterday stops working.

Laravel's `php artisan dev` helps with one project in one terminal. Ringmaster is for the case where you have many projects running at the same time.

## What Ringmaster does

- Each project runs on its own PHP version, all at the same time. `php: "8.2"` on one project and `php: "8.4"` on the next resolve to real `php.exe` installs from your PHP folders (Laragon's `bin\php` works) or from PATH. Nothing global is switched. That PHP also leads the `PATH` of every task in the project, so `composer` and npm scripts that call `php` get the same version.
- One daemon supervises every project. You control it from a web UI, a desktop app or the CLI, with live status, start/stop/restart per task, and live logs.
- Stopping a task stops its whole process tree. Ringmaster puts every task in a Windows Job Object, so child processes such as Vite's `esbuild.exe` die with it and the port is freed.
- Before starting a project, Ringmaster checks its port. If something holds it, Ringmaster tells you the PID and the program name.
- Projects without a fixed port get a unique one from a range, so two Laravel apps never both land on 8000.
- `host: lan` binds to your current LAN IP and re-detects it on every start, so phone testing survives a DHCP change.
- After a crash, Ringmaster cleans up the processes it started, and checks each PID's identity first so it never kills an unrelated program that reused the number.

Ringmaster does not install PHP, Node or databases, and it has no SSL, `*.test` domains or reverse proxy. Laragon or Laravel Herd handle that, and Ringmaster runs alongside them.

## Quickstart

Download `ringmaster_<version>_windows_amd64.zip` from the [releases page](https://github.com/aremanzh/ringmaster/releases) and extract it. The zip holds `ringmaster.exe` (CLI and web UI), `ringmaster-gui.exe` (desktop app) and an example config.

```powershell
copy ringmaster.example.yaml ringmaster.yaml
# edit ringmaster.yaml: php_roots, project path, php version, port

.\ringmaster.exe serve             # start everything + web UI on http://127.0.0.1:9090
.\ringmaster.exe up myapp          # or: start + supervise (foreground, Ctrl+C stops)
.\ringmaster.exe ps                # recorded tasks with verified live status
.\ringmaster.exe logs myapp:serve  # print buffer, then tail
.\ringmaster.exe down myapp        # stop everything, free the port
.\ringmaster.exe restart myapp
.\ringmaster.exe run myapp php artisan migrate   # one-off command on the project's PHP
```

`ringmaster-gui.exe` runs the same projects in a desktop window instead of a browser. It needs the WebView2 runtime, which ships with Windows 11.

The binaries are not code-signed yet, so Windows SmartScreen warns the first time you run them.

To try it before pointing it at your own projects, [`examples/demo`](examples/demo/ringmaster.yaml) has three small invented projects (`shop`, `blog`, `crm`) on PHP 8.4, 8.3 and 8.2, with simulated Vite and queue processes. Each page shows the PHP version that served it. Set `php_roots` and the versions to ones you have in that file, then run `..\..\ringmaster.exe serve` from that folder.

A typical Laravel project in `ringmaster.yaml`:

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
        restart: always
```

See [`ringmaster.example.yaml`](ringmaster.example.yaml) for every option.

## CLI

| Command | Description |
| --- | --- |
| `ringmaster serve [-listen host:port]` | Start every project's `autostart` tasks and serve the web UI / REST / WebSocket API. Ctrl+C stops everything cleanly. |
| `ringmaster up [projectId]` | Reap orphans, start all `autostart` tasks, supervise in the foreground. Ctrl+C stops everything cleanly. |
| `ringmaster down [projectId]` | Kill the recorded supervisor (its Job Objects tear down every task tree) and reap any remaining recorded processes. |
| `ringmaster restart <projectId>` | `down` then `up`. |
| `ringmaster ps` | Show recorded tasks; each PID is verified (running / dead / recycled). |
| `ringmaster logs <projectId>[:<taskId>]` | Print the buffered log and tail live. Bare project id defaults to the primary task. |
| `ringmaster run <projectId> [--] <command>` | Run a one-off command in the project folder with the project's PHP first on `PATH`: `ringmaster run shop php artisan migrate`, `ringmaster run shop composer install`, `ringmaster run shop php artisan tinker`. It runs in your terminal, is not supervised, works with or without the daemon, and exits with the command's exit code. |

`-config path\to\ringmaster.yaml` selects a config; `ringmaster.state.json` and log files live next to it.

While `ringmaster serve` runs (found through `ringmaster.daemon.json`), `ps`, `up`, `down`, `restart` and `logs` go through the daemon, which gives richer statuses (`starting`, `failed`) and live log streaming. Bare `ringmaster down` shuts the whole daemon down; `ringmaster down <project>` stops only that project. Without a daemon, the CLI works from the state file.

## Web UI and API

`ringmaster serve` listens on `127.0.0.1:9090` by default. The web UI is embedded in the binary. It lists every project and task with live status, start/stop/restart buttons and a live log pane, and it can start tasks that don't autostart, such as a queue worker.

You can also manage the registry from the UI: add a project (folder, PHP version, and a Laravel or custom-command template), remove one (its tasks are stopped first and the folder is never deleted), and add or remove `php_roots` with a live list of the PHP installs found. Ringmaster validates each change, writes `ringmaster.yaml` atomically (temp file + rename) and applies it without a restart. The file is regenerated on save, so hand-written comments in `ringmaster.yaml` are lost.

The same daemon serves a JSON API:

- `GET /api/status`
- `POST /api/projects/{id}/{start|stop|restart}`
- `POST /api/tasks/{project}/{task}/{start|stop|restart}`
- `GET /api/tasks/{project}/{task}/logs`
- WebSocket `/api/events` streams task status changes.
- WebSocket `/api/tasks/{project}/{task}/logs/ws` sends the buffered log, then follows it live.

The API can add a project with any command and start it, so it is guarded:

- It binds to loopback by default. On loopback there is no login, but a request that changes anything must come from the Ringmaster page itself or from a non-browser client such as the CLI or `curl`. Requests from other web pages are rejected (403), as are requests whose `Host` header is not `localhost`, an IP address or this PC's name, and JSON bodies need `Content-Type: application/json`.
- With `-listen` on any other address, for example `-listen 0.0.0.0:9090` to open the UI from your phone, `ringmaster serve` generates an access token on every start and prints a URL ending in `?token=...`. Open that URL once per browser; it sets a cookie and the token leaves the address bar. API clients send `Authorization: Bearer <token>`. The CLI reads the token from `ringmaster.daemon.json`, so `ps`, `logs`, `up` and `down` keep working.

The token travels over plain HTTP, so anyone who can watch the network can read it. Use a non-loopback `-listen` only on a network you trust.

## Config notes

- `argv` is always an array and is spawned with `shell: false`, so there is no shell interpolation. `.cmd` and `.bat` files (for example `npm.cmd`) run through `cmd.exe /c` automatically.
- `{php}`, `{host}` and `{port}` are replaced inside `argv` and in `env` values.
- A project with a `php` version has that PHP's folder put first on the `PATH` of all its tasks. `{php}` is the project's PHP in `argv`; the `PATH` entry covers everything a task starts in turn: `composer`, `npm run dev` scripts that call `php artisan`, a `Procfile` runner. A bare `argv[0]` such as `php` is looked up in that folder first, then on Ringmaster's own `PATH`. A `php` version that is not installed is now an error on that project even when no task uses `{php}`.
- `env` adds environment variables to one task, on top of the ones Ringmaster runs with: `env: { APP_URL: "http://{host}:{port}" }`. Setting `PATH` there replaces the inherited one; the project's PHP folder is still put in front.
- `cwd` runs a task in a subfolder of the project, for example `cwd: frontend` in a monorepo. It is relative to the project path and cannot leave it. `cleanup` paths stay relative to the project path.
- Readiness probes either poll the port (`type: port`, 30 s) or wait for a substring on stdout (`type: stdout`, 60 s).
- Each task keeps its last 2000 lines (up to 1 MB) of output in memory, mirrored to a file so another process can tail it. Console output that isn't UTF-8 (cp1252, cp850 and so on) is decoded with the active codepage first.
- `restart: on-failure` restarts a task that exits with a non-zero code. `restart: always` also restarts it after a clean exit, which is what a queue worker needs: `artisan queue:restart`, `--max-time` and `--max-jobs` all make the worker exit with code 0 and expect something to start it again. Stopping a task yourself never triggers a restart.
- The first restart comes after 1 s. If the task exits again within 10 s of starting, or before it is ready, the wait doubles each time up to 30 s. After 5 failed restarts in a row the task is marked `failed`. A run of 10 s or more starts the count and the wait over, and clean exits never count toward the limit.
- `defaults.host: lan` binds to the current LAN IPv4: the adapter with the default route, skipping WSL and Hyper-V adapters. Ringmaster detects it at startup and again on every start or restart. When the address has changed, restarting a task picks up the new IP; tasks already running stay on the old one until restarted. With no LAN, it falls back to `127.0.0.1`.
- With `host: lan`, pages open on the LAN IP, and Vite's default CORS policy only allows `localhost` origins, so the browser blocks scripts from `:5173`. Allow private-network origins in your project's `vite.config.js`:

  ```js
  server: {
    cors: {
      origin: /^https?:\/\/(localhost|127\.0\.0\.1|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+)(:\d+)?$/,
    },
  },
  ```

- `cleanup: ["public/hot"]` deletes those files, relative to the project path and never outside it, before the task starts and after it exits. Vite only removes `public/hot` on SIGINT or SIGTERM, which Ringmaster's Windows stop doesn't send. A leftover `hot` file makes Laravel load assets from a dead `:5173` instead of `public/build`.
- A project without `port` gets a unique port from `defaults.port_range` on each run. Ringmaster skips declared ports and ports already given to other projects. Declare `port` if a project must always use the same one.
- Duplicate project ids or duplicate declared ports are validation errors.
- `external: true` marks a service Ringmaster only watches, such as a system Apache, a Windows service or a Docker container. Its status comes from probing the project port, or the task's own `port`; start and stop are rejected.
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

Ringmaster's port check makes sure the port is free before starting, and the Job Object kills the parent and worker `httpd.exe` processes together on stop.

## How stopping and orphan cleanup work

1. Ringmaster spawns every task suspended, assigns it to a Job Object with `KILL_ON_JOB_CLOSE`, then resumes it. The task cannot start a child before it is inside the job, so no descendant can escape.
2. A stop first sends CTRL_BREAK to the task's process group so it can exit cleanly, then kills the job after a 3-second grace period. The desktop app has no console, so CTRL_BREAK can't reach its tasks and every stop there ends with the kill.
3. If Ringmaster itself dies, Windows closes its handles and terminates every job, taking the process trees with it.
4. As a second layer, Ringmaster records each spawn in `ringmaster.state.json` with the program name and OS start time. `ringmaster up` and `ringmaster down` check each recorded PID against that before killing it. A PID that Windows has given to another program is dropped from the state file and left alone.

On macOS and Linux, Ringmaster uses POSIX process groups (setpgid, then SIGTERM and SIGKILL to the whole group), `ps` for process identity, `lsof` to find what holds a port, and finds PHP in `php-*/php` and `php-*/bin/php` layouts. A daemon that double-forks can leave its process group; the identity-checked state file still catches it.

## Development

Building from source needs Go 1.26 or later.

```powershell
go build -o ringmaster.exe ./cmd/ringmaster
go build -tags gui,desktop,production -ldflags "-H windowsgui" -o ringmaster-gui.exe ./cmd/ringmaster-gui
go vet ./...
go test ./...
```

Every push and pull request runs `go vet`, the tests and a build on Windows and Ubuntu (`.github/workflows/ci.yml`). Windows is the supported platform; the Ubuntu run keeps the macOS/Linux code paths from breaking unnoticed. Pushing a `v*` tag builds both binaries on GitHub Actions and publishes them as a release. Without a checkout at hand, run the `release` workflow from the Actions tab on `main` and enter the version (for example `v0.1.3`): it checks that `CHANGELOG.md` has that entry, runs the tests and the build, then creates the tag and the release.

The icon is drawn by `go run ./tools/genassets`, which writes `assets/icon/ringmaster.ico`, a 256 px PNG and the UI's `favicon.png`. The `.syso` files that put the icon into each `.exe` come from that `.ico`:

```powershell
go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --manifest none --icon assets/icon/ringmaster.ico --product-name Ringmaster --product-version v0.0.0 --file-version v0.0.0 --file-description "Ringmaster CLI" --original-filename ringmaster.exe --out cmd/ringmaster/rsrc
go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --manifest none --icon assets/icon/ringmaster.ico --product-name Ringmaster --product-version v0.0.0 --file-version v0.0.0 --file-description "Ringmaster desktop app" --original-filename ringmaster-gui.exe --out cmd/ringmaster-gui/rsrc
```

Package layout: `internal/config` (YAML, validation, interpolation, LAN host detection), `internal/discover` (PHP discovery and version resolution), `internal/proc` (process groups and the Windows Job Object), `internal/portcheck` (free-port check and port-holder lookup), `internal/supervisor` (lifecycle, readiness, logs, state, reaping, status events), `internal/core` and `internal/daemon` (the engine shared by `ringmaster serve` and the desktop app), `internal/api` (REST, WebSocket, embedded UI), `cmd/ringmaster` (CLI and serve daemon), `cmd/ringmaster-gui` (desktop app).

## License

MIT, see [LICENSE](LICENSE).

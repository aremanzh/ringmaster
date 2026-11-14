# Changelog

Notable, user-facing changes. Newest first.

## 0.1.3 — unreleased

- **Security: other web pages can no longer drive the API.** While
  `ringmaster serve` was running, any page open in your browser could send
  requests to `127.0.0.1:9090`, add a project with a command of its choice and
  start it. Requests that change anything are now accepted only from the
  Ringmaster page itself or from non-browser clients (the CLI, `curl`);
  requests with a foreign `Host` header are rejected, which closes the DNS
  rebinding route; JSON bodies must be sent as `application/json`. The desktop
  app was not affected: it does not open a port. If you ran `ringmaster serve`
  on 0.1.2 or earlier, check `ringmaster.yaml` for projects you did not add.
- **`-listen` on a non-loopback address now requires an access token.**
  `ringmaster serve -listen 0.0.0.0:9090` prints a URL ending in `?token=...`;
  open it once per browser. API clients send `Authorization: Bearer <token>`.
  The CLI picks the token up by itself. The token changes on every start.
- **New: `restart: always`.** Restarts a task after any exit you did not ask
  for, including a clean one. Use it for queue workers: `artisan queue:restart`,
  `--max-time` and `--max-jobs` make the worker exit with code 0, which
  `on-failure` leaves stopped. The Laravel template in the UI now uses it.
- **Fixed: `on-failure` stopped restarting after 5 crashes in total.** The
  limit was meant to be 5 in a row, but the count never went back to zero, so a
  worker that crashed now and then stayed down after the fifth time. A run of
  10 seconds or more now resets it.
- Restarts back off: 1 s, then doubling up to 30 s while the task keeps exiting
  right after it starts. Stopping a task during that wait takes effect at once.
- A task waiting to be restarted no longer shows the PID of its dead process.

## 0.1.2 — 2026-11-09

- **Fixed: one busy port no longer takes everything down.** If an autostart
  task could not start at launch (typically because another process held its
  port), `ringmaster serve` and the desktop app exited and no project started.
  Now the failure is shown on that task, e.g. "port 8101 on 127.0.0.1 is in use
  by PID 1234 (php.exe)", and every other project starts normally. The message
  clears on the next successful start.
- **Fixed: no half-started projects.** When a project's primary task fails to
  start, its other tasks (`vite`, `queue`) are no longer started on their own.
- `examples/demo` runs its three projects on PHP 8.4, 8.3 and 8.2, and each page
  shows the PHP version that served it.

## 0.1.1 — 2026-11-09

- `ringmaster.exe` and `ringmaster-gui.exe` carry the Ringmaster icon and
  Windows version info (product name, version, description); release builds
  stamp the tag's version into it.
- The web UI has a favicon.
- `examples/demo`: three invented projects (`shop`, `blog`, `crm`) with
  simulated Vite and queue processes, for trying Ringmaster without a real
  Laravel app.

## 0.1.0 — 2026-10-23

First public release.

- Renamed from byob to Ringmaster.
- `ringmaster -version` (or `ringmaster version`) prints the version.
- Pushing a `v*` tag builds both binaries on GitHub Actions and publishes
  `ringmaster_<tag>_windows_amd64.zip` with a SHA-256 checksum.
- **Fixed: duplicate default ports.** Projects that omit `port` are now each
  allocated a **unique** port from `defaults.port_range`. Previously every
  port-less project was handed the first bindable port (usually 8000) because
  allocation ran before earlier projects' tasks had started listening — so
  `ringmaster serve` started several Laravel apps on the same port and the later
  ones failed. Declared project ports and external task ports are also
  excluded from allocation.
- `ps` and the web UI no longer show the project port next to tasks that do
  not own it (e.g. `vite`, `queue`); only the primary task and tasks with a
  `ready: {type: port}` probe carry a port.
- Clearer errors: a config missing `version: 1` now says exactly that (and
  points at `ringmaster.example.yaml`); a missing `ringmaster.yaml` suggests
  `copy ringmaster.example.yaml ringmaster.yaml`.
- `ringmaster --help` exits cleanly (no `flag: help requested` noise) and
  `ringmaster help` prints usage.

## M3 — 2026-07-21

- Graceful stop on Windows (CTRL_BREAK before Job Object kill).
- Console codepage decoding for non-UTF-8 output.
- macOS/Linux support: POSIX process groups, identity-checked reaping,
  `lsof` port-holder lookup, Unix PHP discovery.
- `external: true` observed-only tasks with per-task probe `port`.
- `requires_docker: true` preflight with a clear failure message.
- State file writes retry on Windows sharing violations.

## M2 — 2026-07-20

- `ringmaster serve` daemon: web UI + REST + WebSocket log streaming.
- CLI routes through the daemon when it is running.
- Manage projects and `php_roots` from the web UI; atomic `ringmaster.yaml`
  write-back.

## M1 — 2026-07-20

- `ringmaster.yaml` registry, PHP discovery, version-prefix resolution.
- Supervision with Windows Job Objects (no orphan processes).
- Port precheck with holder PID/image lookup; readiness probes; log ring
  buffers mirrored to files; identity-checked state reaping.

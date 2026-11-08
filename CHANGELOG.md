# Changelog

Notable, user-facing changes. Newest first.

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

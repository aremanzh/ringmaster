# Changelog

Notable, user-facing changes. Newest first.

## Unreleased

- **Fixed: duplicate default ports.** Projects that omit `port` are now each
  allocated a **unique** port from `defaults.port_range`. Previously every
  port-less project was handed the first bindable port (usually 8000) because
  allocation ran before earlier projects' tasks had started listening — so
  `byob serve` started several Laravel apps on the same port and the later
  ones failed. Declared project ports and external task ports are also
  excluded from allocation.
- `ps` and the web UI no longer show the project port next to tasks that do
  not own it (e.g. `vite`, `queue`); only the primary task and tasks with a
  `ready: {type: port}` probe carry a port.
- Clearer errors: a config missing `version: 1` now says exactly that (and
  points at `byob.example.yaml`); a missing `byob.yaml` suggests
  `copy byob.example.yaml byob.yaml`.
- `byob --help` exits cleanly (no `flag: help requested` noise) and
  `byob help` prints usage.

## M3 — 2026-07-21

- Graceful stop on Windows (CTRL_BREAK before Job Object kill).
- Console codepage decoding for non-UTF-8 output.
- macOS/Linux support: POSIX process groups, identity-checked reaping,
  `lsof` port-holder lookup, Unix PHP discovery.
- `external: true` observed-only tasks with per-task probe `port`.
- `requires_docker: true` preflight with a clear failure message.
- State file writes retry on Windows sharing violations.

## M2 — 2026-07-20

- `byob serve` daemon: web UI + REST + WebSocket log streaming.
- CLI routes through the daemon when it is running.
- Manage projects and `php_roots` from the web UI; atomic `byob.yaml`
  write-back.

## M1 — 2026-07-20

- `byob.yaml` registry, PHP discovery, version-prefix resolution.
- Supervision with Windows Job Objects (no orphan processes).
- Port precheck with holder PID/image lookup; readiness probes; log ring
  buffers mirrored to files; identity-checked state reaping.

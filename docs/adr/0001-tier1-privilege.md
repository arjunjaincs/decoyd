# ADR 0001 — Tier-1 / Tier-2 Privilege Model for Decoyd Watchers

**Status**: ACCEPTED  
**Date**: 2026-11-09  
**Amended**: 2026-11-10 (Phase 2 — owner decisions O1–O8 incorporated)  
**Authors**: batch/hardening-1, batch/tier1-v1  

---

## Context

Decoyd uses Linux inotify to detect reads of deployed canary-token files.
inotify requires no special privilege for files the process can already `open()`.
A second detection tier — fanotify — can intercept events on paths the process
does not own. fanotify requires `CAP_SYS_ADMIN` or root.

---

## Decision: Tier 1 fanotify (notification-only, hybrid with inotify)

The following owner decisions (O1–O8) are **ACCEPTED** and binding for
all Phase 2 implementation:

### O1 — fanotify class and fd handling

- fanotify instance class: `FAN_CLASS_NOTIF` (notification only).  
- Mode: plain fd mode — **no** `FAN_REPORT_FID`.  
- Mark type: inode marks via `FAN_MARK_ADD` with mask `FAN_OPEN|FAN_ACCESS`.  
- **Never** use any `FAN_*_PERM` flag in any form.  
- Close **every** event fd (defer immediately after read, even on parse errors).  
- Handle `FAN_Q_OVERFLOW`: log `"decoyd fanotify: event queue overflow"` and
  continue; do not stop the watcher.

### O2 — Hybrid fanotify + inotify

- fanotify supplies open/access events plus pid.  
- inotify keeps modify/move/delete events and the parent-directory watches,
  and re-adds the fanotify mark on file recreate.  
- While a token's fanotify mark is active, inotify open/access events **do
  not** alert for that token (deduplication).  
- If marking fails (EPERM, ENODEV, EOPNOTSUPP, ENOENT): fall back to inotify
  for that token silently and report the fallback in `decoyd doctor`.

### O3 — Privilege model

- Persistent daemon mode: a **system unit** running as root.  
- Binary at root-owned `/usr/local/bin/decoyd` (never a user-writable path).  
- Explicit `--data-dir` required (HOME changes under sudo).  
- `CapabilityBoundingSet=CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH`
  with rationale:
  - `CAP_SYS_ADMIN`: required for `fanotify_init(2)`.
  - `CAP_SYS_PTRACE`: required for reading `/proc/<pid>/exe` of other users.
  - `CAP_DAC_READ_SEARCH`: required for traversing 0700 home directories.  
- `NoNewPrivileges=yes`.  
- `ProtectSystem=strict`, `ReadWritePaths=/var/lib/decoyd`.  
- No `SystemCallFilter` unless validated on a real systemd installation.

### O4 — Root never writes into user-writable directories

- Daemon state (triggers.jsonl, pid): `/var/lib/decoyd` (root-owned, 0644 files, no secrets).  
- Alert config: copied one-way into `/etc/decoyd` (0600 root) by `sudo decoyd sync`.  
- TUI/CLI read the daemon's triggers.jsonl.

### O5 — Trust model for path reads

For every path read from a user-writable snapshot, the daemon must:
1. `Lstat` the path.
2. Refuse if it is a symlink (not `S_IFREG`).
3. Require the file is owned by the snapshot-recorded UID.
4. If any check fails: skip and log; never error-exit.

### O6 — Foreground tier-1 mode

`decoyd watch --tier1`: foreground mode for demos and CI.  
Errors immediately if Tier 1 (fanotify) is unavailable — no silent downgrade.

### O7 — Attribution fields

For every fanotify event, collect:
- pid, ppid chain (depth ≤ 8), comm, exe, cmdline (capped 4 KB).
- cwd, uid, start ticks, exe SHA-256 (size cap 100 MB, cached by path+mtime+size).
- Environment: read **only** `npm_package_name`, `npm_package_version`,
  `npm_lifecycle_event`; never store or log the full environ.
- Computed in the dispatch worker (not the event loop — avoid blocking reads).

### O8 — Events never contain token values

No TriggerEvent, alert payload, or log entry may contain the content of a
deployed token file. Token IDs and types are permitted; values are not.

---

## Implementation (Phase 2)

Tasks and files:

| Task | File(s) | Description |
|------|---------|-------------|
| T2 | `internal/watch/fanotify_linux.go` | fanotify watcher + event loop |
| T2 | `internal/watch/hybrid_linux.go` | hybrid fanotify+inotify coordinator |
| T3 | `internal/watch/procinfo_linux.go` | /proc attribution parser |
| T4 | `internal/attribution/attribution.go` | ProcessInfo, Attribution types |
| T5 | `internal/triglog/triglog.go` (amend) | Schema v1: Attribution + WatcherTier |
| T5 | `cmd/decoyd/cmd_events.go` | `decoyd events` subcommand |
| T6 | `cmd/decoyd/cmd_install_system_linux.go` | `--system`, `sync`, `uninstall --system` |

### Key constraints

- Event parsing: `encoding/binary`, no `unsafe`.
- Validate `event_len` and `metadata_len` before advancing buffer.
- Fuzz target: `FuzzParseFanotifyEvents` — never panics, never out-of-bounds.
- Source-level test: fails if any `_PERM` constant is referenced in package watch.
- Privileged integration test (build tag `privileged`): child `cat` reads decoy,
  expect event with matching pid and exe ending in `cat`.
- `FAN_Q_OVERFLOW`: log + continue (do not re-fire tokens in first version).

---

## Rejected options

**Option C (FAN_*_PERM)**: Permission events block the reading process.
Creates denial-of-service risk; requires `FAN_CLASS_CONTENT`; explicitly
prohibited by AGENTS.md rule 15 and this ADR.

**FAN_REPORT_FID**: Requires approved ADR amendment (AGENTS.md rule 15).
Not included in Phase 2.

**setuid binary**: Increases attack surface; system unit with bounded
capabilities is cleaner and auditable.

---

## References

- `fanotify(7)`: https://man7.org/linux/man-pages/man7/fanotify.7.html
- `fanotify_init(2)`: https://man7.org/linux/man-pages/man2/fanotify_init.2.html
- AGENTS.md rule 15 (fanotify constraints)
- ADR format: https://adr.github.io/

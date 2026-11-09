# ADR 0001 — Tier-1 / Tier-2 Privilege Model for Decoyd Watchers

**Status**: PROPOSED  
**Date**: 2026-11-09  
**Authors**: batch/hardening-1  

---

## Context

Decoyd uses Linux inotify to detect reads of deployed canary-token files.
inotify requires no special privilege for files the process can already `open()`.
A second detection tier — fanotify — can intercept events on paths the process
does not own, and can optionally block access (`FAN_*_PERM`). fanotify requires
`CAP_SYS_ADMIN` or root.

The question is: **how much privilege should Decoyd's watcher hold, and
through what mechanism?**

Three options were evaluated:

| Option | Mechanism | Privilege | Blocking? | ADR decision |
|--------|-----------|-----------|-----------|--------------|
| A | inotify only (current) | None | No | Tier 1 baseline |
| B | fanotify notification class via system unit | root or CAP_SYS_ADMIN | No | Preferred for Tier 2 |
| B+ | Option B + sudoers / systemd system unit | root | No | **Selected (PROPOSED)** |
| C | fanotify permission events (`FAN_*_PERM`) | root | Yes | Rejected — see below |

---

## Decision: Option B+ (sudoers / system unit, notification-only fanotify)

### What it means

1. **Tier 1 (default)**: The existing inotify-based watcher runs as the
   unprivileged user. No change to current behaviour. Detects reads on files
   the user owns.

2. **Tier 2 (opt-in)**: A separate process (`decoyd-watch-privileged`) runs
   as root under a **systemd system unit** (not a user unit). The binary is
   installed with `setuid root` or invoked via a sudoers rule that
   restricts it to a specific, non-writable path. It opens a fanotify
   instance with `FAN_CLASS_NOTIF` (notification class only — never
   `FAN_CLASS_CONTENT` or any `FAN_*_PERM` flag).

3. **CAP_SYS_PTRACE**: Not required. fanotify notification class does not
   need it. Option B+ intentionally avoids CAP_SYS_PTRACE.

4. **No `FAN_*_PERM` events**: Permission events that block file access
   are explicitly forbidden (AGENTS.md rule 15 and this ADR). They would
   introduce denial-of-service risk and require a separate `FAN_CLASS_CONTENT`
   instance.

5. **No `FAN_REPORT_FID`**: Not used unless a future ADR approves it
   (AGENTS.md rule 15).

### Rationale

- **Security**: Running as root is narrowed by the systemd `NoNewPrivileges`,
  `MemoryDenyWriteExecute`, `RestrictSUIDSGID`, and `SystemCallArchitectures`
  hardening already applied to the user unit (A11). The system unit will carry
  the same hardening plus `CapabilityBoundingSet=CAP_SYS_ADMIN`.
- **Simplicity**: No kernel module, no eBPF. fanotify is stable since
  Linux 2.6.37.
- **Reversibility**: Uninstalling the system unit leaves no residual
  privilege. The sudoers rule is in its own file under `/etc/sudoers.d/`.
- **Auditability**: The privileged binary has a minimal surface: open fanotify
  fd, loop on read, forward event JSON to unprivileged parent via unix socket.

### Rejected: Option C (FAN\_\*\_PERM)

Permission events block the process reading the file until Decoyd responds.
This creates a denial-of-service vector if Decoyd crashes or is slow. It also
requires `FAN_CLASS_CONTENT`, which cannot coexist safely with
`FAN_CLASS_NOTIF` on the same inotify instance. AGENTS.md rule 15 prohibits
`FAN_*_PERM` without an approved ADR — this ADR explicitly rejects it.

---

## Implementation notes (not yet implemented)

The following is design intent; no code exists yet. A future task must:

1. Create `internal/watch/watch_privileged_linux.go` — the privileged child
   process that opens fanotify and writes newline-delimited JSON events to
   stdout/a socket.
2. Update `cmd/decoyd/cmd_install_linux.go` to conditionally install the
   system unit when `--tier2` flag is passed.
3. Write a sudoers snippet to `docs/examples/decoyd.sudoers`.
4. Add an integration test (marked `//go:build integration`) that probes
   `fanotify_init(2)` and skips if `EPERM`.
5. Update `decoyd doctor` to report the achieved tier.

---

## References

- `fanotify(7)` man page: https://man7.org/linux/man-pages/man7/fanotify.7.html
- `fanotify_init(2)`: https://man7.org/linux/man-pages/man2/fanotify_init.2.html
- AGENTS.md rule 15 (fanotify constraints)
- ADR format: https://adr.github.io/

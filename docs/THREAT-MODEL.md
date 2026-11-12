# Decoyd Threat Model

## Scope

This document covers the Decoyd binary running on a single Linux or Windows host.

**Tier 1** (Linux, root / `CAP_SYS_ADMIN`): hybrid fanotify (`FAN_CLASS_NOTIF`) + inotify watcher with `/proc` process attribution.
ADR 0001 is ACCEPTED. Tier 1 implemented in v1.0.

**Tier 2** (Linux/Windows, no root): inotify/fsnotify-based watcher with token identity only (no process attribution).

## Assets

| Asset | Sensitivity | Location |
|-------|-------------|----------|
| Generated token files | High — exposure triggers incident response | User-chosen path |
| Alert config (`alert_config.json`) | High — contains webhook URLs / tokens | `~/.decoyd/` (user) or `/etc/decoyd/` (`0600` root, Tier 1 system daemon) |
| Trigger log (`triggers.jsonl`) | Medium — reveals token access history (no token values, O8) | `~/.decoyd/` (user) or `/var/lib/decoyd/` (`0644` non-secret, Tier 1 system daemon) |
| bbolt database (`decoyd.db`) | Medium — token metadata | `~/.decoyd/` (or `%APPDATA%\Decoyd\`; not opened by headless `watch`) |
| Decoyd binary | Low | `$PATH` or `/usr/local/bin/decoyd` (`0755` root for `--system`) |

## Threat actors

| Actor | Goal |
|-------|------|
| External attacker with file-system read access | Read a decoy → trigger alert (intended behaviour) |
| External attacker who gains write access | Overwrite/delete decoy silently, suppress alerts |
| Malicious alert sink | Inject content into alert messages |
| Local unprivileged process | Enumerate data dir, exfiltrate alert config |

## Mitigations

### T1 — Token fingerprinting
**Threat**: Token content reveals it is a decoy (static label, predictable format).
**Mitigation**: AGENTS.md Rule 12: no static identifiers in generated tokens (`decoyd`, `canary`, `decoy`, `fake`, `example`); AWS keys use uppercase base32 (`base32Upper`), kubeconfig generates randomized names, DNS canary omits instruction text. Tested by `TestNoStaticIdentifiers` and `TestNoStaticIdentifiers_CatchesBadTemplates`.

### T2 — Symlink attack on deploy
**Threat**: Attacker pre-places symlink at deploy path; Decoyd follows it and overwrites a victim file.
**Mitigation**: AGENTS.md Rule 14: `O_CREATE|O_EXCL|O_NOFOLLOW` on Linux; reparse-point check on Windows. Tested by `TestDeploy_SymlinkRefusal`.

### T3 — Alert injection
**Threat**: Token path, type, or process name contains ANSI/bidi/control chars that render deceptively in the alert channel.
**Mitigation**: AGENTS.md Rule 13: `internal/sanitize.Sanitize` and `AlertPayload.Sanitized()` strip control/ANSI/bidi/zero-width chars before payload construction; Discord sets `allowed_mentions: {"parse":[]}` with markdown escaping; Slack uses `plain_text` objects; Telegram/ntfy use plain text.

### T4 — Webhook redirect
**Threat**: Webhook URL redirects to an attacker-controlled host.
**Mitigation**: `CheckRedirect` returns `ErrWebhookRedirected` (`internal/alert/alert.go`); redirects are never followed.

### T5 — Huge / slow webhook response
**Threat**: Webhook server returns huge body or hangs, stalling the event loop.
**Mitigation**: `io.LimitReader` (`alertResponseBodyLimit = 64 * 1024`), `DialContext` timeout `5s`, `TLSHandshakeTimeout` `5s`, `ResponseHeaderTimeout` `10s`, request timeout `10s` (`internal/alert/alert.go`).

### T6 — Concurrent triglog corruption
**Threat**: Multiple Decoyd processes write to or edit `triggers.jsonl` concurrently.
**Mitigation**: `internal/filelock` advisory file lock (`flock` on Linux, `LockFileEx` on Windows) around `Append`, `DeleteOne`, and `ClearAll` in `internal/triglog/triglog.go`.

### T7 — Rate-limit suppression
**Threat**: Attacker reads the decoy many times to overflow the rate limit and suppress alerts.
**Mitigation**: Per-token sliding window counter sends a single suppression notice per window; rate-limited events are still appended to `triggers.jsonl`.

### T8 — Data-dir permissions and root trust isolation (O4, O5)
**Threat**: Unprivileged users read alert webhook credentials, or symlink/owner-spoofed paths in user snapshot trick root daemon.
**Mitigation**: Secret deploy directories use `0700`; system install puts secrets only in `/etc/decoyd/alert_config.json` (`0600`), never writes `$HOME`/user `--data-dir` (O4), and `validateTokenPathO5` `Lstat`s every snapshot path refusing symlinks and UID mismatches (O5). `decoyd doctor` warns on loose permissions.

### T9 — fanotify privilege surface (O1, O3)
**Threat**: The Tier 1 daemon runs as root; a vulnerability could allow privilege escalation.
**Mitigation**: ADR 0001 O1 & O3: notification class only (`FAN_CLASS_NOTIF`, never `FAN_*_PERM`, every event fd closed), `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/decoyd`, bounded `CapabilityBoundingSet` & `AmbientCapabilities` (`CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH`), binary installed at root-owned `/usr/local/bin/decoyd`.

### T10 — PID reuse in process attribution
**Threat**: A short-lived process touches the decoy; by the time Decoyd reads `/proc/<pid>/`, the PID has been reused by an innocent process.
**Mitigation**: `internal/watch/procinfo_linux.go` re-reads `/proc/<pid>/stat` starttime (`startTicks1`) after collecting `/proc` fields: if `startTicks` mismatches (PID reused), all `/proc`-derived fields (`PPID`, `Comm`, `Exe`, `ExeDeleted`, `ExeSHA256`, `Cmdline`, `Cwd`, `UID`, `StartTicks`, `NpmPackage`, `NpmVersion`, `NpmLifecycleEvent`) are discarded and `Confidence` drops to `ConfidenceLow` (`1`); if the process vanished before the post-read check (`err != nil`), fields read while alive are retained and `Confidence` drops to `ConfidenceLow` (`1`).

### T11 — Token value leakage in attribution (O7, O8)
**Threat**: Attribution fields (`cmdline`, `environ`) or logs leak the deployed token value.
**Mitigation**: ADR O7 & O8: `TriggerEvent` never stores file contents; `cmdline` is capped at 4096 bytes and sanitized; `/proc/<pid>/environ` reads ONLY `npm_package_name`, `npm_package_version`, and `npm_lifecycle_event`.

## Known limitations

- **Tier-2 `inotify` (Linux) and `fsnotify` (Windows)**: user-level `inotify` watches files the user owns; a process accessing raw block devices or `/proc/<pid>/mem` bypasses VFS file watches. On Windows, `ReadDirectoryChangesW` reports writes, renames, and deletes only (not pure read-only opens).
- **Root Tier-1 (`fanotify`) scope**: catches VFS `open`/`access` across users via `FAN_OPEN|FAN_ACCESS`, but offline disk image copies or raw block-device reads still bypass VFS.
- **Short-lived processes in `/proc` attribution**: attribution runs asynchronously in the dispatch worker (not a blocking `FAN_*_PERM` hook), so a process that exits before `/proc/<pid>/` is read may have partial metadata (`ConfidenceLow`).
- **Network filesystems** (NFS, SMB, 9p/drvfs): `inotify`/`fanotify` events are generally not generated for remote accesses on network mounts. `decoyd doctor` warns on `v9fs` (9p), `cifs`, and `fuseblk`/`drvfs` filesystems, but cannot prevent silent blind spots on all remote mounts.
- **Known false positives**: legitimate tools reading their own config at default paths (for example `npm config ls` and `npm ls` reading `~/.npmrc`, `git credential fill` reading `~/.git-credentials`, and `docker --version` reading `~/.docker/config.json`) trigger Decoyd because kernel `fanotify`/`inotify` observe all VFS reads to monitored decoy files regardless of caller process (no allowlist is implemented in v1.0).

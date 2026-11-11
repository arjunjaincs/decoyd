# Catching an npm Package Reading Decoy Credentials — Technical Notes

> **DRAFT** — Contains measured Tier 2 (`inotify`) results from our WSL2 test host.
> Items marked **UNKNOWN** require root/`fanotify` execution on a privileged Linux host.

## The Problem

A malicious npm `postinstall` script runs automatically during `npm install` (unless `--ignore-scripts`
is set) and executes with the installing user's file permissions, allowing it to read files like
`~/.npmrc`, `~/.pypirc`, or `~/.aws/credentials`.

## Decoyd's Design

Decoyd generates realistic-looking decoy credential files containing **no static identifiers** (checked
by an entropy-stripped denylist in `internal/tokens/tokens_test.go`), deploys them cleanly without
following symlinks (`O_NOFOLLOW | O_EXCL` on Linux), and alerts when a decoy file is accessed:

- **Tier 1 (`fanotify`, Linux root / `CAP_SYS_ADMIN`)**: hybrid `FAN_CLASS_NOTIF` (`FAN_OPEN|FAN_ACCESS`,
  never `*_PERM`) + `inotify` (write/move/delete + parent-dir recreate). Enriches events with `/proc/<pid>/`
  attribution (`pid`, `ppid` chain up to depth 8, `comm`, `exe`, `cmdline`, `cwd`, `uid`, `start_ticks`,
  `exe_sha256` <= 100 MB) and reads only `npm_package_name`, `npm_package_version`, and `npm_lifecycle_event`
  from `/proc/<pid>/environ`.
- **Tier 2 (`inotify` on Linux / `fsnotify` on Windows, unprivileged)**: detects file events and reports
  token identity without process attribution. Note that on Windows, `ReadDirectoryChangesW` reports writes,
  renames, and deletes only (not pure read-only opens).

## Usage

Generate and deploy decoy tokens using the interactive TUI (`decoyd`), or inspect headless state under a
data directory containing `deployed_tokens.json` and `alert_config.json`:

```bash
# Install
go install github.com/arjunjaincs/decoyd/cmd/decoyd@latest

# Launch TUI to generate & deploy decoy credential files
decoyd

# Start headless watcher (Tier 2 unprivileged, or Tier 1 on Linux with sudo)
decoyd watch                # Tier 2 (inotify/fsnotify)
sudo decoyd watch --tier1   # Tier 1 (fanotify; errors if unavailable)

# Simulate a benign credential-scanning npm postinstall hook
npm install --no-save ./examples/demo-evil-pkg

# Inspect events (table or raw TriggerEvent JSON) and fail CI builds if triggered
decoyd events --json
decoyd events --fail-if-any  # exits 3 if events exist
```

An actual Tier 1 `TriggerEvent` record uses this structure (see [EVENT-SCHEMA.md](../EVENT-SCHEMA.md)):

```json
{
  "id": "8f4a2c1e9b3d7a05",
  "token_id": "tok-abc123",
  "token_type": "aws_credentials",
  "path": "/home/runner/.aws/credentials",
  "triggered_at": "2026-11-10T10:00:05Z",
  "event_type": "access",
  "status": "sent",
  "watcher_tier": "tier1",
  "attribution": {
    "accessor": {
      "pid": 4567,
      "ppid": 4000,
      "comm": "node",
      "exe": "/usr/bin/node",
      "cmdline": "node install.js",
      "cwd": "/tmp/evil-pkg",
      "uid": 1001,
      "start_ticks": 998877
    },
    "confidence": 3,
    "npm_package": "demo-evil-pkg",
    "npm_version": "0.0.1",
    "npm_lifecycle_event": "postinstall"
  }
}
```

## Measured Performance (`6.18.58.1-microsoft-standard-WSL2`, i7-1265U, `go1.25.0`) [RAN]

All measurements below were collected against compiled `decoyd watch` (Tier 2 `inotify`, default `2s`
per-path debounce). Tier 1 (`fanotify`) end-to-end benchmark figures are **UNKNOWN** locally because
this WSL2 test environment did not permit non-interactive `sudo` (`FAN_CLASS_NOTIF`).

### Time-to-Alert (100 sequential child `cat` reads -> local HTTP webhook)

- **p50**: `2008.90 ms` [RAN]
- **p95**: `2011.19 ms` (rank 95/100; rank 96: `2011.34 ms`) [RAN]
- **Max**: `2063.36 ms` [RAN]
- **Min / Mean**: `2005.76 ms` / `2009.31 ms` [RAN]
- **Tier 1 (`fanotify`)**: **UNKNOWN** — run `sudo -E decoyd watch --tier1 --data-dir <dir>` on a Linux host with `FAN_CLASS_NOTIF`.

(The `~2000 ms` floor comes directly from the default `2s` per-path debounce timer before webhook dispatch.)

### Idle Memory (RSS) and CPU Over 30 Seconds

- **1 decoy**: `10.90 MiB` avg RSS (`11,162 KiB`, range `11,160–11,172 KiB`), `0.033% CPU` (`1` tick / `30.02s`) [RAN]
- **100 decoys**: `11.21 MiB` avg RSS (`11,474 KiB`, range `11,464–11,476 KiB`), `0.067% CPU` (`2` ticks / `30.02s`) [RAN]
- **1000 decoys**: `12.52 MiB` avg RSS (`12,823 KiB`, range `12,780–12,844 KiB`), `0.033% CPU` (`1` tick / `30.02s`) [RAN]

### False-Positive Observations (Tier 2 `inotify`)

Zero webhook alerts fired across `ls -la`, `stat`, `find`, `git status`, `grep -r` in a sibling directory,
and a 50-iteration `lstat`/`stat` loop; the `cat` positive control fired `1` alert [RAN].

## Current Status

Tier 1 `fanotify` lifecycle proof (`install --system`, `systemctl status`, child decoy read, `triggers.jsonl`
schema check, unprivileged `decoyd events --json`, `sync`, and `uninstall --system`) runs in the GitHub
Actions `system-lifecycle` CI job on `ubuntu-latest`.

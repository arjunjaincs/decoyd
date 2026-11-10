# Decoyd Event Schema v1

> **Schema version**: 1 (introduced in T5 / `batch/tier1-v1`)
>
> Old events (schema v0, without `watcher_tier` and `attribution`) load
> correctly from `triggers.jsonl`; both fields are `omitempty`.

## Overview

Decoyd appends JSON records to `<data-dir>/triggers.jsonl` whenever a deployed
canary token file is accessed. The `decoyd events` command reads and formats
these records.

## Record Format

Each line is a JSON object conforming to the following schema:

```jsonc
{
  // Required fields (present in all schema versions)
  "id":           "8f4a2c...",          // random 8-byte hex event ID (deduplication)
  "token_id":     "tok-abc123",         // ID of the token that triggered
  "token_type":   "aws_credentials",   // token type (e.g. "aws_credentials", "github_pat")
  "path":         "/home/user/.aws/credentials",  // absolute path of the accessed file
  "triggered_at": "2026-11-10T10:00:00Z",          // UTC RFC3339 timestamp
  "event_type":   "access",            // inotify/fanotify event: "access" | "write" | "rename" | "delete"
  "status":       "sent",             // see Status Values below

  // Optional fields added in schema v1 (omitted for pre-v1 events)
  "alert_error":   "...",              // present when status == "failed"; sanitized error string
  "watcher_tier":  "tier1",           // "tier1" (fanotify) or "tier2" (inotify); empty = tier2
  "attribution":   { ... }            // process/package attribution; tier1 only (see below)
}
```

## Status Values

| Status | Meaning |
|--------|---------|
| `pending` | Written before alert dispatch (durability record; superseded by final status) |
| `sent` | Alert dispatched successfully |
| `failed` | Alert dispatch failed; see `alert_error` for sanitized reason |
| `rate_limited` | Suppressed by per-token rate limiter (default: 5 alerts/token/hour) |
| `quiet_hours` | Suppressed because event fell in configured quiet hours window |

> [!NOTE]
> `triggers.jsonl` deduplicates by `id` on read — the latest record per ID
> wins. A `pending` record followed by a `sent` record for the same ID will
> show as `sent` in `decoyd events` output.

## Attribution Object (Tier 1 only)

Present only when `watcher_tier == "tier1"` (fanotify-based detection).
All string fields are pre-sanitized (ANSI, bidi, C0/C1 stripped).
**Token values are never stored here (O8).**

```jsonc
{
  "accessor": {              // the process that opened / accessed the token file
    "pid":         1234,
    "ppid":        1000,
    "comm":        "cat",           // /proc/<pid>/comm (15-char max)
    "exe":         "/usr/bin/cat",  // readlink /proc/<pid>/exe
    "exe_deleted": false,           // true when exe had " (deleted)" suffix
    "cmdline":     "cat secret",    // NUL→space, capped 4096 chars
    "cwd":         "/home/user",    // readlink /proc/<pid>/cwd
    "uid":         1000,            // real UID from /proc/<pid>/status
    "start_ticks": 123456,          // field 22 of /proc/<pid>/stat (PID reuse guard)
    "exe_sha256":  "deadbeef..."    // SHA-256 hex of exe (omitted if >100 MB or deleted)
  },
  "ancestors": [             // PPID chain, depth ≤ 8; index 0 = immediate parent
    { /* ProcessInfo */ },
    { /* ProcessInfo */ }
  ],
  "confidence":  "high",     // "low" | "medium" | "high" (see below)

  // npm environment variables — only these three are captured (O7):
  "npm_package":         "lodash",   // npm_package_name
  "npm_version":         "4.17.21",  // npm_package_version
  "npm_lifecycle_event": "install"   // npm_lifecycle_event
}
```

### Confidence Levels

| Level | Meaning |
|-------|---------|
| `low` | PID read but `/proc/<pid>/` vanished before stat could complete, or PID reused between reads |
| `medium` | exe resolved, ppid chain read; SHA-256 not computed (file deleted, or hash error) |
| `high` | exe read and SHA-256 computed successfully (file ≤ 100 MB) |

> [!IMPORTANT]
> Attribution is **best-effort**: a process can exit before `/proc` is read.
> Short-lived commands (e.g. `cat secret; rm -f secret`) may appear with
> `confidence: low` and empty `exe`. This is expected behaviour, not a bug.

## Watcher Tiers

| Tier | Technology | Platforms | Access Detection |
|------|-----------|-----------|-----------------|
| `tier1` | fanotify + inotify | Linux (requires `CAP_SYS_ADMIN`) | open, access, write, rename, delete + process attribution |
| `tier2` | inotify | Linux | open, access (via IN_OPEN/IN_ACCESS), write, rename, delete |
| `tier2` | fsnotify (ReadDirectoryChangesW) | Windows | write, rename, delete only (read-only opens not detectable) |

> [!WARNING]
> On Windows (tier2), **pure read-only file access is not detectable**. This
> is a known v1 limitation of the ReadDirectoryChangesW API. See the Windows
> watcher source for details.

## decoyd events CLI

```
decoyd events [flags]

Flags:
  --json              Output raw JSON array instead of table
  --since DURATION    Only show events from the last DURATION (e.g. 1h, 30m, 24h)
  --token ID          Filter to events for a specific token ID
  --limit N           Show only the last N events
  --fail-if-any       Exit with code 3 if any events are found (use in CI)

Exit codes:
  0   Success (or no events when --fail-if-any)
  1   Fatal error
  3   Events found and --fail-if-any is set
```

## Example: CI Integration

```yaml
- name: Check for credential access
  run: |
    decoyd events --fail-if-any --since 10m --json
  # Exit code 3 if any decoy was accessed; fail the build.
```

## Example: JSON Output

```json
[
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
        "start_ticks": 998877,
        "exe_sha256": "a1b2c3..."
      },
      "ancestors": [],
      "confidence": "high",
      "npm_package": "evil-pkg",
      "npm_version": "1.0.0",
      "npm_lifecycle_event": "postinstall"
    }
  }
]
```

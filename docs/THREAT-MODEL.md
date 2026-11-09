# Decoyd Threat Model

## Scope

This document covers the Decoyd binary running in Tier-1 mode (inotify,
no elevated privilege) on a single Linux or Windows host. Tier-2 (fanotify)
is PROPOSED in ADR 0001 and not yet implemented.

## Assets

| Asset | Sensitivity | Location |
|-------|-------------|----------|
| Generated token files | High — exposure triggers incident response | User-chosen path |
| Alert config (`alert_config.json`) | High — contains webhook URLs / tokens | `~/.decoyd/` |
| Trigger log (`triggers.jsonl`) | Medium — reveals token access history | `~/.decoyd/` |
| bbolt database (`decoyd.db`) | Medium — token metadata | `~/.decoyd/` |
| Decoyd binary | Low | `$PATH` |

## Threat actors

| Actor | Goal |
|-------|------|
| External attacker with file-system read access | Read a decoy → trigger alert (intended behaviour) |
| External attacker who gains write access | Overwrite/delete decoy silently, suppress alerts |
| Malicious alert sink | Inject content into alert messages |
| Local unprivileged process | Enumerate data dir, exfiltrate config |

## Mitigations

### T1 — Token fingerprinting
**Threat**: Token content reveals it is a decoy (static label, predictable format).  
**Mitigation**: Rule 12 + A7: no static identifiers, AWS body is base32, kubeconfig uses random names, DNS canary has no instruction text.

### T2 — Symlink attack on deploy
**Threat**: Attacker pre-places symlink at deploy path; Decoyd follows it and overwrites a victim file.  
**Mitigation**: Rule 14 + A6: `O_NOFOLLOW | O_EXCL` on Linux; reparse-point check on Windows. Test: `TestDeploy_SymlinkRefusal`.

### T3 — Alert injection
**Threat**: Token path, type, or process name contains ANSI/bidi/control chars that render deceptively in the alert channel.  
**Mitigation**: Rule 13 + A9: `internal/sanitize.Sanitize` applied before `AlertPayload` construction; Discord `allowed_mentions: {"parse":[]}` ; Slack `plain_text` objects.

### T4 — Webhook redirect
**Threat**: Webhook URL redirects to an attacker-controlled host.  
**Mitigation**: A8: `CheckRedirect` returns `ErrWebhookRedirected`; no automatic follow.

### T5 — Huge / slow webhook response
**Threat**: Webhook server returns huge body or hangs, stalling the event loop.  
**Mitigation**: A8: `io.LimitReader(64 KB)`, `ResponseHeaderTimeout: 10 s`, `DialTimeout: 5 s`.

### T6 — Concurrent triglog corruption
**Threat**: Two watcher processes write to `triggers.jsonl` concurrently.  
**Mitigation**: A12: `internal/filelock` advisory lock (flock on Linux, LockFileEx on Windows) around all Append/DeleteOne/ClearAll calls.

### T7 — Rate-limit suppression
**Threat**: Attacker reads the decoy many times to overflow the rate limit and suppress alerts.  
**Mitigation**: A13: exactly one suppression alert is sent per token per rate-limit window. Rate-limited events are still logged to `triggers.jsonl`.

### T8 — Data-dir permissions
**Threat**: Other users on the system read alert config (webhook credentials) or token metadata.  
**Mitigation**: Deploy creates secret-type directories with `0700`; `decoyd doctor` warns if data-dir permissions are loose.

## Known limitations

- **Tier-1 inotify** only watches files the user owns. A privileged process
  reading the decoy via `/proc/<pid>/mem` or direct block device access does
  not trigger an alert (would require Tier-2 fanotify — see ADR 0001).
- **Network filesystems** (NFS, SMB): inotify events are not generated for
  remote opens. Decoys on network paths are silently unmonitored. `decoyd
  doctor` warns on 9p/drvfs filesystems but cannot detect all NFS mounts.
- **Offline attacker**: copying the entire disk image bypasses all monitoring.

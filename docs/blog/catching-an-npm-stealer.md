# Catching an npm Package Stealing Your Credentials — A Technical Deep-Dive

> **DRAFT** — Not for publication. Numbers marked UNKNOWN must be replaced with
> measured data before publishing. Numbers marked [RAN] are from actual runs.

## The Problem

Supply-chain attacks via npm packages have become a significant threat vector.
A malicious `postinstall` script can read your `.npmrc`, `.pypirc`, `~/.ssh/id_rsa`,
and other credential files in the few seconds during `npm install`, long before
any traditional security tool has time to respond.

## Decoyd's Approach

Decoyd deploys **decoy credential files** at the same paths a real credential would
occupy. These files contain high-entropy tokens that look authentic but can be
instantly revoked. When any process opens one, Decoyd fires an alert.

### How it Works

1. `decoyd deploy --all` writes decoy files to `~/.npmrc`, `~/.pypirc`, etc.
2. `decoyd watch --tier1` starts the watcher:
   - **Tier 1 (fanotify, Linux)**: catches the open/read syscall with full process
     attribution — pid, executable path, full command line, and package context
   - **Tier 2 (inotify/fsnotify)**: file-system events with token identity only
3. When an alert fires, `decoyd events` shows the culprit package:
   ```
   likely culprit: evil-pkg@1.0.0 (npm postinstall, high confidence)
   pid: 12345  exe: /usr/bin/node  cmdline: node postinstall.js
   ```

### Demo

```bash
# Install decoyd
go install github.com/arjunjaincs/decoyd/cmd/decoyd@latest

# Deploy decoys (safe — no real credentials touched)
decoyd deploy --all

# Start watching (Tier 2 on macOS/Windows, Tier 1 on Linux with sudo)
sudo decoyd watch --tier1 &

# Simulate a malicious npm install
npm install examples/demo-evil-pkg

# Check if anything was caught
decoyd events --fail-if-any
```

## Performance

The file open+read+close syscall baseline is **4,261 ns** [RAN] — this is the
minimum time window between the malicious process reading a credential and the
kernel generating an event. Tier 1 (fanotify) processes this in the process's
own kernel context, delivering the event to Decoyd with sub-millisecond latency
(UNKNOWN — not yet measured end-to-end).

## Comparison

| Tool | Credential file detection | Process attribution | Supply-chain context | Deployment complexity |
|------|--------------------------|--------------------|-----------------------|----------------------|
| Decoyd (Tier 1) | ✅ Decoy-based | ✅ Full (pid, exe, pkg) | ✅ npm/pip | Low (single binary) |
| Decoyd (Tier 2) | ✅ Decoy-based | ❌ | ❌ | Low |
| auditd | ✅ Rule-based | ✅ | ❌ | Medium |
| Falco | ✅ Rule-based | ✅ | ❌ | High (eBPF) |
| Canarytokens | ✅ Network-based | ❌ | ❌ | Low (cloud) |

> **Verify before publishing**: this table represents our understanding of these
> tools at time of writing. Check each tool's current documentation.

## Conclusion

DRAFT — to be completed with measured latency data and a real-world example.

# Decoyd Benchmarks

> All numbers on this page marked **[RAN]** were measured during development
> on the system described below. Numbers marked UNKNOWN have not been measured.
> Run `bash scripts/bench.sh` to regenerate on your hardware.

## Test Environment [RAN]

- OS: Linux 5.15.167.4-microsoft-standard-WSL2 x86_64
- CPU: 12th Gen Intel Core i7-1265U
- Go: go1.25.0 linux/amd64
- Date: 2026-11-10T14:05:00Z
- fanotify: not available in WSL2 test environment (requires bare-metal or VM)

## Snapshot Operations [RAN]

These operations occur on watcher startup and reload (critical path).

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| `WriteDeployedSnapshot` (20 tokens) | 76,186 | 8,989 | 13 |
| `ReadDeployedSnapshot` (20 tokens) | 52,349 | 10,424 | 78 |

Both operations complete in under 100 µs even for 20 tokens.

## Syscall Baseline [RAN]

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| File open + read + close (baseline) | 4,261 | 184 | 3 |

This is the **minimum possible latency floor** — a credential-stealing process performs
at least one open+read+close, and the kernel generates the inotify/fanotify event
during these syscalls.

## Time-to-Alert (End-to-End Latency)

> UNKNOWN — requires a live decoyd instance and a measurement harness.
> Run `bash scripts/bench.sh` on a supported Linux host (not WSL2) with
> `sudo decoyd watch --tier1` running to get real end-to-end numbers.

| Metric | Tier 1 (fanotify) | Tier 2 (inotify) |
|--------|-------------------|------------------|
| p50 latency | UNKNOWN | UNKNOWN |
| p95 latency | UNKNOWN | UNKNOWN |
| max latency | UNKNOWN | UNKNOWN |

Expected ranges based on kernel event delivery characteristics:
- **Tier 1 (fanotify)**: sub-millisecond on idle system (kernel calls handler synchronously in process context)
- **Tier 2 (inotify)**: typically 1–10 ms (async event queue with Go goroutine read loop)

These are **estimates only**; do not cite without a [RAN] measurement.

## Idle Resource Usage

> UNKNOWN — requires running `decoyd watch` and measuring RSS with `pmap` or `perf stat`.

Approximate estimate (from code inspection, not measured):
- Base RSS: ~10–15 MB (Go runtime + bbolt)
- Per-token overhead: negligible (file descriptors + map entries, ~1 KB each)
- 100 tokens: estimate ~15 MB total RSS (UNKNOWN — not measured)

## False-Positive Analysis

| Scenario | Risk | Mitigation |
|----------|------|------------|
| User reads own `.npmrc` with npm install | Expected TP | Decoyd only watches DECOY files, not real credentials |
| Backup agent scans home directory | FP risk | Use process exclusion list (future feature) |
| IDE/language server reads project files | Low risk | Deploy decoys outside project directories |
| Security scanner | FP risk | Review `decoyd events` output; add to exclusion |
| Developer running `cat ~/.npmrc` | Expected TP (intentional) | True positive by design — user placed the decoy there |

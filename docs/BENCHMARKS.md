# Decoyd Benchmarks

Every number below marked **[RAN]** was directly measured in an execution run on
the host environment listed below. Metrics requiring unobtainable local privileges
are explicitly marked **UNKNOWN** with the exact command to reproduce them.

## Test Environment [RAN]

- Kernel (`uname -r`): `6.18.58.1-microsoft-standard-WSL2`
- CPU (`lscpu | head -20`): `12th Gen Intel(R) Core(TM) i7-1265U` (12 CPUs)
- Go (`go version`): `go1.25.0 linux/amd64` (module `go 1.25.0`, toolchain `go1.26.10`)
- WSL2: `yes` (`Linux version 6.18.58.1-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Tue Oct 27 00:24:11 UTC 2026`)
- Tier 1 (`fanotify`) locally: `UNKNOWN` (unprivileged WSL2 cannot run interactive `sudo` / `FAN_CLASS_NOTIF`; `decoyd watch` falls back to Tier 2 `inotify`)

## (a) Time-to-Alert Over 100 Runs (Child Read -> Local Webhook)

Methodology: 100 deployed decoy files watched by compiled `decoyd watch --data-dir <tmp>`.
For each iteration `i = 0..99`, `cat <decoy_i>` runs as a child process at `t0`, and the local
`httptest`/loopback HTTP webhook records receipt time `RecvAt` (`RecvAt - t0`).
Default per-path debounce (`2s` = `2,000 ms`) is active in the headless watcher.

| Metric | Tier 1 (fanotify) | Tier 2 (inotify, 2s debounce) [RAN] |
|--------|-------------------|-------------------------------------|
| Sample size | UNKNOWN | `100` runs |
| Min | UNKNOWN | `2005.76 ms` |
| p50 (median) | UNKNOWN | `2008.90 ms` |
| Mean | UNKNOWN | `2009.31 ms` |
| p95 | UNKNOWN | `2011.19 ms` (rank 95/100; rank 96: `2011.34 ms`) |
| Max | UNKNOWN | `2063.36 ms` |

To measure Tier 1 (`fanotify`) on a Linux host with root/`CAP_SYS_ADMIN`, run:

```bash
sudo -E decoyd watch --tier1 --data-dir <data-dir>
# or verify Tier 1 child read detection end-to-end:
go test -c -tags privileged -o /tmp/watch_priv.test ./internal/watch/ && sudo -E /tmp/watch_priv.test -test.v -test.run TestFanotify
```

## (b) Idle RSS and CPU Over 30 Seconds (`N = 1, 100, 1000` Decoys)

Methodology: Headless `decoyd watch` running idle for `30.02 s` per decoy count, sampled once
per second from `/proc/<pid>/status` (`VmRSS`) and `/proc/<pid>/stat` (`utime + stime` CPU
clock ticks at Linux `USER_HZ = 100 Hz`), cross-checked with `ps -o rss=,pcpu= -p <pid>`.

### Tier 2 (`inotify`) [RAN]

| Decoy count | RSS Min (KiB) | RSS Avg (KiB / MiB) | RSS Max (KiB) | CPU ticks (`delta_ticks` / 30.02 s) | CPU % (`USER_HZ=100`) | `ps` snapshot (`rss pcpu`) |
|------------:|--------------:|--------------------:|--------------:|-------------------------------------:|----------------------:|----------------------------|
| 1 | 11,160 | 11,162 (`10.90 MiB`) | 11,172 | 1 tick (`0.010 s`) | `0.033%` | `11172  0.0` |
| 100 | 11,464 | 11,474 (`11.21 MiB`) | 11,476 | 2 ticks (`0.020 s`) | `0.067%` | `11476  0.0` |
| 1000 | 12,780 | 12,823 (`12.52 MiB`) | 12,844 | 1 tick (`0.010 s`) | `0.033%` | `12844  0.0` |

### Tier 1 (`fanotify`)

> UNKNOWN — requires root/`CAP_SYS_ADMIN` on Linux:
> `sudo -E decoyd watch --tier1 --data-dir <data-dir>` and sample `/proc/<pid>/status` (`VmRSS`) and `/proc/<pid>/stat`.

## (c) False-Positive Suite (Tier 2 `inotify`, 2s Debounce, 2.6s Settle Window) [RAN]

Setup: Git repository containing decoy files (`.npmrc`, `.env`, `.aws/credentials`), source
file `src/app.js`, and a sibling directory (`sibling/notes.txt`). Webhook queue was drained
before each operation and inspected for `2.6 s` afterward.

| Operation | Alerts Triggered | Outcome |
|-----------|-----------------:|---------|
| `ls -la <tree>` | 0 | No false positive |
| `stat <decoy>` (`.npmrc`, `.env`) | 0 | No false positive |
| `find <tree> -maxdepth 3` | 0 | No false positive |
| `git status` (in repo with decoys) | 0 | No false positive |
| `grep -rn API_TOKEN <sibling-dir>` | 0 | No false positive |
| Editor-style `lstat`/`stat` loop (`50x`, `10 ms` interval) | 0 | No false positive |
| Positive control: `cat <decoy>` | 1 | Expected alert |

Tier 1 (`fanotify`) false-positive sweep on this exact harness: `UNKNOWN` (requires root/`CAP_SYS_ADMIN`).

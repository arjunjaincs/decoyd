# Decoyd Benchmarks

Every number below marked **[RAN]** was measured directly in an execution run on the
host environment listed in its section and stored as an immutable artifact (CI run
[`43161750870`](https://github.com/arjunjaincs/decoyd/actions/runs/43161750870), job
`Benchmarks (Tier 1 fanotify vs Tier 2 inotify)`, artifact `tier1-benchmarks`).

## Environments [RAN]

### Environment A — GitHub Actions `ubuntu-latest` CI Runner (`tier1-benchmarks`)

- Runner image: `ubuntu26` (`Ubuntu 26.04.1 LTS (Resolute Raccoon)`, `ImageVersion=20261101.186.1`)
- Kernel (`uname -r`): `7.0.0-1019-azure` (`#20-Ubuntu SMP PREEMPT Mon Oct 19 22:41:16 UTC 2026 x86_64`)
- CPU (`lscpu | head -20`): `AMD EPYC 7763 64-Core Processor`, 4 vCPUs (`2` cores × `2` threads, Microsoft Hyper-V full virtualization)
- Go (`go version`): `go1.27.4 linux/amd64` (module `go 1.25.0`, pinned toolchain `go1.26.10`)
- Privilege: `sudo` / `CAP_SYS_ADMIN` (Tier 1 `fanotify`) and unprivileged user (Tier 2 `inotify`)

### Environment B — Local WSL2 Development Host

- Kernel (`uname -r`): `6.18.58.1-microsoft-standard-WSL2` (`#1 SMP PREEMPT_DYNAMIC Tue Oct 27 00:24:11 UTC 2026`)
- CPU (`lscpu | head -20`): `12th Gen Intel(R) Core(TM) i7-1265U` (`12` CPUs)
- Go (`go version`): `go1.25.0 linux/amd64` (local build toolchain `go1.26.10`)
- Privilege: unprivileged (`Tier 2 inotify` only)

---

## (a) Time-to-Alert Over 100 Runs (Child Read -> Event Recorded / Delivered)

### CI Runner Apples-to-Apples (`Environment A`: Child Read -> `triggers.jsonl`) [RAN]

**Measurement method** (`bench_suite.py`, CI run [`43161750870`](https://github.com/arjunjaincs/decoyd/actions/runs/43161750870)):
100 distinct deployed decoy files are monitored by compiled `decoyd watch --tier1`
(Tier 1) or `decoyd watch` (Tier 2) with a loopback HTTP webhook configured so each
alert completes full delivery (`pending` -> `sent`). For each decoy `i = 0..99`, the
harness forks a child process that captures `t0 = time.monotonic_ns()`, opens and reads
64 bytes of `decoy_i`, writes `t0` over a pipe, sleeps `10 ms`, and exits. The parent
polls `triggers.jsonl` (`0.2 ms` interval for Tier 1; `0.5 ms` for Tier 2) and records
`found_ns = time.monotonic_ns()` as soon as the event JSON line for `tok["id"]`
appears (`latency_ms = (found_ns - t0) / 1e6`).

| Metric | Tier 1 (`fanotify`) [RAN] | Tier 2 (`inotify`, `2s` debounce) [RAN] |
|--------|---------------------------:|----------------------------------------:|
| Sample size | `100` runs | `100` runs |
| Min | `6.493 ms` | `2000.952 ms` |
| p50 (median) | `7.677 ms` | `2003.355 ms` |
| Mean | `7.804 ms` | `2003.187 ms` |
| p95 (rank 95 / rank 96) | `8.526 ms` / `8.577 ms` | `2004.653 ms` / `2004.684 ms` |
| Max | `35.084 ms` | `2007.110 ms` |

Why Tier 2 sits near `2,000 ms`: the `inotify` loop in `internal/watch/watch_linux.go`
holds file events in a per-path `2 s` debounce map before dispatching (`safeDispatch` ->
`triglog.Append`), whereas `dispatchFanotifyEvent` appends to `triggers.jsonl`
immediately upon receiving the `fanotify` event.

### Local WSL2 Tier 2 End-to-End (`Environment B`: Child `cat` -> Loopback Webhook) [RAN]

Same 100-decoy pattern, measuring receipt timestamp `RecvAt - t0` directly inside the
local `httptest` webhook handler:

- Sample size: `100` runs
- Min / Mean: `2005.76 ms` / `2009.31 ms`
- p50 (median): `2008.90 ms`
- p95: `2011.19 ms` (rank 95/100; rank 96: `2011.34 ms`)
- Max: `2063.36 ms`

---

## (b) Idle RSS and CPU Over 30 Seconds (`N = 1, 100, 1000` Decoys)

**Methodology**: Compiled `decoyd watch` running idle for `30 s` (`wall_s = 30.006 s` in
CI, `30.02 s` on WSL2) after startup, sampling `/proc/<pid>/status` (`VmRSS`) once per
second and `/proc/<pid>/stat` (`utime + stime` CPU clock ticks at `USER_HZ = 100 Hz`),
cross-checked with `ps -o rss=,pcpu= -p <pid>`.

### Tier 1 (`fanotify` + `inotify`, `Environment A` CI Runner) [RAN]

| Decoy count | RSS Min (KiB) | RSS Avg (KiB / MiB) | RSS Max (KiB) | CPU ticks (`delta_ticks` / `30.006 s`) | CPU % (`USER_HZ=100`) | `ps` snapshot (`rss pcpu`) |
|------------:|--------------:|--------------------:|--------------:|---------------------------------------:|----------------------:|----------------------------|
| 1 | 11,484 | 11,622.7 (`11.35 MiB`) | 11,664 | 0 ticks (`0.000 s`) | `0.0000%` | `11664  0.0` |
| 100 | 11,672 | 11,816.3 (`11.54 MiB`) | 11,848 | 1 tick (`0.010 s`) | `0.0333%` | `11848  0.0` |
| 1,000 | 14,048 | 14,092.9 (`13.76 MiB`) | 14,100 | 0 ticks (`0.000 s`) | `0.0000%` | `14100  0.0` |

### Tier 2 (`inotify` only, `Environment B` WSL2) [RAN]

| Decoy count | RSS Min (KiB) | RSS Avg (KiB / MiB) | RSS Max (KiB) | CPU ticks (`delta_ticks` / `30.02 s`) | CPU % (`USER_HZ=100`) | `ps` snapshot (`rss pcpu`) |
|------------:|--------------:|--------------------:|--------------:|-------------------------------------:|----------------------:|----------------------------|
| 1 | 11,160 | 11,162 (`10.90 MiB`) | 11,172 | 1 tick (`0.010 s`) | `0.033%` | `11172  0.0` |
| 100 | 11,464 | 11,474 (`11.21 MiB`) | 11,476 | 2 ticks (`0.020 s`) | `0.067%` | `11476  0.0` |
| 1,000 | 12,780 | 12,823 (`12.52 MiB`) | 12,844 | 1 tick (`0.010 s`) | `0.033%` | `12844  0.0` |

---

## (c) False-Positive Suite (`2.6 s` Settle Window per Operation)

Setup: Git repository containing three deployed decoys (`.npmrc`, `.env`,
`.aws/credentials`), source file `src/app.js`, and a sibling directory (`sibling/notes.txt`).
Each operation is followed by a `2.6 s` settle pause and checked against `triggers.jsonl`
delta (Tier 1 in `Environment A`) or webhook receipts (Tier 2 in `Environment B`).

### Tier 1 (`fanotify` + `inotify`, `Environment A` CI Runner) [RAN]

| Operation | `triggers.jsonl` lines delta | Outcome |
|-----------|-----------------------------:|---------|
| `ls -la <tree>` | 0 | No false positive |
| `stat <decoy>` (`.npmrc`, `.env`) | 0 | No false positive |
| `find <tree> -maxdepth 3` | 0 | No false positive |
| `git status` (in repo with decoys) | 0 | No false positive |
| `grep -rn API_TOKEN <sibling-dir>` | 0 | No false positive |
| Editor-style `lstat`/`stat` loop (`50x`, `10 ms` interval) | 0 | No false positive |
| Positive control: `cat <decoy>` (`.env`) | 2 (`pending` + `sent`) | Expected trigger (1 alert delivered) |

### Tier 2 (`inotify`, `2s` Debounce, `Environment B` WSL2) [RAN]

| Operation | Alerts Triggered | Outcome |
|-----------|-----------------:|---------|
| `ls -la <tree>` | 0 | No false positive |
| `stat <decoy>` (`.npmrc`, `.env`) | 0 | No false positive |
| `find <tree> -maxdepth 3` | 0 | No false positive |
| `git status` (in repo with decoys) | 0 | No false positive |
| `grep -rn API_TOKEN <sibling-dir>` | 0 | No false positive |
| Editor-style `lstat`/`stat` loop (`50x`, `10 ms` interval) | 0 | No false positive |
| Positive control: `cat <decoy>` | 1 | Expected alert |

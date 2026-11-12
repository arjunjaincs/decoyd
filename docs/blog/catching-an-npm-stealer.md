# Catching an npm Postinstall Script Reading Decoy Credentials

This post traces a live end-to-end run of Decoyd Tier 1 (`fanotify` + `inotify`) catching
`examples/demo-evil-pkg` during `npm install` on `ubuntu-latest` in GitHub Actions, shows the
exact JSON records and process chains captured in CI artifacts, and reports the measured
latency, memory, CPU, and false-positive numbers from the same runner environment.

---

## 1. The Threat Flow (`examples/demo-evil-pkg`)

When `npm install` installs a package without `--ignore-scripts`, `npm` executes the
package's `postinstall` script (`examples/demo-evil-pkg/package.json` defines
`"postinstall": "node postinstall.js"`). In our benign fixture
(`examples/demo-evil-pkg/postinstall.js`), the script scans eight credential paths under
`$HOME` (`~/.npmrc`, `~/.pypirc`, `~/.docker/config.json`, `~/.git-credentials`, `~/.netrc`,
`~/.ssh/id_rsa`, `~/.vault-token`, `~/.cursor/mcp.json`) using `fs.readFileSync`, prints
byte counts, and discards the contents.

In the CI `npm-e2e` job (run [`43162675883`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675883),
job `Real npm end-to-end (Tier 1 fanotify)`):

1. We seed `~/.pypirc` and `~/.vault-token` (`0600`) and register them in `deployed_tokens.json`
   (we omit `~/.npmrc` in this job because `npm` itself reads `~/.npmrc` on startup even
   under `--ignore-scripts`).
2. `sudo decoyd watch --tier1 --data-dir "$DATA_DIR"` starts in the background and logs
   `decoyd watch started — monitoring 2 tokens`.
3. **Negative control**: `npm install --no-save --ignore-scripts "$GITHUB_WORKSPACE/examples/demo-evil-pkg"`
   followed by a `2.6 s` wait leaves `$DATA_DIR/triggers.jsonl` empty (`0` events).
4. **Positive test**: `npm install --no-save "$GITHUB_WORKSPACE/examples/demo-evil-pkg"` executes
   `node postinstall.js` and triggers detection in Tier 1.

---

## 2. Actual Event JSON Captured in CI

### Case A — `postinstall.js` Reading `~/.pypirc` (CI Run [`43162675883`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675883))

From the `npm-e2e-events` artifact (`npm-e2e-triggers.jsonl`) produced by `npm-e2e`:

```json
{
  "id": "deb7cdbd28c0b4fe",
  "token_id": "npm-e2e-pypirc",
  "token_type": "pypirc",
  "path": "/home/runner/.pypirc",
  "triggered_at": "2026-11-12T05:38:38.390932373Z",
  "event_type": "access",
  "status": "failed",
  "alert_error": "no alert channel configured",
  "watcher_tier": "tier1",
  "attribution": {
    "accessor": {
      "pid": 4465,
      "ppid": 4464,
      "comm": "node",
      "exe": "",
      "cmdline": "node postinstall.js",
      "cwd": "/home/runner/work/decoyd/decoyd/examples/demo-evil-pkg",
      "uid": 1001,
      "start_ticks": 5218
    },
    "confidence": 1,
    "npm_package": "demo-evil-pkg",
    "npm_version": "0.0.1",
    "npm_lifecycle_event": "postinstall"
  }
}
```

Here Decoyd captured `comm: "node"`, `cmdline: "node postinstall.js"`,
`cwd: "/home/runner/work/decoyd/decoyd/examples/demo-evil-pkg"`, `npm_package: "demo-evil-pkg"`,
`npm_version: "0.0.1"`, and `npm_lifecycle_event: "postinstall"`. Because `node postinstall.js`
finished reading and exited while `collectProcess` (`internal/watch/procinfo_linux.go`) ran,
the post-collection `/proc/4465/stat` re-check could no longer confirm `starttime`; by design,
Decoyd cleared `exe`/`exe_sha256` and recorded `"confidence": 1` (`low`) rather than risk
attributing a reused PID.

### Case B — Full Ancestor Chain & Exit Code `3` in `.github/workflows/demo.yml` (CI Run [`43162675881`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675881))

When the reading process stays alive long enough for pre/post `starttime` verification and the
`/proc/<pid>/stat` parent walk, Decoyd records the full process chain (`demo-events` artifact,
`demo-events.json`):

```json
{
  "id": "cc0d0fd6ffa0eddd",
  "token_id": "demo-npmrc-tok1",
  "token_type": "npmrc",
  "path": "/home/runner/.npmrc",
  "triggered_at": "2026-11-12T05:38:35.190894402Z",
  "event_type": "access",
  "status": "failed",
  "alert_error": "no alert channel configured",
  "watcher_tier": "tier1",
  "attribution": {
    "accessor": {
      "pid": 4418,
      "ppid": 4416,
      "comm": "npm",
      "exe": "/opt/hostedtoolcache/node/20.20.2/x64/bin/node",
      "cmdline": "npm",
      "cwd": "/home/runner/work/_temp/demo-pkg-install",
      "uid": 1001,
      "start_ticks": 23700,
      "exe_sha256": "6295488653f0d93b0a157841746fef7e72cc4328cfb60c4bbe0ca2668a836ffd"
    },
    "ancestors": [
      {
        "pid": 4416,
        "ppid": 2060,
        "comm": "bash",
        "exe": "/usr/bin/bash",
        "cmdline": "/usr/bin/bash -e /home/runner/work/_temp/2654bede-9277-4860-88c1-6809e887c90a.sh",
        "cwd": "/home/runner/work/_temp/demo-pkg-install",
        "uid": 1001,
        "start_ticks": 23700,
        "exe_sha256": "450c68376d14e5040d36cce58605c35b2211d244f3079297440dc9d602ee4994"
      },
      {
        "pid": 2060,
        "ppid": 2040,
        "comm": "Runner.Worker",
        "exe": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Worker",
        "cmdline": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Worker spawnclient 142 145",
        "cwd": "/home/runner/actions-runner/cached/2.338.2/bin",
        "uid": 1001,
        "start_ticks": 20436,
        "exe_sha256": "9cb30f90c9473025facb2856310483c338b88d5873649e76bfbe3a0f0182103d"
      },
      {
        "pid": 2040,
        "ppid": 1965,
        "comm": "Runner.Listener",
        "exe": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Listener",
        "cmdline": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Listener run",
        "cwd": "/",
        "uid": 1001,
        "start_ticks": 20275,
        "exe_sha256": "433782c36a44447fc19e541f786981ce222f78755345c59e02b50689ec2ad2ac"
      },
      {
        "pid": 1965,
        "ppid": 1,
        "comm": "hosted-compute-",
        "exe": "/opt/hca/hosted-compute-agent",
        "cmdline": "/opt/hca/hosted-compute-agent",
        "cwd": "/",
        "uid": 1001,
        "start_ticks": 1418,
        "exe_sha256": "e6294d28ce5d563b1e7dd03f24175c8532bf1bc246df3e5ecf43e3a7aa126255"
      }
    ],
    "confidence": 3
  }
}
```

The process chain in `demo-events.json` walks cleanly up to PPID 1:
`npm (4418)` <- `bash (4416)` <- `Runner.Worker (2060)` <- `Runner.Listener (2040)` <- `hosted-compute-agent (1965)`,
with `"confidence": 3` (`high`) and a SHA-256 binary digest for every process in the chain, and
`decoyd events --fail-if-any` exited with status `3`.

---

## 3. Measured Numbers (CI Run [`43161750870`](https://github.com/arjunjaincs/decoyd/actions/runs/43161750870))

All measurements below were recorded in the `Benchmarks` CI job (`ubuntu26`, kernel
`7.0.0-1019-azure`, AMD EPYC 7763 4 vCPUs, `go1.27.4`), artifact `tier1-benchmarks`
(full methodology in [BENCHMARKS.md](../BENCHMARKS.md)):

- **Time-to-alert over 100 runs** (child open/read -> `triggers.jsonl`):
  - **Tier 1 (`fanotify`)**: `min = 6.493 ms`, `p50 = 7.677 ms`, `mean = 7.804 ms`,
    `p95 = 8.526 ms` (rank 95) / `8.577 ms` (rank 96), `max = 35.084 ms`.
  - **Tier 2 (`inotify`, `2 s` debounce)**: `min = 2000.952 ms`, `p50 = 2003.355 ms`,
    `mean = 2003.187 ms`, `p95 = 2004.653 ms` (rank 95) / `2004.684 ms` (rank 96),
    `max = 2007.110 ms`.
- **Tier 1 idle RSS and CPU over 30 s** (`USER_HZ = 100`, `wall_s = 30.006 s`):
  - **1 decoy**: `11.35 MiB` avg RSS (`11,622.7 KiB`), `0.0000%` CPU (`0` ticks).
  - **100 decoys**: `11.54 MiB` avg RSS (`11,816.3 KiB`), `0.0333%` CPU (`1` tick).
  - **1,000 decoys**: `13.76 MiB` avg RSS (`14,092.9 KiB`), `0.0000%` CPU (`0` ticks).
- **Tier 1 false-positive suite** (`2.6 s` settle per operation):
  - `0` events for `ls -la`, `stat` on decoys, `find -maxdepth 3`, `git status` inside a
    repo containing `.npmrc`/`.env`/`.aws/credentials`, `grep -rn` on a sibling directory,
    and a 50-iteration `lstat`/`stat` loop.
  - `2` JSONL lines (`pending` + `sent`, 1 webhook delivery) for the `cat .env` positive control.

The full `systemd` lifecycle (`install --system`, `systemd-analyze verify`, child read attribution,
unprivileged `decoyd events --json`, `sync`, and `uninstall --system`) also passes on `ubuntu-latest`
in the `System lifecycle proof` CI job (`43162675883`).

---

## 4. Limitations

- **Read methods and timing**: Tier 1 observes VFS `FAN_OPEN|FAN_ACCESS` and Tier 2 observes
  `IN_OPEN|IN_ACCESS`/`IN_MODIFY`/`IN_MOVE_SELF`/`IN_DELETE_SELF` (`internal/watch/watch_linux.go`).
  Raw block-device reads, `/proc/<pid>/mem` reads, offline disk copies, or remote network
  filesystems (NFS, SMB, 9p/drvfs) can bypass local file notifications. Because `/proc`
  attribution runs asynchronously in `FAN_CLASS_NOTIF` (`internal/watch/procinfo_linux.go`),
  sub-millisecond processes that exit during collection drop to `confidence: 1` without `exe`
  or ancestor chains (intentional anti-PID-reuse behavior, as seen in Case A above).
- **Root attacker**: Tier 1 runs `decoyd watch --tier1` as root / `CAP_SYS_ADMIN` (with
  systemd capability bounding and `ProtectSystem=strict` in `cmd/decoyd/cmd_install_system_linux.go`).
  An attacker who already has root can stop the daemon, tamper with `/var/lib/decoyd/triggers.jsonl`,
  or bypass VFS watches via raw block devices. Decoyd targets unprivileged or user-level
  compromise such as malicious package scripts running under a developer account.
- **Windows read gap**: On Windows, `internal/watch/watch_windows.go` uses `fsnotify`
  (`ReadDirectoryChangesW`), which surfaces writes, renames, and deletes only—pure read-only
  file opens (such as `fs.readFileSync` on Windows) are not detected in v1.
- **npm only**: Package attribution in `internal/watch/procinfo_linux.go` (`ReadEnvFiltered`)
  reads only `npm_package_name`, `npm_package_version`, and `npm_lifecycle_event` from
  `/proc/<pid>/environ`. `pnpm` and `yarn` are untested, and `pip` is not supported.

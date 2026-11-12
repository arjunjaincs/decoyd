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
`npm_version: "0.0.1"`, and `npm_lifecycle_event: "postinstall"`. Because `node postinstall.js` finished reading and exited while `collectProcess`
(`internal/watch/procinfo_linux.go`) ran in that pre-J2 run, the post-collection
`/proc/4465/stat` re-check saw `node` had already exited and dropped `confidence` to `1` (`low`).
In `v1.0`, if a process vanishes before the post-read `starttime` check, Decoyd preserves fields
read while alive at `"confidence": 1` (`low`), and discards all `/proc`-derived fields only on a
`starttime` mismatch (PID reuse).

### Case B — Full Ancestor Chain & Exit Code `3` in `.github/workflows/demo.yml` (CI Run [`43172564530`](https://github.com/arjunjaincs/decoyd/actions/runs/43172564530))

In `.github/workflows/demo.yml` (run [`43172564530`](https://github.com/arjunjaincs/decoyd/actions/runs/43172564530)),
only `~/.pypirc` and `~/.vault-token` are seeded (`~/.npmrc` is omitted because `npm` reads
`~/.npmrc` on startup even under `--ignore-scripts`), `npm install --no-save --ignore-scripts`
runs first as a negative control (`0` events), and `examples/demo-evil-pkg/postinstall.js` holds
its Node process open `300 ms` after scanning (fixture behavior) so asynchronous `/proc`
attribution completes before exit. Decoyd captured the full ancestor chain from `node postinstall.js`
(`demo-events` artifact, `demo-events.json`):

```json
{
  "id": "2a591750d2afeaaf",
  "token_id": "demo-pypirc-tok2",
  "token_type": "pypirc",
  "path": "/home/runner/.pypirc",
  "triggered_at": "2026-11-12T07:05:05.399509156Z",
  "event_type": "access",
  "status": "failed",
  "alert_error": "no alert channel configured",
  "watcher_tier": "tier1",
  "attribution": {
    "accessor": {
      "pid": 4484,
      "ppid": 4483,
      "comm": "node",
      "exe": "/opt/hostedtoolcache/node/20.20.2/x64/bin/node",
      "cmdline": "node postinstall.js",
      "cwd": "/home/runner/work/decoyd/decoyd/examples/demo-evil-pkg",
      "uid": 1001,
      "start_ticks": 19130,
      "exe_sha256": "6295488653f0d93b0a157841746fef7e72cc4328cfb60c4bbe0ca2668a836ffd"
    },
    "ancestors": [
      {
        "pid": 4483,
        "ppid": 4472,
        "comm": "sh",
        "exe": "/usr/bin/dash",
        "cmdline": "sh -c node postinstall.js",
        "cwd": "/home/runner/work/decoyd/decoyd/examples/demo-evil-pkg",
        "uid": 1001,
        "start_ticks": 19130,
        "exe_sha256": "2f86ee371c7250d927f3e32ccb449986ee9e7c4d5b5c96d4e619d6c6f29ef92e"
      },
      {
        "pid": 4472,
        "ppid": 4470,
        "comm": "npm install /ho",
        "exe": "/opt/hostedtoolcache/node/20.20.2/x64/bin/node",
        "cmdline": "npm install /home/runner/work/decoyd/decoyd/examples/demo-evil-pkg",
        "cwd": "/home/runner/work/_temp/demo-pkg-install",
        "uid": 1001,
        "start_ticks": 19105,
        "exe_sha256": "6295488653f0d93b0a157841746fef7e72cc4328cfb60c4bbe0ca2668a836ffd"
      },
      {
        "pid": 4470,
        "ppid": 2081,
        "comm": "bash",
        "exe": "/usr/bin/bash",
        "cmdline": "/usr/bin/bash -e /home/runner/work/_temp/1de48c05-6b14-46db-a3c2-cebd6e6139a2.sh",
        "cwd": "/home/runner/work/_temp/demo-pkg-install",
        "uid": 1001,
        "start_ticks": 19105,
        "exe_sha256": "450c68376d14e5040d36cce58605c35b2211d244f3079297440dc9d602ee4994"
      },
      {
        "pid": 2081,
        "ppid": 2062,
        "comm": "Runner.Worker",
        "exe": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Worker",
        "cmdline": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Worker spawnclient 142 145",
        "cwd": "/home/runner/actions-runner/cached/2.338.2/bin",
        "uid": 1001,
        "start_ticks": 15374,
        "exe_sha256": "9cb30f90c9473025facb2856310483c338b88d5873649e76bfbe3a0f0182103d"
      },
      {
        "pid": 2062,
        "ppid": 1957,
        "comm": "Runner.Listener",
        "exe": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Listener",
        "cmdline": "/home/runner/actions-runner/cached/2.338.2/bin/Runner.Listener run",
        "cwd": "/",
        "uid": 1001,
        "start_ticks": 15193,
        "exe_sha256": "433782c36a44447fc19e541f786981ce222f78755345c59e02b50689ec2ad2ac"
      },
      {
        "pid": 1957,
        "ppid": 1,
        "comm": "hosted-compute-",
        "exe": "/opt/hca/hosted-compute-agent",
        "cmdline": "/opt/hca/hosted-compute-agent",
        "cwd": "/",
        "uid": 1001,
        "start_ticks": 1390,
        "exe_sha256": "e6294d28ce5d563b1e7dd03f24175c8532bf1bc246df3e5ecf43e3a7aa126255"
      }
    ],
    "confidence": 3,
    "npm_package": "demo-evil-pkg",
    "npm_version": "0.0.1",
    "npm_lifecycle_event": "postinstall"
  }
}
```

The process chain in `demo-events.json` walks cleanly up to PPID 1:
`node postinstall.js (4484)` <- `sh -c node postinstall.js (4483)` <- `npm install ... (4472)` <- `bash (4470)` <- `Runner.Worker (2081)` <- `Runner.Listener (2062)` <- `hosted-compute-agent (1957)`,
with `"confidence": 3` (`high`), `npm_package: "demo-evil-pkg"`, `npm_lifecycle_event: "postinstall"`,
and a SHA-256 binary digest for every process in the chain, and `decoyd events --fail-if-any`
exited with status `3`.

---

## 3. Measured Numbers (CI Run [`43172569783`](https://github.com/arjunjaincs/decoyd/actions/runs/43172569783))

All measurements below were recorded in the `Benchmarks` CI job (`ubuntu26` / Ubuntu 26.04.1 LTS,
kernel `7.0.0-1019-azure`, `go1.27.4`), artifact `tier1-benchmarks` (full methodology in
[BENCHMARKS.md](../BENCHMARKS.md)):

- **Detection latency over 100 runs** (child open/read -> recorded in `triggers.jsonl`):
  - **Tier 1 (`fanotify`)**: `min = 4.453 ms`, `p50 = 4.778 ms`, `mean = 5.212 ms`,
    `p95 = 5.930 ms` (rank 95) / `5.983 ms` (rank 96), `max = 26.691 ms`.
  - **Tier 2 (`inotify`, `2 s` debounce)**: `min = 2001.343 ms`, `p50 = 2002.732 ms`,
    `mean = 2002.701 ms`, `p95 = 2003.693 ms` (rank 95) / `2003.753 ms` (rank 96),
    `max = 2003.987 ms`.
- **Tier 1 idle RSS and CPU over 30 s** (`USER_HZ = 100`, `wall_s = 30.005–30.006 s`):
  - **1 decoy**: `11.44 MiB` avg RSS (`11,716.1 KiB`), `0.0000%` CPU (`0` ticks).
  - **100 decoys**: `11.70 MiB` avg RSS (`11,979.1 KiB`), `0.0000%` CPU (`0` ticks).
  - **1,000 decoys**: `13.76 MiB` avg RSS (`14,091.7 KiB`), `0.0333%` CPU (`1` tick).
- **Tier 1 false-positive suite** (`2.6 s` settle per operation):
  - `0` events for `ls -la`, `stat` on decoys, `find -maxdepth 3`, `git status` inside a
    repo containing `.npmrc`/`.env`/`.aws/credentials`, `grep -rn` on a sibling directory,
    a 50-iteration `lstat`/`stat` loop, and `pip list`.
  - **Known false positives (legitimate tools reading their own config at default paths)**:
    `npm config ls`, `npm ls`, `git credential fill`, and `docker --version` each triggered `2`
    JSONL lines (`pending` + `sent`) when their corresponding `~/.npmrc`, `~/.git-credentials`,
    or `~/.docker/config.json` decoy existed.
  - `2` JSONL lines (`pending` + `sent`, 1 webhook delivery) for the `cat .env` positive control.

The full `systemd` lifecycle (`install --system`, `systemd-analyze verify`, child read attribution,
unprivileged `decoyd events --json`, `sync`, and `uninstall --system`) also passes on `ubuntu-latest`
in the `System lifecycle proof` CI job (`43172569783`).

---

## 4. Limitations

- **Read methods and timing**: Tier 1 observes VFS `FAN_OPEN|FAN_ACCESS` and Tier 2 observes
  `IN_OPEN|IN_ACCESS`/`IN_MODIFY`/`IN_MOVE_SELF`/`IN_DELETE_SELF` (`internal/watch/watch_linux.go`).
  Raw block-device reads, `/proc/<pid>/mem` reads, offline disk copies, or remote network
  filesystems (NFS, SMB, 9p/drvfs) can bypass local file notifications. Because `/proc`
  attribution runs asynchronously in `FAN_CLASS_NOTIF` (`internal/watch/procinfo_linux.go`),
  sub-millisecond processes that exit before `/proc` collection finishes drop to `confidence: 1`
  without ancestor chains (while processes whose PID was reused before the `starttime` re-check
  have all `/proc`-derived fields discarded).
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

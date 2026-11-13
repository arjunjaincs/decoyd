# Decoyd

**Plant fake credentials. Find out which process, and which npm package, touched them.**

[![CI](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml/badge.svg)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml)
[![Privileged + Windows CI](https://github.com/arjunjaincs/decoyd/actions/workflows/ci-privileged.yml/badge.svg)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci-privileged.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<!-- After adding docs/demo.gif, uncomment the next line -->
<!-- ![Decoyd demo](docs/demo.gif) -->

Decoyd is a self-hosted canary token tool written in Go. It generates realistic fake
credential files (AWS keys, SSH keys, `.env`, `.npmrc`, kubeconfig and more), places them
where an attacker or a malicious package would look, and alerts you the moment one is
touched. One static binary. No cloud account. No server to run.

On Linux with root, Decoyd goes further than "a file was opened": it tells you **which
process** read the decoy (PID, executable hash, parent chain) and, for npm install scripts,
**which package** was running.

```text
accessor   : node postinstall.js  (pid 4484)
package    : demo-evil-pkg@0.0.1  (npm lifecycle: postinstall)
confidence : high  (full parent chain and executable SHA-256 recorded)
```

*(Real event from the CI demo run; see [`docs/blog/catching-an-npm-stealer.md`](docs/blog/catching-an-npm-stealer.md).)*

---

## Contents

- [Quickstart](#quickstart)
- [How detection works](#how-detection-works)
- [Using Decoyd](#using-decoyd)
- [Token types](#token-types)
- [Alert channels](#alert-channels)
- [Measured performance](#measured-performance)
- [Known limitations](#known-limitations)
- [Security design](#security-design)
- [Verifying releases](#verifying-releases)
- [Building from source](#building-from-source)
- [Roadmap](#roadmap)
- [Development notes](#development-notes)

---

## Quickstart

Download the archive for your platform from the
[Releases page](https://github.com/arjunjaincs/decoyd/releases), then:

```bash
# Linux: verify, extract, install
sha256sum -c checksums.txt --ignore-missing
tar xzf decoyd_<version>_linux_amd64.tar.gz
sudo install -m 0755 decoyd /usr/local/bin/decoyd

decoyd doctor     # what detection tier can this machine run?
decoyd test       # isolated self-test: plants a decoy, reads it, checks the alert arrives
decoyd            # interactive TUI: generate, deploy, configure alerts
```

On Windows, unzip the archive and run `.\decoyd.exe` from PowerShell.

The main menu lets you generate decoys, deploy them to a path you choose, and add an alert channel
under **Alert settings** (Discord, Slack, Telegram, Teams, ntfy.sh or a webhook).

---

## How detection works

| | Tier 1 | Tier 2 |
|---|---|---|
| Platform | Linux, root or `CAP_SYS_ADMIN` | Linux or Windows, no privileges |
| Mechanism | `fanotify` (notification only) plus `inotify` | `inotify` (Linux) / `ReadDirectoryChangesW` (Windows) |
| Detects reads | Yes | Linux: yes. Windows: no (write, rename, delete only) |
| Process attribution | PID, parent chain, executable SHA-256, command line, cwd, uid | No |
| npm package attribution | Yes (see below) | No |
| Start with | `sudo decoyd watch --tier1` | `decoyd watch` or the TUI |

Tier 1 uses `fanotify` in notification mode only. Decoyd never uses `FAN_*_PERM` events, because a
stalled permission listener can freeze file access system-wide. A source-level test fails the
build if a `_PERM` constant ever appears.

**Package attribution** reads three variables from `/proc/<pid>/environ` of the accessing
process: `npm_package_name`, `npm_package_version` and `npm_lifecycle_event`. Nothing else from the
environment is read or stored. It works for npm only; pnpm and yarn are untested and pip is not
supported.

If an attribute cannot be read because the process exited quickly, Decoyd keeps what it captured
while the process was alive and lowers the event's confidence. If the PID was reused during
collection, all `/proc` data is discarded.

---

## Using Decoyd

### Interactive (TUI)

```bash
decoyd
```

| Key | Action |
|---|---|
| `Up` / `Down` or `k` / `j` | Navigate |
| `Space` | Toggle selection |
| `Enter` | Confirm |
| `Esc` | Back |
| `?` | Help |
| `q` | Quit |

Generate decoys, deploy them to a chosen path (existing files are never overwritten), configure
alerts, and view recent trigger events. The TUI starts a watcher automatically while it is open.

### Headless

```bash
decoyd watch                # Tier 2, runs in the foreground
sudo decoyd watch --tier1 --data-dir /home/you/.decoyd   # Tier 1 with process attribution
decoyd events               # recent trigger events
decoyd events --json --since 1h
decoyd events --fail-if-any # exit code 3 if anything triggered (for CI)
decoyd list                 # deployed decoys
decoyd remove <id>          # remove the record (file stays)
decoyd remove --purge <id>  # remove the record and the file
```

Under `sudo`, `HOME` changes, so always pass `--data-dir` explicitly for Tier 1.

### Run as a service (Linux)

```bash
decoyd install                               # per-user systemd unit, Tier 2
sudo decoyd install --system --data-dir /var/lib/decoyd   # system unit, Tier 1
sudo decoyd sync                             # copy alert config into /etc/decoyd
sudo decoyd uninstall --system               # remove unit, binary, /var/lib/decoyd, /etc/decoyd
```

The system install puts the binary at the root-owned `/usr/local/bin/decoyd`, runs with a bounded
capability set (`CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_DAC_READ_SEARCH`) and `NoNewPrivileges`,
keeps its non-secret trigger log in `/var/lib/decoyd`, and **never writes into user-writable
directories**. Alert credentials are copied one way into `/etc/decoyd` (mode `0600`) by `sync`.

### In CI

```yaml
- run: decoyd events --fail-if-any   # fails the job if any decoy was touched
```

See [`.github/workflows/demo.yml`](.github/workflows/demo.yml) for a complete example that
seeds decoys on a runner, installs a benign fake "malicious" package, and fails when it reads them.

---

## Token types

18 types. Every file contains realistic values and no static identifiers (nothing says "decoy",
"canary" or "fake"), so a stealer cannot grep for them.

| File | Type | Content |
|---|---|---|
| `credentials` | `aws_credentials` | AWS-style access key (base32 alphabet) and secret |
| `id_ed25519` | `ssh_key` | Valid Ed25519 keypair |
| `id_rsa` | `rsa_key` | RSA-2048 key, PKCS#1 PEM |
| `.env` | `env_secrets` | `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET` |
| `.github_token` | `github_pat` | `ghp_` token with a valid checksum |
| `.slack_token` | `slack_token` | `xoxb-` bot token |
| `config` | `kubeconfig` | Valid kubeconfig with a bearer token |
| `backup.sql` | `db_dump` | Schema, header and rows |
| `.npmrc` | `npmrc` | npm token |
| `.pypirc` | `pypirc` | PyPI token |
| `config.json` | `docker_config` | Docker registry auth |
| `.git-credentials` | `git_credentials` | Credential-store URL with a PAT |
| `.netrc` | `netrc` | Host, login, password |
| `service-account.json` | `gcp_service_account` | Service account JSON with a PKCS#8 key |
| `credentials.tfrc.json` | `terraform_creds` | Terraform Cloud token |
| `.vault-token` | `vault_token` | HashiCorp Vault token |
| `mcp.json` | `mcp_config` | MCP server config with a token |
| `token.txt` | `dns_canary` | Unique subdomain label. **Experimental:** no listener, needs external DNS logs |

The `npm_` and `pypi-` token formats are plausible, not verified against official specifications.
Tracing a leaked credential back is a lookup of its value in the local database.

---

## Alert channels

Discord, Slack, Telegram, Microsoft Teams, ntfy.sh, and a generic webhook. You can configure
several and assign individual decoys to specific channels.

All external strings (paths, process names, command lines) are sanitized before they reach an
alert. Discord messages disable mentions, Slack uses plain-text fields. Delivery uses timeouts,
caps the response body and refuses redirects. Rate limiting sends a single "further events
suppressed" notice per window instead of flooding the channel.

---

## Measured performance

Measured on GitHub Actions `ubuntu-latest` runners and recorded as CI artifacts. Full method,
environments and raw numbers: [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md).

- **Tier 1 time to record an event** (child process read to a line in `triggers.jsonl`): median
  **7.7 ms**, p95 8.5 ms, max 35 ms over 100 runs. This is time to *record*, not time to deliver the
  alert to a webhook.
- **Tier 2 time to record:** about 2,003 ms. That is the default 2-second debounce, a setting,
  not a limit of `inotify`.
- **Idle footprint:** low tens of MiB of memory and near-zero CPU from 1 to 1,000 decoys.
- **Benign operations that did not trigger:** `ls`, `stat`, `find`, `git status`, `grep -r` on
  another directory, and an editor-style `stat` loop.

---

## Known limitations

- **Legitimate tools trigger decoys at their default paths.** `npm`, `git credential` and
  `docker` read their own config files, and in CI measurements each caused an event; `pip list` did
  not. Deploy decoys at paths your own tooling does not read, or wait for the allowlist
  (see [Roadmap](#roadmap)). Details: [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md).
- **Windows cannot detect read-only access.** Only writes, renames and deletes are reported.
- **Package attribution is npm only.**
- **Very short-lived processes** can exit before `/proc` is read, giving lower-confidence
  attribution.
- **Network and virtual filesystems** (NFS, SMB, 9p/drvfs) often generate no events. `decoyd doctor`
  warns about them. On WSL, deploy decoys on the Linux filesystem, not under `/mnt/c`.
- **A root attacker** can stop the daemon or bypass VFS notifications. Decoyd targets unprivileged
  compromise such as a malicious package running under a developer account.
- If `fanotify` cannot mark a particular file (for example on some filesystems), that decoy falls
  back to Tier 2 behaviour and `decoyd doctor` reports it. Events record which tier produced them.

The full analysis is in [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md).

---

## Security design

- Decoys are created with `O_EXCL` and `O_NOFOLLOW` (reparse-point refusal on Windows); existing
  files and symlinks are never followed or overwritten.
- `fanotify` in notification mode only, every event file descriptor closed, parser fuzzed
  (`FuzzParseFanotifyEvents`), queue overflow produces a rate-limited "events lost" alert.
- Events never contain token values. Command lines are capped and sanitized.
- The root daemon `Lstat`-validates every path it reads from user-writable state and refuses
  symlinks and mismatched owners.
- Trigger log and alert config updates are serialized with advisory file locks.
- Details: [`SECURITY.md`](SECURITY.md), [ADR 0001](docs/adr/0001-tier1-privilege.md),
  [`docs/EVENT-SCHEMA.md`](docs/EVENT-SCHEMA.md).

Report vulnerabilities through GitHub's private vulnerability reporting (Security tab of this
repository). Please do not open public issues for security bugs.

---

## Verifying releases

Release archives ship with a checksum file, SPDX SBOMs and a keyless
[cosign](https://github.com/sigstore/cosign) signature created by this repository's release workflow.

```bash
VERSION=v1.0.0
gh release download "$VERSION" --repo arjunjaincs/decoyd --dir decoyd-release && cd decoyd-release
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity "https://github.com/arjunjaincs/decoyd/.github/workflows/release.yml@refs/tags/${VERSION}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt
```

---

## Building from source

Requires Go 1.25 or newer (the repository pins a patched toolchain).

```bash
git clone https://github.com/arjunjaincs/decoyd.git && cd decoyd
go build -o decoyd ./cmd/decoyd
go test -race ./...
```

`cmd/decoyd-emu` is a benign test tool that reads credential paths the way a stealer would
(read-only, no network). It exists only to exercise the detection.

```text
cmd/decoyd/           CLI and TUI entry point, install and lifecycle commands
cmd/decoyd-emu/       benign credential-reading test tool
internal/watch/       fanotify, inotify, Windows watcher, /proc attribution
internal/attribution/ process and package attribution types
internal/tokens/      the 18 generators
internal/alert/       alert channels
internal/triglog/     trigger log (schema v1)
internal/sanitize/    sanitization of external strings
internal/filelock/    advisory locking, Linux and Windows
detection/            Wazuh and Sigma rules (syntax-checked, untested)
docs/                 ADR, event schema, threat model, benchmarks, blog post
```

---

## Roadmap

Planned for v1.1:

- Allowlist and learning mode for legitimate tools that read their own config files
- pnpm, yarn and pip attribution
- Windows read detection (ETW)
- Validate the Wazuh and Sigma rules against real tooling

---

## Development notes

Decoyd was built with AI coding agents under human review: the design, threat model, scope
decisions and review checklist are mine, and the evidence for what works is the automated test
and CI history, not my say-so. Linux and Windows builds, a privileged `fanotify` suite, a live
`systemd` install-to-uninstall lifecycle, a real `npm install` end-to-end test, and the demo
workflow all run in GitHub Actions. See the
[Actions tab](https://github.com/arjunjaincs/decoyd/actions).

## License

MIT. See [LICENSE](LICENSE).

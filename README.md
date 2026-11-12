# Decoyd — Credential Decoy File Monitor

**Deploy decoy credential files. Get alerted when anything accesses them.**

Decoyd is a Go CLI/TUI and daemon that generates realistic decoy credential
files (`.npmrc`, `id_rsa`, `.git-credentials`, AWS credentials, etc.) and fires an
alert with process attribution (Tier 1 on Linux) or token identity (Tier 2)
when a monitored file is accessed.

---

## Quickstart

```bash
# Install
go install github.com/arjunjaincs/decoyd/cmd/decoyd@latest

# Launch the interactive TUI to generate tokens, configure alert sinks, and deploy files
decoyd

# Or run the headless watcher against an existing data dir (~/.decoyd by default)
decoyd watch &              # Tier 2 (inotify/fsnotify, no root required)
sudo decoyd watch --tier1   # Tier 1 (fanotify, Linux, full process attribution; errors if unavailable)

# Check for detected accesses
decoyd events
decoyd events --fail-if-any   # exit 3 if anything was caught (use in CI)
```

---

## Detection Tiers

| Feature | Tier 1 (Linux, root / `CAP_SYS_ADMIN`) | Tier 2 (unprivileged) |
|---------|----------------------------------------|-----------------------|
| Transport | hybrid `fanotify` (`FAN_CLASS_NOTIF`) + `inotify` | Linux `inotify` / Windows `fsnotify` (`ReadDirectoryChangesW`) |
| Process pid + exe + cmdline | ✅ | ❌ |
| npm lifecycle hook attribution | ✅ (`npm_package`, `npm_version`, `npm_lifecycle_event` via `/proc/<pid>/environ`) | ❌ |
| Root required | Yes | No |
| Windows read-only open detection | N/A | ❌ (write/rename/delete only on Windows) |

> **Package-manager attribution scope:** Tier 1 package attribution reads only npm lifecycle environment variables (`npm_package_name`, `npm_package_version`, `npm_lifecycle_event`) from `/proc/<pid>/environ`. `pnpm` and `yarn` are untested, and `pip` is not supported.
>
> **Detection latency (event recorded in `triggers.jsonl`):** In [CI benchmarks](docs/BENCHMARKS.md), time from a child process reading a decoy file to the event recorded in `triggers.jsonl` over 100 runs was `7.677 ms` p50 (`8.526`–`8.577 ms` p95) for Tier 1 `fanotify` and `2003.355 ms` p50 (`2004.653`–`2004.684 ms` p95) for Tier 2 `inotify` (2 s per-path debounce).

---

## Token Types (18)

| Filename | Type Key | Format |
|----------|----------|--------|
| `credentials` | `aws_credentials` | AWS `AKIA...` base32 access key + secret |
| `id_ed25519` | `ssh_key` | Valid Ed25519 SSH keypair (no comment identifier) |
| `.env` | `env_secrets` | `.env` with `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET` |
| `.github_token` | `github_pat` | GitHub PAT (`ghp_...`) |
| `.slack_token` | `slack_token` | Slack bot token (`xoxb-...`) |
| `config` | `kubeconfig` | Kubeconfig YAML with random cluster/user + bearer token |
| `backup.sql` | `db_dump` | SQL dump header, schema, fake rows |
| `token.txt` | `dns_canary` | Unique subdomain label (experimental: no listener, needs external DNS logs) |
| `.npmrc` | `npmrc` | npm access token (`npm_...`; plausible, not verified against official specs) |
| `.pypirc` | `pypirc` | PyPI API token (`pypi-...`; plausible, not verified against official specs) |
| `config.json` | `docker_config` | Docker registry base64 auth JSON |
| `.git-credentials` | `git_credentials` | Git credential store URL with `ghp_...` PAT |
| `.netrc` | `netrc` | `.netrc` host/login/password |
| `id_rsa` | `rsa_key` | RSA-2048 private key in PKCS#1 PEM |
| `service-account.json` | `gcp_service_account` | GCP service account JSON with PKCS#8 private key |
| `credentials.tfrc.json` | `terraform_creds` | Terraform Cloud API token JSON |
| `.vault-token` | `vault_token` | HashiCorp Vault token (`s.<24 chars>`) |
| `mcp.json` | `mcp_config` | MCP server config JSON with `ghp_...` PAT |

**Rule**: Generated tokens contain **no static identifiers** — tracing a leaked
credential means looking up its value in the database, never embedding visible labels.

---

## Detection Content

- `detection/decoyd-wazuh.xml` — Wazuh decoder + rules (**syntax-checked, untested** against a real Wazuh 4.x installation)
- `detection/decoyd.yml` — Sigma rules (**syntax-checked, untested** against live `sigma-cli` / SIEM)
- `docs/ATTACK-MAPPING.md` — ATT&CK T1552.001 and related mapping

---

## System Install (Linux, persistent)

```bash
# Install systemd unit decoyd.service (writes only /usr/local/bin/decoyd, unit, /var/lib/decoyd, /etc/decoyd)
sudo decoyd install --system --data-dir /var/lib/decoyd

# Sync alert config from user dir to /etc/decoyd/alert_config.json (and deployed_tokens.json to /var/lib/decoyd/)
sudo decoyd sync

# Uninstall service, binary, /var/lib/decoyd, and /etc/decoyd
sudo decoyd uninstall --system
```

---

## CI Integration

```yaml
- run: decoyd events --fail-if-any
  # Exits 3 if any TriggerEvent was recorded; fails the CI job
```

See `examples/github-action/decoyd-demo.yml` for a full demo workflow.

---

## Architecture

```
cmd/decoyd/           Main binary (TUI, list, remove, watch, install, uninstall, sync, events, triggers, test, doctor)
cmd/decoyd-emu/       Benign test emulator (read-only credential scanner)
internal/watch/       Watchers (fanotify_linux.go, hybrid_linux.go, watch_linux.go, watch_windows.go, procinfo_linux.go)
internal/attribution/ Process + attribution types
internal/tokens/      Token generators (18 types, no static identifiers)
internal/alert/       Alerters (Discord, Slack, Telegram, Teams, ntfy, webhook)
internal/triglog/     triggers.jsonl (schema v1)
detection/            Wazuh + Sigma detection content (syntax-checked, untested)
docs/                 ADR, EVENT-SCHEMA, ATTACK-MAPPING, BENCHMARKS, THREAT-MODEL, blog
```

---

## Development notes

Decoyd was built with AI coding agents under human review. The verifiable evidence for
build, security scanning, cross-platform tests, `systemd` lifecycle behavior, npm Tier 1
attribution, and benchmarks is the automated GitHub Actions CI history:

- [`CI` (Linux & Windows matrix, govulncheck, gosec, staticcheck, vet)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml) — run [`43162675839`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675839)
- [`CI — Privileged and Windows` (`fanotify`, `npm-e2e`, `system-lifecycle`, `benchmarks`)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci-privileged.yml) — runs [`43161750870`](https://github.com/arjunjaincs/decoyd/actions/runs/43161750870), [`43162675883`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675883)
- [`Decoyd Demo (Tier 1 npm Credential Access)`](https://github.com/arjunjaincs/decoyd/actions/workflows/demo.yml) — run [`43162675881`](https://github.com/arjunjaincs/decoyd/actions/runs/43162675881)

---

## License

MIT — see [LICENSE](LICENSE).

# Decoyd — Credential Decoy File Monitor

**Deploy fake credential files. Get alerted the moment anything touches them.**

Decoyd is a lightweight Go daemon that plants realistic-looking decoy credential
files (`.npmrc`, `id_rsa`, `.git-credentials`, etc.) and fires an alert with
full process attribution the instant any program opens one.

---

## Quickstart (< 30 seconds)

```bash
# Install
go install github.com/arjunjaincs/decoyd/cmd/decoyd@latest

# Deploy decoy credential files to their default locations
decoyd deploy --all

# Start watching (Tier 2 on all platforms; Tier 1 on Linux with sudo)
decoyd watch &              # Tier 2 (inotify/fsnotify, no root required)
sudo decoyd watch --tier1   # Tier 1 (fanotify, Linux, full attribution)

# Check for detected accesses
decoyd events
decoyd events --fail-if-any   # exit 3 if anything was caught (use in CI)
```

---

## Detection Tiers

| Feature | Tier 1 (Linux, root) | Tier 2 (all platforms) |
|---------|----------------------|------------------------|
| Transport | fanotify | inotify / fsnotify |
| Process pid + exe | ✅ | ❌ |
| Full command line | ✅ | ❌ |
| npm/pip package attribution | ✅ | ❌ |
| Root required | Yes | No |
| macOS / Windows | ❌ | ✅ |

---

## Token Types

| File | Type | Format |
|------|------|--------|
| `~/.npmrc` | npm access token | `npm_<35 alphanumeric>` |
| `~/.pypirc` | PyPI API token | `pypi-<60 base64url>` |
| `~/.docker/config.json` | Docker registry | base64 auth string |
| `~/.git-credentials` | Git store | GitHub PAT (`ghp_...`) |
| `~/.netrc` | FTP/HTTP auth | 32-char alphanumeric |
| `~/.ssh/id_rsa` | RSA-2048 private key | PKCS#1 PEM |
| GCP service account JSON | GCP credentials | PKCS#8 key, realistic fields |
| `credentials.tfrc.json` | Terraform Cloud | 51-char alphanumeric (plausible) |
| `~/.vault-token` | Vault service token | `s.<24 alphanumeric>` |
| `~/.cursor/mcp.json` | MCP server config | GitHub PAT (`ghp_...`) |
| `~/.aws/credentials` | AWS credentials | AKIA-prefixed key |
| `~/.kube/config` | Kubernetes config | base64 cert + bearer token |
| SSH key (comment-free) | ED25519 / RSA | No static identifiers |

**Rule**: Generated tokens contain **no static identifiers** — tracing a leaked
credential means looking up its VALUE, not its label.

---

## Detection Content

- `detection/decoyd-wazuh.xml` — Wazuh decoder + rules (UNVALIDATED)
- `detection/decoyd.yml` — Sigma rules (UNVALIDATED against real toolchain)
- `docs/ATTACK-MAPPING.md` — ATT&CK T1552.001 mapping

---

## System Install (Linux, persistent)

```bash
# Install as a systemd service (runs as root, Tier 1)
sudo decoyd install --system --data-dir /var/lib/decoyd

# Sync alert config from user to /etc/decoyd
sudo decoyd sync

# Uninstall
sudo decoyd uninstall --system
```

---

## CI Integration

```yaml
- run: decoyd events --fail-if-any
  # Exits 3 if any credential decoy was accessed; fails the CI job
```

See `examples/github-action/decoyd-demo.yml` for a full demo workflow.

---

## Architecture

```
cmd/decoyd/           Main binary (watch, deploy, events, install, doctor)
cmd/decoyd-emu/       Benign test emulator (read-only credential scanner)
internal/watch/       Watcher (fanotify_linux.go, watch_linux.go, watch_windows.go)
internal/attribution/ Process + package attribution types
internal/tokens/      Token generators (12 types, no static identifiers)
internal/alert/       Alerters (Discord, Slack, Telegram, Teams, ntfy, webhook)
internal/triglog/     triggers.jsonl (schema v1)
detection/            Wazuh + Sigma detection content
docs/                 ADR, EVENT-SCHEMA, ATTACK-MAPPING, BENCHMARKS
```

---

## License

Apache 2.0. See LICENSE.

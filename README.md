# Decoyd

> Self-hosted canary token generator and monitor with an interactive TUI.

**Stack:** Go · bubbletea + lipgloss (TUI) · bbolt (embedded storage) · single static binary, no runtime dependencies, no cgo.

---

## What it does

Decoyd generates realistic-looking decoy credentials and monitors them for access. Drop a fake AWS key, SSH private key, or `.env` file somewhere an attacker would find it — when they use it, you get an instant alert.

**Token types (Phase 1)**

| Type | What it produces |
|---|---|
| AWS credentials | `AKIA...` access key + secret in real credentials file format |
| SSH private key | Valid ed25519 keypair (OpenSSH PEM), never registered anywhere |
| `.env` secrets | `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET` with realistic fake values |
| GitHub PAT | `ghp_` + 36 alphanumeric chars — matches GitHub's real format |
| Slack bot token | `xoxb-` with realistic digit segment structure |
| Kubeconfig | Structurally valid YAML with fake cluster endpoint and bearer token |
| Database dump | `backup.sql` with real-looking schema, connection header, and fake `INSERT` rows |
| DNS canary token | Unique 16-char subdomain label for a domain you control |

---

## Quick start

```sh
# Build from source (requires Go 1.22+)
go build -o decoyd ./cmd/decoyd

# Run (Windows)
.\decoyd.exe

# Run (Linux)
./decoyd
```

First launch shows a splash screen → press any key → main menu.

**Generate a decoy**
1. Select **1. Generate a decoy** from the main menu
2. `↑/↓` or `j/k` to navigate, `Space` to toggle token types
3. Optional: navigate to the **Label** field and type a note
4. `Enter` to generate — tokens are saved to the local database instantly
5. Any key to return to the main menu

**Keybindings (global)**

| Key | Action |
|---|---|
| `↑` / `k`, `↓` / `j` | Navigate |
| `Space` | Toggle selection |
| `Enter` | Confirm |
| `Esc` | Back |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

---

## Data directory

| Platform | Path |
|---|---|
| Linux | `~/.decoyd/` |
| Windows | `%APPDATA%\Decoyd\` |

The embedded database (`decoyd.db`) lives here. No server, no cloud, no account.

---

## Requirements

- Go 1.22+
- Windows or Linux (amd64)

---

## Build status

| Phase | Status |
|---|---|
| 0 — Foundation (TUI shell, theme, splash, menu, CI) | ✅ Complete |
| 1 — Token generation (8 types, local storage) | ✅ Complete |
| 2 — Deployment (write to disk, token list screen) | 🔜 Planned |
| 3 — Alerting (Discord, Slack, Telegram, ntfy, …) | 🔜 Planned |
| 4 — Detection engine (file watcher, dashboard) | 🔜 Planned |
| 5 — Polish (onboarding wizard, multi-profile) | 🔜 Planned |
| 6 — Packaging & distribution (GoReleaser, install scripts) | 🔜 Planned |

---

## License

MIT — see [LICENSE](LICENSE).



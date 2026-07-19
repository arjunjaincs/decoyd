# Decoyd

> Self-hosted canary token generator and monitor with an interactive TUI.

**Stack:** Go · bubbletea + lipgloss (TUI) · bbolt (embedded storage) · single static binary. No cgo. No server required. On Windows, `decoyd install` requires `powershell.exe` (present on all stock Windows installations).

---

## What it does

Decoyd generates realistic-looking decoy credentials and monitors them for access. Drop a fake AWS key, SSH private key, or `.env` file somewhere an attacker would find it — when they touch the file, you get an instant alert.

The TUI starts monitoring automatically when you open it. No separate background service needed — though `decoyd install` registers one for persistent post-reboot monitoring.

**Token types**

| Type | What it produces |
|---|---|
| AWS credentials | `AKIA...` access key + secret in real credentials file format |
| SSH private key | Valid ed25519 keypair (OpenSSH PEM + `.pub` sibling), never registered anywhere |
| `.env` secrets | `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET` with realistic fake values |
| GitHub PAT | `ghp_` + 36 alphanumeric chars — matches GitHub's real format |
| Slack bot token | `xoxb-` with realistic digit segment structure |
| Kubeconfig | Structurally valid YAML with fake cluster endpoint and bearer token |
| Database dump | `backup.sql` with real-looking schema, connection header, and fake `INSERT` rows |
| DNS canary token | Unique 16-char subdomain label for a domain you control |

---

## Quick start

```sh
# Build from source (requires Go 1.25+)
go build -o decoyd ./cmd/decoyd

# Run (Linux)
./decoyd

# Run (Windows)
.\decoyd.exe
```

Every launch plays a brief animated splash screen → press any key → centered main menu.

The TUI detects your terminal's Unicode/VT support at startup using a three-stage check: Windows Terminal (`WT_SESSION` env var), an existing `ENABLE_VIRTUAL_TERMINAL_PROCESSING` console flag, and finally an active probe (`SetConsoleMode` succeeds on Windows 10 v1511+). Full Unicode glyphs, rounded box borders, and animated `»/›` cursor work in Windows Terminal, standalone PowerShell 5.1+, macOS Terminal, and Linux. Plain ASCII fallbacks (`+-|`, `>`) activate only on genuine legacy consoles. Every screen is centered as a focused card regardless of terminal width.

**Generate a decoy**
1. Select **1. Generate a decoy** from the main menu
2. `↑/↓` or `j/k` to navigate, `Space` to toggle token types
3. Optional: navigate to the **Label** field and type a note
4. `Enter` to generate — tokens are saved to the local database instantly
5. Any key to return to the main menu

**Deploy a decoy**
1. Select **2. Deploy existing decoys**
2. Pick a token from your generated list
3. Pick a destination (Home, Downloads, Desktop, `~/.ssh`, or a custom path)
4. Press `d` on the confirmation screen for a **dry-run** preview, or `Enter` to write
5. The deployed path is recorded — visible in the token list

**Configure alert channels**
1. Select **3. Alert settings** from the main menu
2. Press `Enter` on the **Channel** row to cycle through supported channels:
   - Discord webhook, Slack webhook, Telegram bot, Microsoft Teams, ntfy.sh, Generic webhook
3. `Tab` / `Shift+Tab` to move between fields; type your webhook URL (must start with `https://`) or bot token
4. Navigate to **Send test alert** and press `Enter`, or press `s` from anywhere on the form
5. On success the config is saved automatically to `alert_config.json` in your data directory

Credential fields are masked (`••••last4`) when unfocused so secrets don't linger on screen.
If the URL or token is missing or malformed, a clear error is shown before any network request is made.

**View / manage tokens**
- Select **4. View tokens** from the main menu
- `↑/↓` or `j/k` to browse, `d` to delete (with confirm step), `e` to edit the Notes label inline, `esc` to go back
- The deployed file is NOT removed from disk on delete (by design — the physical canary stays in place)
- CLI fallback: `decoyd list`

**Watcher / status dashboard**
1. Select **5. Status** from the main menu to see watcher state and recent trigger events
2. The watcher starts automatically when you open the TUI — no separate command needed
3. `↑/↓` to scroll trigger list, `Enter` to drill into an event, `r` to manually refresh, `Esc` to go back
4. For persistent background monitoring after reboot: `decoyd install` (see CLI section below)

**CLI (for scripting)**

```sh
decoyd list             # tab-aligned table of all tokens
decoyd remove <id>      # delete a token record (file NOT removed from disk)
decoyd triggers         # recent trigger events (newest-first)
decoyd watch            # run headless watcher (for servers / no TUI)
decoyd install          # register OS service: systemd unit (Linux) or Task Scheduler task (Windows)
decoyd help
```

**Keybindings (global)**

| Key | Action |
|---|---|
| `↑` / `k`, `↓` / `j` | Navigate |
| `Space` | Toggle selection |
| `Enter` | Confirm / cycle |
| `Esc` | Back |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

---

## Data directory

| Platform | Path |
|---|---|
| Linux | `~/.decoyd/` |
| Windows | `%APPDATA%\Decoyd\` |

Files written here:

| File | Contents |
|---|---|
| `decoyd.db` | bbolt database — every token you've generated and deployed |
| `alert_config.json` | Alert channel credentials (webhook URLs, bot tokens) — **0600** |
| `deployed_tokens.json` | Snapshot of deployed token paths — read by the headless watcher |
| `triggers.jsonl` | Append-only log of every trigger event and alert status |
| `watcher.pid` | Lock file holding the watcher's PID — present while watcher is running |

No server, no cloud, no account required.

---

## Requirements

- Go 1.25+ (to build from source)
- Windows or Linux (amd64)
- `powershell.exe` on PATH (Windows only, required for `decoyd install` — present on all stock Windows installations)

---

## Build status

| Phase | Status |
|---|---|
| 0 — Foundation (TUI shell, theme, splash, menu, CI) | ✅ Complete |
| 1 — Token generation (8 types, local storage) | ✅ Complete |
| 2 — Deployment (write to disk, token list, CLI subcommands) | ✅ Complete |
| 3 — Alerting (Discord, Slack, Telegram, Teams, ntfy, webhook) | ✅ Complete |
| 4 — Detection engine (file watcher, TUI dashboard, `decoyd install`) | ✅ Complete |
| 5 — Polish (desktop notification, multi-channel UI, onboarding) | 🔜 Planned |
| 6 — Packaging & distribution (GoReleaser, install scripts) | 🔜 Planned |

[![CI](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml/badge.svg)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml)

---

## Notes

**`decoyd remove <id>` only deletes the database record — it does not touch the file on disk.** This is intentional: a management command shouldn't silently delete files you may have placed carefully. To fully clean up, delete the deployed file yourself first, then run `decoyd remove`.

**GitHub / GitLab secret scanning.** The AWS, GitHub PAT, and Slack token formats generated by Decoyd match the real formats closely enough that GitHub's own secret-scanning pipeline may flag them if they accidentally land in a git commit. This is by design (the decoys look real), but worth knowing if you store token values somewhere that gets scanned.

**Protect your config directory.** Both `decoyd.db` and `alert_config.json` are written at `0600` (Linux). The `~/.decoyd/` directory itself is `0700`. These files contain the map of every trap you've planted and your alert channel credentials — don't back them up to a shared location or commit them to version control.

**Alert channel credentials are masked in the TUI.** Webhook URLs and bot tokens are stored in `alert_config.json` (0600) and displayed as `••••last4` when the field is not focused. They never appear in error messages or log output.

---

## License

MIT — see [LICENSE](LICENSE).

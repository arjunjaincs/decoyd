# Decoyd

> Drop fake credentials where an attacker would look. Get alerted the moment anything touches them.

Decoyd is a self-hosted canary token tool. It generates realistic-looking decoy credentials — fake AWS keys, SSH private keys, `.env` files, and more — and watches them for access. When a file is opened, renamed, or deleted, you receive an alert over whichever channel you configure (Discord, Slack, Telegram, Teams, ntfy.sh, or a generic webhook).

No cloud account. No ongoing cost. One static binary.

---

## Token types

| Type | What it produces |
|---|---|
| AWS credentials | `AKIA…` access key + secret in real credentials file format |
| SSH private key | Valid ed25519 keypair (OpenSSH PEM + `.pub` sibling), never registered anywhere |
| `.env` secrets | `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET` with realistic fake values |
| GitHub PAT | `ghp_` + 36 alphanumeric chars — matches GitHub's real token format |
| Slack bot token | `xoxb-` with realistic digit segment structure |
| Kubeconfig | Structurally valid YAML with fake cluster endpoint and bearer token |
| Database dump | `backup.sql` with real-looking schema, connection header, and fake `INSERT` rows |
| DNS canary token | Unique 16-char subdomain label for a domain you control |

---

## Quick start

```sh
# Requires Go 1.25+
go build -o decoyd ./cmd/decoyd

# Linux
./decoyd

# Windows
.\decoyd.exe
```

The first launch opens a brief animated splash → press any key → main menu.
Every screen is centered in your terminal. The TUI starts the file watcher automatically — no separate background process needed for basic use.

---

## Workflow

### 1. Generate a decoy

1. Select **1. Generate a decoy** from the main menu
2. `↑`/`↓` or `j`/`k` to navigate, `Space` to toggle token types
3. Optionally navigate to the **Label** field and type a note
4. `Enter` to generate — tokens are saved instantly to the local database
5. Any key to return to the main menu

### 2. Deploy a decoy

1. Select **2. Deploy existing decoys**
2. Pick a token from your list
3. Pick a destination: Home, Downloads, Desktop, `~/.ssh`, or a custom path
4. Press `d` on the confirmation screen for a **dry-run** preview, or `Enter` to write

The deployed path is recorded in the database and shown in the token list.

### 3. Configure alert channels

1. Select **3. Alert settings** from the main menu
2. Press `Enter` on the **Channel** row to cycle through: Discord webhook, Slack webhook, Telegram bot, Microsoft Teams, ntfy.sh, Generic webhook
3. `Tab` / `Shift+Tab` to move between fields; type your webhook URL or bot token
4. Navigate to **Send test alert** and press `Enter` (or press `s` from anywhere on the form)
5. The config is saved to `alert_config.json` automatically on success

Webhook URLs and bot tokens are masked (`••••last4`) when unfocused so secrets do not linger on screen. A clear error is shown before any network request if the URL is missing or malformed.

**Multiple alert channels:** you can configure more than one channel and assign individual tokens to specific channels. Tokens without an assignment use the default channel.

### 4. Monitor and manage tokens

1. Select **5. Status** from the main menu to see watcher state and recent trigger events
2. `↑`/`↓` to scroll, `Enter` to drill into an event, `r` to refresh, `Esc` to go back
3. Select **4. View tokens** to browse your full token list

In the token list:
- `d` — delete a token record (two-step confirmation if a deployed file exists — see below)
- `e` — edit the Notes label inline
- `a` — assign an alert channel to this token
- `Esc` — back to menu

**Deleting a token with a deployed file** uses a two-step confirmation:
1. First screen confirms deletion of the database record
2. Second screen asks whether to also permanently delete the file on disk
   - `y` — delete both the record and the file
   - `n` — delete the record, leave the file on disk
   - `Esc` — cancel everything; nothing is deleted

---

## CLI reference

```sh
decoyd                        # launch the interactive TUI
decoyd list                   # tab-aligned table of all token records
decoyd remove <id>            # delete a token record (deployed file is kept)
decoyd remove --purge <id>    # delete the record AND the deployed file from disk
decoyd triggers               # recent trigger events, newest-first
decoyd watch                  # run the headless watcher (no TUI — useful for servers)
decoyd install                # register watcher as an OS service (see below)
decoyd help                   # print usage
```

`decoyd remove` without `--purge` leaves the deployed file on disk — this is intentional. A management command shouldn't silently delete files you placed carefully. Use `--purge` when you want a full cleanup.

### Persistent background monitoring

`decoyd install` registers `decoyd watch` to run automatically at login:

- **Linux:** creates a `systemd --user` unit (`decoyd-watch.service`)
- **Windows:** registers a Task Scheduler task (`DecoydWatch`) using the raw Task Scheduler COM API — no administrator rights needed

After registration the watcher starts at next logon. The TUI's embedded watcher remains active while the TUI is open regardless of whether `install` has been run.

---

## Keybindings (global)

| Key | Action |
|---|---|
| `↑` / `k`, `↓` / `j` | Navigate |
| `Space` | Toggle selection |
| `Enter` | Confirm / cycle |
| `Esc` | Back |
| `?` | Help overlay |
| `Ctrl+V` | Paste (in text fields) |
| `Ctrl+C` | Copy current field content |
| `q` / `Ctrl+C` | Quit (when not in a text field) |

---

## Data directory

| Platform | Path |
|---|---|
| Linux | `~/.decoyd/` |
| Windows | `%APPDATA%\Decoyd\` |

| File | Contents |
|---|---|
| `decoyd.db` | bbolt database — every token you've generated and deployed |
| `alert_config.json` | Alert channel credentials (webhook URLs, bot tokens) — **0600** |
| `deployed_tokens.json` | Snapshot of deployed token paths — read by the headless watcher |
| `triggers.jsonl` | Append-only log of every trigger event and alert status |
| `watcher.pid` | Lock file holding the watcher's PID while the watcher is running |

---

## Security notes

**Protect your data directory.** `decoyd.db` and `alert_config.json` are written at `0600` (Linux); the `~/.decoyd/` directory is `0700`. These files contain the map of every trap you've planted and your alert channel credentials. Do not back them up to a shared location or commit them to version control.

**Alert credentials are masked in the TUI.** Webhook URLs and bot tokens are displayed as `••••last4` when unfocused and never appear in error messages or log output.

**GitHub / GitLab secret scanning.** The AWS, GitHub PAT, and Slack token formats generated by Decoyd match the real formats closely enough that GitHub's secret-scanning pipeline may flag them if they end up in a git commit. This is intentional — the decoys are meant to look real — but worth knowing.

**Windows file-access detection.** On Windows, only Write, Rename, and Remove events trigger alerts. Read-only access (`cat`, opening in Notepad) is not detected due to a Windows kernel limitation with directory-change notifications. This is a v1 constraint.

---

## Requirements

- Go 1.25+ (to build from source)
- Windows or Linux (amd64)
- `powershell.exe` on PATH (Windows only, for `decoyd install` — present on all stock Windows installations)

[![CI](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml/badge.svg)](https://github.com/arjunjaincs/decoyd/actions/workflows/ci.yml)

---

## License

MIT — see [LICENSE](LICENSE).

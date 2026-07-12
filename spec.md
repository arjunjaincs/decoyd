# Decoyd — Engineering Spec

Self-hosted, single-binary canary token generator and monitor with an interactive TUI.

**Stack:** Go · bubbletea + lipgloss (TUI) · bbolt (embedded storage, pure-Go, no cgo) · single static binary for Windows + Linux, no runtime dependencies.

This document is a build brief. Each phase assumes everything in the previous phase is done and working. Hand this to an AI coding assistant one phase at a time, in order.

---

## UI/UX Design System

Defined once, up front, in `internal/tui/theme.go`, so every screen built across every phase is visually consistent from the start.

### Color palette

| Token | Hex | Used for |
|---|---|---|
| Background | `#0d1117` | App background |
| Primary accent | `#3fb950` | Selected menu item, success states, borders, wordmark |
| Warning | `#d29922` | Non-fatal warnings (e.g. "file already exists") |
| Danger | `#f85149` | Errors, live trigger alerts inside the TUI |
| Text primary | `#c9d1d9` | Body text |
| Text muted | `#8b949e` | Help footer, timestamps, secondary detail |
| Border | `#30363d` | Box borders and dividers |

### Layout conventions

- Every screen boxed with a rounded lipgloss border in the border color, screen name in the top border line
- A persistent one-line help footer at the bottom of every screen showing that screen's active keybindings, in muted text
- Selected list items get a left `▸` marker **plus** the accent color — never rely on color alone (some terminals remap colors or run with `NO_COLOR` set)
- Consistent padding: 1 line above/below content inside every box, 2 spaces left/right

### Screen inventory

| Screen | Purpose | Built in |
|---|---|---|
| Splash | First-run welcome, version number | Phase 0 |
| Main menu | Entry point, routes to every other screen | Phase 0 |
| Generate | Multi-select token type picker | Phase 1 |
| Deploy | Destination picker + confirmation | Phase 2 |
| Token list | Table of all deployed tokens | Phase 2 |
| Alert setup | Channel picker + config form + test-send | Phase 3 |
| Status / dashboard | Watcher health, active tokens, recent triggers | Phase 4 |
| Trigger detail | Drill into a single trigger event's full detail | Phase 4 |
| Onboarding wizard | Guided first-run setup | Phase 5 |
| Help overlay | Full keybinding cheatsheet, toggled with `?` | Phase 5 |

### Keybinding conventions

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | Move selection (vim-style alternates supported throughout) |
| `Enter` | Confirm / select |
| `Space` | Toggle item in multi-select lists |
| `Esc` | Back to previous screen |
| `?` | Toggle help overlay |
| `q` / `Ctrl+C` | Quit (with confirmation if the watcher is running) |

### Accessibility & terminal compatibility

- Respect `NO_COLOR` env var — fall back to bold + `▸` marker instead of color
- Handle terminal resize (bubbletea's `WindowSizeMsg`) by reflowing box widths, not clipping
- Below ~60 columns width, show a one-line message asking the user to widen their terminal rather than rendering broken boxes
- No emoji as the *only* indicator of state — pair any icon with a text label

### Wireframes

**Splash screen (first run only)**
```
┌────────────────────────────────────────┐
│                                         │
│              D E C O Y D                │
│      self-hosted deception  ·  v0.1.0   │
│                                         │
│        press any key to continue        │
│                                         │
└────────────────────────────────────────┘
```

**Main menu**
```
┌─ Decoyd ─────────────────────────────┐
│                                        │
│  ▸ 1. Generate a decoy                 │
│    2. Deploy existing decoys           │
│    3. Alert settings                   │
│    4. Status                           │
│    5. Quit                             │
│                                        │
└────────────────────────────────────────┘
 ↑/↓ navigate   enter select   ? help   q quit
```

**Status dashboard**
```
┌─ Status ───────────────────────────────────────┐
│ Watcher   ● running     uptime 3d 4h            │
│ Tokens watched: 6                               │
│                                                  │
│ Recent triggers                                 │
│  ⚠ .env            2m ago      alert sent ✓     │
│  ⚠ id_ed25519       1d ago      alert sent ✓     │
│                                                  │
└──────────────────────────────────────────────────┘
 ↑/↓ browse   enter view detail   esc back   ? help
```

---

## Phase 0 — Foundation

**Time:** part-time 2–3 days · focused 1 day

### What this phase builds
The project skeleton, the design system above wired into a real theme file, and a working navigable menu that doesn't do anything real yet but looks and feels like the finished tool.

### Project structure
```
decoyd/
  cmd/decoyd/main.go
  internal/tui/
    root.go                 root model, screen state machine
    splash.go
    mainmenu.go
    help.go                 help overlay, shared across screens
    theme.go                palette + shared lipgloss styles
    components/             shared widgets: list, form input, spinner
  internal/store/
  internal/tokens/
  internal/deploy/
  internal/alert/
  internal/watch/
  internal/config/
  go.mod / go.sum
  LICENSE (MIT)
  README.md (placeholder)
  .gitignore
  .github/workflows/ci.yml
```

### Features to implement
- **theme.go** — every color from the palette table as a named `lipgloss.Color` constant, plus reusable shared styles (`BoxStyle`, `SelectedItemStyle`, `HelpTextStyle`, `ErrorStyle`) — no screen hardcodes a raw hex value
- **Config path resolution** — per-OS data directory: `os.UserHomeDir()` + `.decoyd/` on Linux, `os.UserConfigDir()` + `Decoyd/` on Windows; create the directory on first run if missing
- **Root bubbletea model** — screen-state enum, routes key events to the active sub-model, renders active view, tracks whether this is a genuine first run
- **Splash screen** — shown only on first run
- **Main menu screen** — arrow/number navigation
- **Help overlay** — shared component any screen can toggle with `?`
- **NO_COLOR and resize handling** — implement both from the start, retrofitting later means touching every screen twice
- **Cross-compilation check** — confirm `GOOS=windows` and `GOOS=linux` both build clean from one codebase, no build tags needed yet

### CI setup
- GitHub Actions workflow on every push: `go build ./...` and `go test ./...` across a linux/windows build matrix

### Tests
- **Unit:** table-driven test for the config path function across simulated OS values
- **Unit:** root model initializes without panic, starts on Splash on first run and MainMenu on subsequent runs
- **Manual:** resize the terminal mid-session, confirm reflow instead of breakage; set `NO_COLOR=1`, confirm selection is still visually clear

> **Done when:** `go build` produces a working binary for both OSes, the splash-then-menu flow works, resize and NO_COLOR both behave correctly, CI is green on a clean clone.

---

## Phase 1 — Token Generation

**Time:** part-time 1.5–2 weeks · focused 5–7 days

### What this phase builds
The decoy generators and local storage. Eight token types for genuine variety.

### Data model
```go
type Token struct {
    ID             string
    Type           string   // see token type table below
    Value          string   // the generated secret/content
    Filename       string   // suggested filename
    CreatedAt      time.Time
    DeployedPath   string
    AlertChannelID string
    Triggered      bool
    TriggeredAt    *time.Time
    Notes          string   // optional user label, e.g. "prod server decoy"
}
```

### Storage layer (`internal/store`)
- bbolt embedded store, single file in the config directory, one bucket keyed by Token ID
- `SaveToken`, `GetToken`, `ListTokens`, `UpdateToken`, `DeleteToken`
- `ListByType(t string) ([]Token, error)` for the token-list screen's filter view

### Token types

| Type | What it produces |
|---|---|
| AWS credentials | Realistic access key ID (`AKIA` + 16 chars) and secret, formatted as a real AWS credentials file section |
| SSH private key | Real, validly-formatted ed25519 keypair (via `crypto/ed25519`) that passes format inspection but is never registered anywhere |
| .env secrets | Common variable names (`DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET`) with format-correct randomized fake values |
| GitHub PAT | String matching GitHub's real format (`ghp_` + 36 alphanumeric chars) |
| Slack bot token | Matches Slack's real format (`xoxb-` + realistic segment structure) |
| Kubeconfig | Fake but structurally valid kubeconfig YAML with decoy cluster endpoint + fake bearer token |
| Database dump | Small fake SQL file (`backup.sql`) with a realistic connection-string comment header and fake INSERT statements |
| DNS canary token | Unique, unguessable hostname the user wires into a domain they control; detection covered in Phase 4 |

### Wiring into the TUI
- Generate screen: multi-select checklist grouped by category (Cloud/Infra, Dev Tools, Data) rather than one flat list
- Optional free-text label per generated token (stored in `Notes`)

### Tests
- **Unit:** each generator's output matches its real-world format via regex (AWS key ID, GitHub PAT prefix, Slack token prefix, etc.)
- **Unit:** SSH key output parses successfully with Go's SSH key parsing package
- **Unit:** kubeconfig output parses as valid YAML with expected top-level keys
- **Unit:** 1,000-generation collision test on IDs
- **Unit:** storage round-trip test for every field including `Notes`

> **Done when:** all eight token types can be generated from the menu, each looks convincingly real in its target format, and each is correctly saved and listable.

---

## Phase 2 — Deployment

**Time:** part-time ~1 week · focused 3–4 days

### What this phase builds
Placing generated tokens where an attacker would actually find them, plus the token list screen.

### Deployer (`internal/deploy`)
- `DeployToFile(t Token, targetDir string) error` — writes the value to `targetDir/t.Filename`, sets realistic permissions (0600 for keys), records `DeployedPath`
- Refuses to overwrite an existing file at the target path — returns a clear error instead
- Dry-run mode: preview what would be written and where without touching disk
- Path selection: presets (home directory, Downloads, Desktop) plus free-text custom path

### DNS canary token setup
- Generate a random 16-character subdomain label, unique per token
- Store the expected full hostname (label + user's own domain, entered during setup)
- One-time instruction screen: create a DNS record for that hostname on a domain the user controls

> **Note:** this phase only generates and records the DNS token. Actual query detection depends on DNS provider log access and is a Phase 4 concern.

### Token list screen
```
┌─ Deployed Tokens ────────────────────────────────┐
│ Type            Location             Triggered    │
│ ──────────────────────────────────────────────    │
│ SSH key         ~/.ssh/id_ed25519    no            │
│ .env            ~/projects/.env      no            │
│ GitHub PAT      ~/Downloads/.env2    yes  ⚠         │
│                                                     │
└─────────────────────────────────────────────────────┘
 ↑/↓ browse   enter details   d delete   esc back
```

### New commands

| Command | Behavior |
|---|---|
| `decoyd list` | Prints deployed tokens for scripting/CI use outside the TUI |
| `decoyd remove <id>` | Deletes the deployed file (if file-based) and the token record |

### Tests
- **Unit:** `DeployToFile` writes expected content to a temp dir, refuses to overwrite an existing file
- **Unit:** permission bits verified 0600 on Linux for key-type decoys
- **Unit:** DNS label uniqueness across 1,000 generations
- **Manual:** full deploy flow end to end for at least three token types

> **Done when:** any token type can be deployed to a real chosen location, and `decoyd list` accurately reflects everything currently deployed.

---

## Phase 3 — Alerting

**Time:** part-time 1.5–2 weeks · focused 5–6 days

### What this phase builds
The notification system, built as a pluggable interface, expanded to cover channels people already use daily.

### Core interface (`internal/alert`)
```go
type AlertPayload struct {
    TokenID     string
    TokenType   string
    Path        string    // file path or DNS hostname that fired
    TriggeredAt time.Time
    Detail      string    // human-readable extra context
}

type Alerter interface {
    Send(ctx context.Context, payload AlertPayload) error
}
```

### Channels for v1

| Channel | Notes |
|---|---|
| Discord webhook | Formatted embed with token type, path, timestamp |
| Slack webhook | Same shape, Slack's block-message format |
| Telegram bot | User-created bot token + chat ID; instant push to phone, no extra app |
| Microsoft Teams webhook | Covers corporate-environment users |
| ntfy.sh push | No signup on either end, good zero-friction default |
| Generic webhook | POSTs the raw `AlertPayload` as JSON to any URL — escape hatch |
| Local desktop notification | Native OS notification (via `beeep`) for self-monitoring machines |

### Alert setup screen
```
┌─ Alert Settings ─────────────────────────────┐
│ Channel:  [ Discord webhook        ▾ ]        │
│ Webhook URL:                                  │
│  https://discord.com/api/webhooks/••••••••    │
│                                                │
│           [ Send test alert ]                 │
│                                                │
│  ✓ Test alert delivered successfully           │
└────────────────────────────────────────────────┘
 tab next field   enter confirm   esc back
```
- Form-style input, tab between fields, masked/truncated display for sensitive URLs once entered
- Test-send happens inline, before config is saved
- Support configuring more than one channel, with one marked default

### Tests
- **Unit:** `httptest.NewServer`-based test per Alerter, asserting correct payload shape for each channel
- **Unit:** each Alerter returns a clean error (not a panic) on non-2xx response and on timeout
- **Unit:** generic webhook Alerter sends valid, parseable JSON matching `AlertPayload` exactly
- **Manual:** real end-to-end test-send against a real Discord webhook, a real Telegram bot, and ntfy.sh

> **Done when:** a user can configure any of the seven channels through the form, get a real inline test alert, and see clear success/failure feedback.

---

## Phase 4 — Detection Engine

**Time:** part-time 2–2.5 weeks · focused 1–1.5 weeks

### What this phase builds
The background watcher, the dashboard screen, plus alert quality features (rate limiting, quiet hours) that matter once running for real over weeks. **This is the hardest phase — budget the most slack here.**

> **Platform caveat:** Go's `fsnotify` reliably reports Create/Write/Rename/Remove on both OSes, but pure read-only "someone opened and viewed this file without modifying it" is not reliably cross-platform through that library.
>
> - **Linux:** true open/read detection via raw inotify (`golang.org/x/sys/unix`, `IN_OPEN | IN_ACCESS`)
> - **Windows:** genuine read-only detection needs ETW or a filesystem minifilter driver — both heavy for v1. v1 scope on Windows: Create/Write/Rename/Delete via fsnotify's `ReadDirectoryChangesW` backend, which catches copying, moving, renaming, or exfiltrating the file — just not a pure open-and-look. True read detection on Windows is a **v1.1 item**.

### File watcher (`internal/watch`)
- Linux: raw inotify on `IN_OPEN | IN_ACCESS | IN_MODIFY | IN_MOVE_SELF | IN_DELETE_SELF` per deployed file
- Windows: fsnotify watch on each parent directory, filtered to the specific decoy filename, covering Write/Rename/Remove
- One long-running watcher process manages every currently-deployed token, not one process per token

### Running persistently
- `decoyd watch` — the long-running foreground command
- Linux: user-level systemd unit at `~/.config/systemd/user/decoyd.service`, enabled on install
- Windows v1: register a Scheduled Task running `decoyd watch` at logon (avoids needing admin rights); a true Windows Service (`golang.org/x/sys/windows/svc`) is a v1.1 upgrade

### Alert quality features
- **Debounce:** repeated events on the same file within a short window collapse into one trigger
- **Rate limiting:** cap alert volume per token per hour so a false-positive loop can't flood the configured channel — log locally but stop sending once capped, until it resets
- **Quiet hours (optional, configurable):** alerts logged but not pushed during a defined window
- **Best-effort process attribution on Linux** via `/proc`, included in alert detail when resolvable
- **Ignore list:** configurable list of known noisy process names filtered out before alerting

### Tying it together
- On a real trigger: write the trigger event to local storage **before** attempting to send the alert — durable record even if delivery fails
- Look up the token's configured alert channel and dispatch through the Phase 3 Alerter interface
- `decoyd status` — shows whether the watcher is running, uptime, tokens actively watched

### Trigger detail screen
```
┌─ Trigger Detail ─────────────────────────────┐
│ Token:      .env (id: 4f2a91c8)               │
│ Path:       ~/projects/.env                    │
│ Time:       2026-07-11 14:32:07                │
│ Event:      file opened                        │
│ Process:    unknown (best-effort, not resolved) │
│ Alert:      sent via Discord webhook  ✓         │
│                                                 │
└─────────────────────────────────────────────────┘
 esc back
```

### Tests
- **Integration:** deploy a decoy to a temp dir, start the watcher, programmatically touch the file, assert the trigger callback fires within a short timeout
- **Unit:** debounce logic collapses rapid repeated events into one trigger
- **Unit:** rate limiter caps alerts correctly and resumes after the window resets
- **Unit:** quiet-hours logic suppresses push while still logging locally
- **Unit:** ignore-list filtering
- **Manual:** real trigger test on both Linux (open/read) and Windows (write/rename)

> **Done when:** touching a real decoy fires a real alert within seconds, the dashboard reflects watcher state and recent history accurately, rate limiting/quiet hours behave correctly under simulated repeated triggers. **This is the real MVP milestone.**

---

## Phase 5 — Polish

**Time:** part-time ~1 week · focused 4–5 days

### What this phase builds
Turns a working tool into a finished-feeling one: a real first-run experience, multi-profile support, every rough edge smoothed.

### Onboarding wizard
- Replaces raw menu-hunting on first run: guided sequence — pick a token type, pick where to deploy, pick an alert channel, send a test alert, done — one working decoy inside two minutes
- Skippable for anyone who wants to go straight to the main menu

### Multi-profile support
- Separate named profiles (e.g. "work", "homelab") each with their own tokens and alert channels, stored as separate bbolt files under the config directory
- `decoyd --profile work` to select from CLI; TUI shows active profile name in the main menu header

### Config export/import
- `decoyd export` — dumps the current profile's token and alert config (not deployed files) to a single JSON file for backup
- `decoyd import <file>` — restores from that file

### Visual & UX pass
- Apply the theme consistently across every screen including onboarding and profile screens
- Loading indicators for anything async (test alerts, watcher startup)
- Full pass through the help overlay so every screen's cheatsheet is accurate

### Error handling audit
- No write permission on deploy directory, invalid/unreachable webhook URL, missing/corrupted config directory on startup, malformed import file — every case shows a clear plain-English message, never a raw Go error or stack trace

### Cleanup commands
- `decoyd remove --all` — deletes every deployed decoy and clears all token records for the active profile
- `decoyd uninstall` — stops/disables the background watcher, optionally removes the config directory entirely

### Documentation
- Full README: what it is, why, install steps, quick-start walkthrough
- Terminal demo recording (asciinema/terminalizer) covering the onboarding wizard specifically
- CONTRIBUTING notes for adding a new token type or alert channel

### Tests / QA
- **Manual QA checklist:** fresh install on a clean Linux VM and a clean Windows VM, full onboarding wizard walkthrough on both, every documented error case deliberately triggered
- **Read-through test:** hand the README to someone unfamiliar with the project, watch them follow it unaided

> **Done when:** a first-time user can complete the onboarding wizard and get a real alert inside two minutes with no help, and every error path shows a clear message instead of a crash.

---

## Phase 6 — Packaging & Distribution

**Time:** part-time ~1 week · focused 3–4 days

### What this phase builds
Everything needed for a stranger to go from a link to a running install in under two minutes, at zero ongoing hosting cost, with a release trustworthy enough for a security tool.

### Automated release pipeline
- GoReleaser config — builds windows/amd64 and linux/amd64 (linux/arm64 optional), packages as archives with SHA256 checksums
- GitHub Actions release workflow triggered on a version tag push (`v*.*.*`), runs GoReleaser, publishes binaries automatically
- **Signed releases:** sign build artifacts with cosign/sigstore as part of the release workflow — free, GitHub-Actions-native, and a real trust signal for a security tool specifically

### One-line install
- `install.sh` for Linux — detects architecture, fetches latest release via GitHub API, verifies checksum, places binary on PATH; run as `curl -fsSL <url> | sh`
- `install.ps1` equivalent for Windows via PowerShell

### Landing page
- Single static page: what it does, the onboarding-wizard demo recording, the copy-paste install command, link to signature verification instructions, link to the GitHub repo
- Hosted free on Vercel or Cloudflare Pages — static, no backend, zero ongoing cost regardless of traffic
- Domain pointed at it if one's been registered

### Tests / QA
- **Manual:** install script run on a genuinely fresh Linux container and a fresh Windows VM
- **Manual:** tag a release, confirm the pipeline runs automatically, binaries appear on the Releases page, cosign signature verifies successfully

> **Done when:** a complete stranger can land on the site, copy one command into a terminal, verify the release is genuinely signed if they choose to, and have Decoyd running within two minutes. **This is the actual launch-ready state.**

---

## v1 vs v1.1 Scope

| v1 (launch) | v1.1 and later |
|---|---|
| All 8 token types | Decoy document with embedded tracking pixel (needs a reachable listener) |
| Discord/Slack/Telegram/Teams/ntfy/generic webhook/local notification | Email/SMTP alerting |
| Windows + Linux binaries, signed, one-line install | macOS support |
| Onboarding wizard, multi-profile | Syslog/SIEM forwarding |
| Rate limiting, quiet hours, debounce | Automatic credential rotation/cycling |
| Linux full read detection, Windows write/rename/delete detection | True Windows read-detection (ETW/minifilter), true Windows Service |
| | Homebrew/Scoop packaging |

---

*Decoyd · github.com/arjunjaincs/decoyd*
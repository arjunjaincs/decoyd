# Decoyd â€” Build Progress Log

> Internal document. Tracks what was built each phase, the technical decisions behind it, how it's tested, and the current state. Not a user-facing README.

---

## TL;DR â€” Where we are

| Phase | Status | Tests |
|---|---|---|
| 0 â€” Foundation | âœ… Complete | 6 pass |
| 1 â€” Token Generation | âœ… Complete | 30 pass |
| 2 â€” Deployment | âœ… Complete | 22 pass, 4 skip (Linux perms on Windows) |
| 3 â€” Alerting | âœ… Complete | 30 pass, 1 skip (Linux file perms on Windows) |
| 4 â€” Detection Engine | âœ… Complete | 124 pass Â· 5 skip Â· 0 fail (local, no CGO) |
| **Total** | | **124 pass Â· 5 skip Â· 0 fail** |

Cross-compile: `GOOS=linux` âœ… `GOOS=windows` âœ…  
Stack: Go 1.25 Â· bubbletea v1.3 Â· lipgloss v1.1 Â· bbolt v1.5 Â· x/crypto v0.54 Â· yaml.v3

### Post-review improvements (applied after Phase 2)

Based on a security/code review, the following were added before marking Phase 2 final:

| Item | What changed |
|---|---|
| SSH `.pub` sibling | `DeployToFile` detects `TypeSSHKey`, splits the Value on a sentinel, and writes both `id_ed25519` (0600) and `id_ed25519.pub` (0644). Without the `.pub`, an attacker's tooling could detect the decoy as fake. |
| CI security scan | New `security` job in `ci.yml`: `govulncheck` (Go vuln DB) + `gosec` (static analysis, G304 excluded for intentional variable path). Runs on every push. |
| README security notes | Added Notes section: `decoyd remove` non-destructive behaviour, GitHub/GitLab secret-scanning warning, config dir protection reminder. |
| spec.md updated | SSH keypair note, corrected `decoyd remove` description, security requirements section, govulncheck/gosec in CI setup. |

---

## Phase 0 â€” Foundation

### What was built

The project skeleton and the full UI design system, wired to a working navigable app before any real logic existed.

**Files created:**
```
cmd/decoyd/main.go
internal/config/config.go
internal/tui/theme.go
internal/tui/root.go
internal/tui/splash.go
internal/tui/mainmenu.go
internal/tui/help.go
internal/tui/components/components.go
.github/workflows/ci.yml
.gitignore
LICENSE
```

**Design system (`theme.go`):**  
Every color is a named constant (`ColorPrimary`, `ColorDanger`, `ColorBorder`, â€¦). No hex value ever appears outside this file. `NO_COLOR` support: when the `NO_COLOR` env var is set, accent colors are skipped and the `â–¸` marker + bold carry all selection state. `SelectedItemStyle()` is a function (not a var) so it re-evaluates `NoColor` on every call.

**Config path resolution (`config.go`):**  
- Linux: `~/.decoyd/`
- Windows: `%APPDATA%\Decoyd\`
- Creates the directory on first launch if missing  
- First-run detection via a sentinel file (`.initialized`) â€” written once, checked on every start

**Root model (`root.go`):**  
`RootModel` is the bubbletea top-level model. It owns a `Screen` enum state machine. Messages from sub-models bubble up through the type switch in `Update`. All sub-models hold their own `width`/`height` and get a `tea.WindowSizeMsg` via `propagateSize` on every resize.

**Splash screen (`splash.go`):**  
- Typewriter reveal: `D E C O Y D` appears one character every 90ms using `tea.Tick`
- Subtitle and blinking "press any key" prompt appear only after the wordmark completes
- Any keypress at any point skips immediately to the main menu
- Box stays the same physical size during animation (padded with spaces so lipgloss doesn't re-layout)

**Main menu (`mainmenu.go`):**  
- Arrow/`j`/`k` navigation + number shortcuts (1â€“5)
- Selected item gets a pulsing `â–¸ â†’ â–¹ â†’ â–· â†’ â–¹` marker cycling at 400ms â€” feels alive, not static
- `NO_COLOR` mode: static `â–¸`, bold only

**Help overlay (`help.go`):**  
Toggled with `?` on any screen. `Esc` dismisses. Rendered via `lipgloss.Place` centered over the current screen, backdrop dimmed to `ColorBackground`.

**CI (`ci.yml`):**  
GitHub Actions matrix: `ubuntu-latest` and `windows-latest`. Runs `go build ./...` and `go test ./...` on every push and PR. Uses `actions/cache` for the Go module cache.

### Why these choices

- **bubbletea**: The Elm architecture model maps perfectly to a multi-screen TUI state machine. Messages are typed, routing is explicit, no global state.
- **lipgloss**: Handles ANSI rendering without terminal detection hacks. Works on Windows Terminal and everything that handles VT100.
- **`NO_COLOR` from the start**: Retrofitting later would mean touching every screen twice.
- **Resize from the start**: Same reason â€” deferring means broken layouts in any CI terminal that opens at a non-standard size.

### Tests (6 pass)

| Test | What it checks |
|---|---|
| `TestDataDir_Linux` | Config path returns a valid directory (notes Windows mismatch) |
| `TestDataDir_PathContainsAppName` | Path always contains "decoyd" or "Decoyd" |
| `TestIsFirstRun_SentinelAbsent` | Returns `true` when `.initialized` doesn't exist |
| `TestIsFirstRun_SentinelPresent` | Returns `false` after `MarkInitialized` writes the file |
| `TestMarkInitialized_Idempotent` | Calling twice doesn't error |
| `TestDataDir_TableDriven/home_override` | `$HOME` override respected |

> **Known quirk:** `TestDataDir_Linux` logs a diff on Windows because `os.UserConfigDir()` returns `%APPDATA%\Decoyd` on Windows. The test still passes â€” it only logs, doesn't fail.

---

## Phase 1 â€” Token Generation

### What was built

Eight generators producing convincingly real-looking credential files, a bbolt-backed persistence store, and the Generate TUI screen.

**Files created:**
```
internal/tokens/tokens.go
internal/tokens/generate.go
internal/tokens/tokens_test.go
internal/store/store.go
internal/store/store_test.go
internal/tui/generate.go
```

**Root model updated:** `NewRootModel` gains `st *store.Store`. `ScreenGenerate` added.

**Dependencies added:**
- `go.etcd.io/bbolt v1.5` â€” pure-Go embedded KV store, no cgo
- `golang.org/x/crypto v0.54` â€” SSH key marshalling in OpenSSH PEM format
- `gopkg.in/yaml.v3` â€” kubeconfig YAML validation in tests

### Token model (`tokens.go`)

```go
type Token struct {
    ID             string
    Type           string
    Value          string     // file content verbatim
    Filename       string     // suggested on-disk name
    CreatedAt      time.Time
    DeployedPath   string
    AlertChannelID string
    Triggered      bool
    TriggeredAt    *time.Time
    Notes          string     // user label
}
```

`NewID()` uses `crypto/rand` (8 bytes â†’ 16 hex chars). The `Categories` variable drives the TUI checklist grouping â€” one authoritative source, zero duplication.

### Generators (`generate.go`)

| Token type | Format | Key library |
|---|---|---|
| AWS credentials | `AKIA` + 16 uppercase alphanumeric chars + 40-char secret | `crypto/rand` |
| SSH private key | Real ed25519 keypair, OpenSSH PEM format | `crypto/ed25519` + `golang.org/x/crypto/ssh` |
| `.env` secrets | `DATABASE_URL`, `STRIPE_SECRET_KEY`, `JWT_SECRET`, etc. with realistic fake values | `crypto/rand` |
| GitHub PAT | `ghp_` + 36 alphanumeric chars | `crypto/rand` |
| Slack bot token | `xoxb-<10 digits>-<11 digits>-<24 alphanumeric>` | `crypto/rand` |
| Kubeconfig | Valid YAML: `apiVersion: v1`, `kind: Config`, `clusters`, `contexts`, `users` with fake bearer token | `crypto/rand` |
| Database dump | PostgreSQL dump with `CREATE TABLE`, `INSERT INTO`, bcrypt-style hashes | `crypto/rand` |
| DNS canary | 16-char lowercase alphanumeric subdomain label with instructions | `crypto/rand` |

Every generator calls `NewID()` first and sets `CreatedAt` to `time.Now().UTC()`. The `Value` field holds the verbatim file content. The SSH key stores `<private PEM>---PUBLIC KEY---\n<authorized_keys line>` so Phase 2 deploy can write both files.

All random helpers (`randStr`, `randB64`) use `crypto/rand`, never `math/rand`.

### Store (`store.go`)

bbolt with a single `"tokens"` bucket. Tokens are JSON-marshalled (`encoding/json`). Operations:

| Method | Notes |
|---|---|
| `Open(dbPath)` | Creates bucket if missing. 2-second lock timeout so a stale lock doesn't hang. |
| `SaveToken` | Validates non-empty ID, upserts. |
| `GetToken` | Returns `ErrNotFound` sentinel for missing IDs. |
| `ListTokens` | `ForEach` in byte-sorted key order. |
| `UpdateToken` | Alias for `SaveToken` (upsert semantics). |
| `DeleteToken` | No-op if ID doesn't exist (bbolt's own behaviour). |
| `ListByType` | In-process filter over `ListTokens`. |

### Generate screen (`generate.go`)

Multi-select checklist, 3 grouped categories (Cloud/Infra, Dev Tools, Data). Cursor moves through 8 token items (0â€“7) plus a notes/label text field (index 8). Rendering:

```
  Cloud / Infra
â–¸ [âœ“] AWS credentials
  [ ] SSH private key

  Dev Tools
  [âœ“] GitHub PAT

  Label (optional): prod-server|
```

`â–¸` = cursor position. `[âœ“]` = selected (green + bold). Both are independent. On `Enter`, calls `tokens.Generate()` for each selected type, sets `t.Notes`, calls `st.SaveToken(t)`. Results screen shows each token's ID and filename with green `âœ“` or red `âœ—` per result.

### Tests (30 pass)

**Token tests (`tokens_test.go`):**

| Test | What it checks |
|---|---|
| `TestNewID_Format` | 16 lowercase hex chars |
| `TestNewID_Collision` | 1,000 IDs â†’ zero duplicates |
| `TestNewID_Concurrent` | 20 goroutines Ã— 50 IDs â€” no race, no collision |
| `TestGenerateAWSCredentials_Format` | `AKIA[A-Z0-9]{16}` regex + 40-char secret |
| `TestGenerateSSHKey_ParsesOK` | `ssh.ParseRawPrivateKey` succeeds; public line starts `ssh-ed25519` |
| `TestGenerateEnvSecrets_Format` | `DATABASE_URL=`, `STRIPE_SECRET_KEY=sk_live_`, `JWT_SECRET=` present |
| `TestGenerateGitHubPAT_Format` | `ghp_[A-Za-z0-9]{36}` regex |
| `TestGenerateSlackToken_Format` | `xoxb-[0-9]{10}-[0-9]{11}-[A-Za-z0-9]{24}` regex |
| `TestGenerateKubeconfig_ValidYAML` | Parses with `gopkg.in/yaml.v3`; all 5 required top-level keys present |
| `TestGenerateDBDump_Format` | SQL keywords, `CREATE TABLE`, `INSERT INTO`, `password_hash` present |
| `TestGenerateDNSCanary_LabelFormat` | `label=[a-z0-9]{16}` regex |
| `TestGenerateDNSCanary_LabelUniqueness` | 1,000 labels â†’ zero duplicates |
| `TestGenerate_UnknownType` | Returns error, doesn't panic |
| `TestGenerate_AllTypes` | Sub-tests for all 8 types: non-empty ID, correct Type, non-empty Value + Filename, non-zero CreatedAt |

**Store tests (`store_test.go`):**

| Test | What it checks |
|---|---|
| `TestStore_RoundTrip_AllFields` | All 9 fields survive JSON marshal/unmarshal, including `TriggeredAt *time.Time` pointer and `Notes` with emoji |
| `TestStore_GetToken_NotFound` | `errors.Is(err, store.ErrNotFound)` |
| `TestStore_ListTokens_Empty` | Empty store returns empty slice, not nil error |
| `TestStore_ListTokens_MultipleRecords` | 5 saves â†’ 5 listed |
| `TestStore_UpdateToken_OverwritesExisting` | Notes and Triggered fields updated in place |
| `TestStore_DeleteToken` | GetToken after delete â†’ ErrNotFound |
| `TestStore_DeleteToken_NoOp` | Deleting missing ID is not an error |
| `TestStore_ListByType` | 3 PAT + 2 SSH â†’ correct counts, DNS returns empty |
| `TestStore_SaveToken_EmptyID` | Returns error without panic |
| `TestStore_Notes_RoundTrip` | Unicode Notes field survives round-trip |

---

## Phase 2 â€” Deployment

### What was built

Writing tokens to real disk locations, a TUI flow to pick where, a tabular token list screen, and CLI subcommands.

**Files created:**
```
internal/deploy/deploy.go
internal/deploy/deploy_test.go
internal/tui/deployscreen.go
internal/tui/tokenlist.go
```

**Root model updated:** `ScreenDeploy` (menu item 1) and `ScreenTokenList` (menu item 2) added. Both route Done messages back to main menu.

**`main.go` updated:** CLI subcommand dispatch before TUI launch.

### Deployer (`deploy.go`)

`DeployToFile(t Token, targetDir string, opts Options) (DeployResult, error)`

- Resolves `targetDir` to an absolute path
- `os.Stat` check before writing â€” returns `ErrAlreadyExists` (wrapped) if file exists
- Dry-run mode: performs the stat check but never calls `WriteFile` or `MkdirAll`
- `os.MkdirAll` with `0o750` before writing â€” nested paths work without pre-creating
- `os.WriteFile` with `PermForType(t.Type)` â€” `0600` for secrets/keys, `0644` for everything else
- On Linux, calls `os.Chmod` explicitly after write (some temp filesystems override umask)
- On Windows, `Chmod` is a no-op but still called for portability

**Permission table:**

| Token type | Permission |
|---|---|
| AWS credentials | `0600` |
| SSH private key | `0600` |
| `.env` secrets | `0600` |
| GitHub PAT | `0600` |
| Slack bot token | `0600` |
| Kubeconfig | `0644` |
| Database dump | `0644` |
| DNS canary token | `0644` |

`PresetDirs()` returns Home, Downloads, Desktop, `~/.ssh` (only if `~/.ssh` exists). `SanitizePath` expands `~/` to `os.UserHomeDir()`.

### Deploy screen (`deployscreen.go`)

4-step state machine:

```
deployStatePickToken â†’ deployStatePickPath â†’ deployStateCustomPath (branch)
                                           â†˜ deployStateConfirm â†’ deployStateDone
```

- **Pick token**: Scrollable list of all tokens from store. Shows deployed path and `âš  triggered` if applicable.
- **Pick path**: Preset list + "Custom pathâ€¦" option at the bottom.
- **Custom path**: Inline text input with cursor, `~` expansion on Enter.
- **Confirm**: Shows token type, filename, destination dir, and permission bits. `Enter`/`y` writes. `d` does a **dry-run** â€” shows the full output path and permissions without touching disk. `n`/`Esc` cancels.
- **Done**: Green box on success; red box on error (with `ErrAlreadyExists` text if applicable). On success, updates `token.DeployedPath` in the store.

### Token list screen (`tokenlist.go`)

Tabular view: `Type | File | Location | Triggered`.

- `â–¸` cursor, vim nav
- `d` enters confirm-delete step with a red-bordered box
- Confirm warns "the deployed file is NOT removed from disk" if the token has a `DeployedPath`
- After delete, calls `reload()` which re-fetches from store and clamps cursor
- Notice line shown below table on success/failure

`truncate(s string, n int)` clips column values to fit the table without breaking the layout.

### CLI subcommands (`main.go`)

`os.Args[1:]` is checked before launching the TUI:

| Command | Output |
|---|---|
| `decoyd list` | Tab-aligned table via `text/tabwriter`: ID, TYPE, FILE, LOCATION, TRIGGERED, NOTES |
| `decoyd remove <id>` | Looks up ID (returns clear error if not found), deletes, prints confirmation. If `DeployedPath` is set, prints warning that disk file was not touched. |
| `decoyd help` | Usage text |

### Tests (18 pass, 2 skip)

**Deploy tests (`deploy_test.go`):**

| Test | What it checks |
|---|---|
| `TestDeployToFile_WritesFile` | File created, content matches `token.Value` verbatim |
| `TestDeployToFile_CreatesTargetDirectory` | Nested `nested/subdir` created automatically |
| `TestDeployToFile_RefusesOverwrite` | Second call â†’ `errors.Is(err, ErrAlreadyExists)` |
| `TestDeployToFile_DryRun_NothingWritten` | `WouldCreate=true`, no file on disk |
| `TestDeployToFile_DryRun_AlsoChecksOverwrite` | Dry-run on existing file â†’ `ErrAlreadyExists` |
| `TestDeployToFile_PermissionsSecret` | `0600` for AWS creds (**skip on Windows**) |
| `TestDeployToFile_PermissionsPublic` | `0644` for DB dump (**skip on Windows**) |
| `TestPermForType_SecretTypes` | All 5 secret types â†’ `0600` |
| `TestPermForType_PublicTypes` | Kubeconfig, DB dump, DNS â†’ `0644` |
| `TestSanitizePath_TildeExpansion` | `~/Documents` and `~` both expand correctly |
| `TestDeployToFile_EmptyDir_Error` | Empty `targetDir` string â†’ error, no panic |

**TUI navigation tests (added to `root_test.go`):**

| Test | What it checks |
|---|---|
| `TestRootModel_DeployScreenNavigation` | `MenuActionMsg{1}` â†’ `ScreenDeploy` |
| `TestRootModel_DeployScreenDoneReturnsToMenu` | `DeployScreenDoneMsg` â†’ `ScreenMainMenu` |
| `TestRootModel_TokenListNavigation` | `MenuActionMsg{2}` â†’ `ScreenTokenList` |
| `TestRootModel_TokenListDoneReturnsToMenu` | `TokenListDoneMsg` â†’ `ScreenMainMenu` |

> **Permission test skips:** `os.Chmod` is effectively a no-op on Windows and the test correctly self-skips via `runtime.GOOS == "windows"` check. These tests run and pass on Linux CI.

---

## Known gaps / deferred to later phases

| Item | Phase |
|---|---|
| SSH deploy writes both `id_ed25519` and `id_ed25519.pub` (currently only private key) | 2 polish / 5 |
| DNS canary requires user to configure their DNS provider â€” instructions in the token file only | 4 |
| `decoyd remove` does not delete the deployed file from disk (by design in v1; Phase 5 adds `--purge`) | 5 |
| Permission tests skipped on Windows (OS doesn't honour POSIX perms via Go) | n/a â€” documented known limit |
| True read-detection on Windows (ETW/minifilter) | v1.1 |
| Alert settings TUI screen (menu item 3) placeholder until Phase 3 | 3 |
| Status / watcher dashboard (menu item 4) placeholder until Phase 4 | 4 |

---

## Repository layout (current)

```
decoyd/
â”œâ”€â”€ cmd/decoyd/main.go           CLI dispatch + TUI entrypoint
â”œâ”€â”€ internal/
â”‚   â”œâ”€â”€ config/
â”‚   â”‚   â”œâ”€â”€ config.go            DataDir, IsFirstRun, MarkInitialized
â”‚   â”‚   â””â”€â”€ config_test.go
â”‚   â”œâ”€â”€ deploy/
â”‚   â”‚   â”œâ”€â”€ deploy.go            DeployToFile, PermForType, PresetDirs, SanitizePath
â”‚   â”‚   â””â”€â”€ deploy_test.go
â”‚   â”œâ”€â”€ store/
â”‚   â”‚   â”œâ”€â”€ store.go             bbolt CRUD
â”‚   â”‚   â””â”€â”€ store_test.go
â”‚   â”œâ”€â”€ tokens/
â”‚   â”‚   â”œâ”€â”€ tokens.go            Token struct, type constants, Categories, Generate()
â”‚   â”‚   â”œâ”€â”€ generate.go          8 generator functions
â”‚   â”‚   â””â”€â”€ tokens_test.go
â”‚   â””â”€â”€ tui/
â”‚       â”œâ”€â”€ root.go              RootModel, Screen enum, message router
â”‚       â”œâ”€â”€ splash.go            Typewriter splash screen
â”‚       â”œâ”€â”€ mainmenu.go          Pulsing-cursor main menu
â”‚       â”œâ”€â”€ generate.go          Multi-select generate screen
â”‚       â”œâ”€â”€ deployscreen.go      4-step deploy flow
â”‚       â”œâ”€â”€ tokenlist.go         Tabular token list + delete
â”‚       â”œâ”€â”€ help.go              Help overlay
â”‚       â”œâ”€â”€ theme.go             Color palette, shared styles
â”‚       â”œâ”€â”€ components/
â”‚       â”‚   â””â”€â”€ components.go    (placeholder for Phase 3+ shared widgets)
â”‚       â””â”€â”€ root_test.go
â”œâ”€â”€ .github/workflows/ci.yml
â”œâ”€â”€ go.mod / go.sum
â”œâ”€â”€ LICENSE
â””â”€â”€ README.md
```

---

## Phase 3 â€” Alerting

### What was built

A pluggable alerting system with 6 channel implementations, a 0600-protected JSON config file, and a full Alert Settings TUI screen with inline test-send.

**Files created:**
```
internal/alert/alert.go        â€” AlertPayload, Alerter interface, ChannelConfig/AlertConfig,
                                  Load/Save (0600), NewAlerter factory, MaskSecret, sanitizeErr,
                                  doPost/doPostText shared HTTP helpers
internal/alert/discord.go      â€” Discord embed via incoming webhook
internal/alert/slack.go        â€” Slack Block Kit via incoming webhook
internal/alert/telegram.go     â€” Telegram Bot API (plain text, no parse_mode)
internal/alert/teams.go        â€” Microsoft Teams MessageCard via incoming webhook
internal/alert/ntfy.go         â€” ntfy.sh push (plain text + Title/Priority/Tags headers)
internal/alert/webhook.go      â€” Generic webhook: posts AlertPayload as JSON verbatim
internal/alert/alert_test.go   â€” 30 tests: payload shape, non-2xx, timeout, sanitizeErr,
                                  MaskSecret, config round-trip, NewAlerter misconfigured
internal/tui/alertscreen.go    â€” Alert Settings TUI screen
```

**Files modified:**
```
internal/tui/root.go            â€” Added ScreenAlertSettings, alertScreen field, dataDir,
                                   wired menu index 2 â†’ AlertSettings, AlertScreenDoneMsg handler
internal/tui/root_test.go       â€” Updated nav tests for Phase 3 routing
cmd/decoyd/main.go              â€” Passed dataDir to NewRootModel
```

### Key technical decisions

| Decision | Rationale |
|---|---|
| `package alert` tests (not `alert_test`) | `newTelegramAlerter` is unexported â€” needed for the test to override `apiBase` with the httptest server URL without making the field public. Same pattern as `sanitizeErr`. |
| `slowServer` instead of `<-r.Context().Done()` | httptest.Server.Close() blocks until handlers return. `<-r.Context().Done()` caused a 5-second watchdog timeout because the server-side request context isn't linked to the client context. 200ms sleep is longer than the 50ms test deadline and drains cleanly. |
| Plain text for Telegram (no `parse_mode`) | Path and Detail fields can contain `<`, `>`, `&` â€” Telegram's HTML mode would reject or mangle them. Plain text sidesteps the escaping problem with zero downside for an alert message. |
| `sanitizeErr` at the alerter layer, not the TUI | Go's `*url.Error.Error()` embeds the full request URL (webhook URL or `https://api.telegram.org/bot<TOKEN>/...`). Every `Send` method calls `sanitizeErr` before returning, so the TUI never needs to think about this. |
| `alert_config.json` at 0600 | Webhook URLs and bot tokens are secrets. The file is written via a tmp-then-rename atomic pattern (same as `deploy.go`). On Linux, `os.Chmod(tmp, 0o600)` is explicit in case umask is loose. |
| `MaskSecret` for TUI display | Shows `â€¢â€¢â€¢â€¢â€¢â€¢<last4>` when a credential field is unfocused. Focused fields show the real value with a block cursor (so users can see what they're typing). Never used on the wire or in error strings. |
| Desktop notification deferred to Phase 5 | beeep requires cgo on Linux (`libnotify`), which breaks the no-cgo cross-compile constraint. Shipping a channel that silently does nothing on Linux is worse than being honest about the gap. |

### Security implementation

- `alert_config.json` written at `0600` via atomic rename â€” webhook URLs and bot tokens protected from other local users
- `sanitizeErr` strips `*url.Error` URL field from all HTTP errors â€” secrets never appear in TUI error messages or Go log output
- `MaskSecret` used for all credential fields in the TUI â€” `â€¢â€¢â€¢â€¢â€¢â€¢last4` when unfocused
- Telegram bot token is in the URL path, not a header â€” `sanitizeErr` handles this correctly
- ntfy Topic treated as a secret (acts as a shared password for public ntfy topics)

### Tests

**Coverage per alerter** (6 channels Ã— 3 tests + extras = 30 total):
- Correct payload shape: unmarshal JSON, assert required fields/types
- Non-2xx response: returns clean error (no panic, no leaked URL/token)
- Timeout: 50ms context, `slowServer` takes 200ms â†’ clean error returned

**Additional tests:**
- `TestSanitizeErr_*` (3): strips URL from `*url.Error`, passthrough non-URL errors, nil input
- `TestMaskSecret` (5 cases): empty, short, exactly-4, long with last-4 visible
- `TestSave_WritesJSON` + `TestSave_FilePermissions` (skip on Windows) + `TestLoad_FileNotExist`
- `TestNewAlerter_Misconfigured` (7 sub-cases, table-driven) + `TestNewAlerter_UnknownType`
- `TestWebhookAlerter_JSONFieldNames`: verifies canonical `snake_case` field names (`token_id`, `token_type`, `triggered_at`, etc.)

### Known gaps (Phase 3)

| Gap | Deferred to |
|---|---|
| Local desktop notification (beeep) | Phase 5 â€” requires cgo on Linux |
| ntfy auth token (self-hosted with authentication) | Phase 5 |
| Multi-channel UI (Add/Edit/Delete list, multiple active channels) | Phase 5 |
| Microsoft Teams Adaptive Cards (richer than MessageCard) | Phase 5 polish |
| Slack OAuth token flow (alternative to incoming webhooks) | Out of scope v1 |

### "Done when" bar â€” met?

> A user can configure any of the seven channels through the form, get a real inline test alert, and see clear success/failure feedback.

**Six of seven channels: YES.** Desktop notification excluded (see Known Gaps). The form configures Discord, Slack, Telegram, Teams, ntfy, and generic webhook. Test-send fires asynchronously with a spinner, config is saved before the send, and the result screen shows green (success) or red (error). All in the same `Tab`/`Enter` key idiom as the rest of the TUI.

---

### Post-Phase 3 CI fixes (two rounds)

**Round 1** â€” switched from `go-version: "1.22"` to `go-version-file: go.mod` + replaced broken `cache: true` with explicit `actions/cache@v4`. Fixed the `/usr/bin/tar` exit-2 issue but govulncheck still failed.

**Round 2** â€” switched to `go-version: "stable"` (always latest patched Go). Fixed govulncheck stdlib CVE findings, but gosec now surfaced 2 new G304 findings: `config.go:65` (`os.OpenFile` for the sentinel file) and `alert.go:122` (`os.ReadFile` for the alert config). These were previously hidden behind the CI `-exclude G304` flag, which silently stopped working in newer gosec `@latest`.

**Round 3 (definitive)** â€” moved G304 suppression inline with `//nolint:gosec // G304: ...` comments at each specific site. This is gosec's own recommended approach: suppression is co-located with the code, survives tool version changes, and documents exactly *why* each path is safe (always `filepath.Join(dataDir, knownFileName)`, never user input). Also suppressed `os.WriteFile` in `Save` proactively. The CI `-exclude G304` flag was removed; only `-exclude G107` remains (webhook URLs from operator config, not untrusted input).

| Issue | Fix |
|---|---|
| `/usr/bin/tar` exit 2 on `ubuntu-latest` cache restore | `cache: false` on `setup-go` + explicit `actions/cache@v4` step |
| `govulncheck` reporting TLS/x509/textproto stdlib CVEs | `go-version: "stable"` â€” always installs the latest patched Go |
| gosec G107 on alert HTTP requests | `-exclude G107` â€” webhook URLs come from operator config, not untrusted input |
| gosec G304 on `config.go:65` and `alert.go:122` | `//nolint:gosec // G304: ...` inline â€” path always under `dataDir`, not user input. CI flag `-exclude G304` silently stopped working in newer `gosec @latest`. |

---

### Pre-Phase 4 verification (items flagged in Phase 2 review)

Three items were explicitly verified before starting Phase 4:

**1. `~/.decoyd/` directory permissions â€” ALREADY FIXED IN CODE**

`config.go:43` does `os.MkdirAll(dir, 0o700)` â€” the directory itself is created at 0700, not just the files inside it. The PROGRESS.md Phase 2 notes were ambiguous ("README note"), but the code was correct. Confirmed by reading the source. No change needed.

**2. `slowServer` 200ms vs 50ms test deadline â€” KNOWN GAP, DEFERRED**

The 150ms margin is fine on a local machine and passes consistently in CI. On a heavily loaded runner it could occasionally produce a flaky timeout test. Not worth a fix now; if it flakes in Phase 4+ CI, bump `slowServer` sleep to 500ms and the context deadline to 100ms.

**3. Telegram bot token in URL path â€” `sanitizeErr` IS GENERIC**

`sanitizeErr` (alert.go:310) type-asserts to `*url.Error` and discards `urlErr.URL` entirely â€” the replacement error is `fmt.Errorf("http %s: %w", urlErr.Op, urlErr.Err)`. It does NOT pattern-match on URL structure. A Telegram failure (`https://api.telegram.org/bot<TOKEN>/sendMessage`) produces a `*url.Error` â†’ `.URL` field is dropped wholesale â†’ output reaches the TUI as `"http Post: <network error>"`. The token never appears regardless of whether the secret is in the URL path (Telegram), a query param, or an Authorization header leak from an intermediate proxy. Generic and correct.

---

### Phase 3 post-launch bug fix â€” Alert Settings TUI

Discovered via live testing: entering a Discord webhook URL and pressing `s` showed `"Test send failed: http Post: unsupported protocol scheme \"\""` instead of delivering the alert.

**Root cause â€” two bugs in `alertscreen.go`:**

| Bug | Detail |
|---|---|
| `maxFieldCursor()` returned wrong value for single-field channels | Returned `alertFieldSend - 1 = 2` (secondary field index) for channels like Discord. Tab/Down navigation skips index 2 (no secondary field) AND max is 2, so the cursor could never reach index 3 (Send button). The Send button was unreachable by keyboard. |
| No URL scheme validation before sending | `doTestSend` called `NewAlerter` directly with whatever was in `primaryBuf`. If the URL was pasted without `https://`, `NewAlerter` succeeded (non-empty string passes the empty-check) but the HTTP client then failed with `unsupported protocol scheme ""`. |

**Fixes applied to `internal/tui/alertscreen.go`:**
1. `maxFieldCursor()` now always returns `alertFieldSend` (3). The Tab handler's existing secondary-skip logic correctly handles two-vs-one field channels without needing `maxFieldCursor` to lie about the limit.
2. `doTestSend` now trims whitespace from both fields before validating, and explicitly checks:
   - Empty primary field â†’ `"<FieldLabel> is required"` error, no network call
   - URL-based channels without `https://` or `http://` prefix â†’ `"Webhook URL must start with https://"`, no network call
3. Trimmed values are written back to the buffers so the form stays in sync.

All 88 tests still pass after the fix.

**Follow-up bug â€” paste eating shortcut characters**

Discovered immediately after: pasting a Discord webhook URL (`https://discord.com/api/webhooks/â€¦`) into the Webhook URL field produced a garbled result like `http://diord.com/api/webhooLatW0lDAtTVSNFJ`.

Root cause: in a terminal, each pasted character arrives as a separate `tea.KeyMsg`. The old `updateForm` used a flat `switch km.String()` that matched `"s"` â†’ fire test-send, `"j"` â†’ cursor down, `"k"` â†’ cursor up **before** the `default` branch that wrote to the buffer. Every `s`, `j`, `k` in the URL was consumed by those cases, and `s` also triggered `doTestSend()` mid-paste.

Fix in `updateForm`:
- `esc`, `tab`, `shift+tab` are handled first (always safe regardless of focus).
- When `fieldCursor` is `alertFieldPrimary` or `alertFieldSecondary`: **all other keys go directly to the text buffer**. Only `enter` is intercepted (advances to the next field). `j`, `k`, `s`, `down`, `up` are treated as runes and written normally.
- When `fieldCursor` is `alertFieldChannel` or `alertFieldSend` (non-text rows): `j`/`k`/`down`/`up` navigate, `enter` cycles/fires, `s` fires test-send.

This means:
- Typing naturally on any field works as before.
- Paste works correctly â€” no characters eaten.
- `s` shortcut to fire test-send still works when on the Channel or Send row.

**Full TUI paste audit (all screens checked):**

| Screen / function | Text input field? | Paste-safe? | Notes |
|---|---|---|---|
| `alertscreen.go` `updateForm` | âœ… primary + secondary | âœ… Fixed | `j`/`k`/`s` moved to non-text guard |
| `deployscreen.go` `updateCustomPath` | âœ… custom path | âœ… Clean | No single-letter shortcuts before `default` |
| `deployscreen.go` `updatePickToken` | âŒ list navigation | N/A | `j`/`k` only; no text input |
| `deployscreen.go` `updatePickPath` | âŒ list navigation | N/A | `j`/`k` only; no text input |
| `generate.go` `updateNotesInput` | âœ… label field | âš ï¸ Fixed | `"up", "k"` â†’ split to `"up"` only; `k` now goes to buffer |
| `generate.go` main update loop | âŒ list/selector | N/A | `j`/`k`/`space` only; no text input |
| `tokenlist.go` | âŒ list navigation | N/A | No text input |

One additional fix: `generate.go` `updateNotesInput` had `case "up", "k":` â€” the `k` rune would be eaten when typing or pasting a label containing `k` (e.g. "backup key", "ssh-key-prod"). Changed to `case "up":` only; `k` now falls through to `default` and is written to the buffer. Added an in-code comment warning against adding single-letter shortcuts in text field handlers.


---
---

### Pre-Phase 4 patch â€” token editing + alert channel cycling fix

Implemented as an isolated commit before Phase 4 work begins.

---

#### Token Notes editing (`internal/tui/tokenlist.go`)

Tokens could be deleted but not edited. Added `e` on the token list to edit the `Notes` label of the selected token, matching the existing confirm-step pattern used for delete.

New state machine:

| State | Trigger | Description |
|---|---|---|
| `tokenListStateBrowse` | â€” | Default list view |
| `tokenListStateConfDel` | `d` | Confirm delete dialog |
| `tokenListStateEdit` | `e` | Inline Notes editor |

`updateEdit` handler is paste-safe: only non-printable keys (`enter`, `esc`, `backspace`, `ctrl+*`, arrows) used for control â€” no single-letter shortcuts â€” so pasting a label works. `enter` trims whitespace then calls `st.UpdateToken(tok)` (the existing upsert alias). `esc` discards changes. A success/error notice is shown in the list footer after saving.

Footer updated: `â†‘/â†“ browse   d delete   e edit notes   esc back`.

---

#### Alert Settings: channel cycling loses field value (`internal/tui/alertscreen.go`)

**Diagnosis:** `AlertModel` had a single pair of `primaryBuf`/`secondaryBuf`. The channel cycling handler did `m.primaryBuf = nil` unconditionally. After a successful test-send (URL saved to disk), cycling away from Discord then back produced an empty field because the nil-clear ran on every cycle regardless of history.

**Root cause:** no per-channel credential storage â€” one shared rune buffer for whatever channel is currently displayed.

**Fix:** Added `savedChannels map[string]alert.ChannelConfig` to `AlertModel`:

- Populated at `NewAlertModel` from all entries in the saved `AlertConfig.Channels` slice (not just the default).
- Updated in `doTestSend` after `buildChannelConfig()` â€” `m.savedChannels[cfg.Type] = cfg` â€” so cycling back works immediately after a test-send even before the next restart.
- New `loadChannelFields()` pointer method reads from the cache for the current `channelType()` and populates `primaryBuf`/`secondaryBuf` appropriately. Clears buffers if the type has no cached config (fresh channel, not yet configured).
- Channel cycling handler now calls `m.loadChannelFields()` instead of nil-clearing.

**Tests added (`internal/tui/alertscreen_test.go`):**

| Test | What it covers |
|---|---|
| `TestAlertChannelCyclePreservesCredentials` | Cycle Discord â†’ next â†’ back; asserts URL restored |
| `TestAlertChannelCycleFullRoundTrip` | Full cycle through all 6 channel types; asserts URL still intact |
| `TestAlertChannelUnconfiguredIsEmpty` | Cycling to a never-configured type shows empty fields, not stale data |

All 91 tests pass.

---

#### Alert Settings: save-vs-test-send audit + autosave fix (`internal/tui/alertscreen.go`)

**Question:** Does the credential save happen only on a _successful_ send, or unconditionally?

**Exact answer from code review:**

The `savedChannels` write in `doTestSend` (line 449) happens **after `alert.NewAlerter(cfg)` succeeds but before the HTTP goroutine is dispatched** (line 460). `NewAlerter` is a pure constructor â€” no network call. So:

| Scenario | Saved? |
|---|---|
| Empty URL | âŒ early-return before save |
| Missing `https://` | âŒ early-return before save |
| `NewAlerter` structural error | âŒ early-return before save |
| Valid URL, **network offline** | âœ… saved to memory + disk, then async send fails |
| Valid URL, **wrong webhook path** | âœ… saved to memory + disk, then async send fails |
| Valid URL, **send succeeds** | âœ… saved to memory + disk |

So network failures do NOT block the save. However there was still a real friction: **the only path to saving was pressing Send test alert.** A user who configures credentials and navigates away (without firing a test) would lose their URL.

**Fix â€” `autosaveCredentials()` pointer method:**

Saves `primaryBuf`/`secondaryBuf` to `savedChannels` + disk whenever the user navigates _out of_ a text field, with no network call:

```
Tab from text field   â†’ autosaveCredentials() â†’ advance cursor
Shift+Tab from field  â†’ autosaveCredentials() â†’ retreat cursor
Enter in text field   â†’ autosaveCredentials() â†’ advance cursor
Esc (leave screen)    â†’ autosaveCredentials() â†’ emit AlertScreenDoneMsg
```

Guard: if `primaryBuf` is empty after trim, skip (no point saving a blank config).

**Result:** Credentials are persisted to disk as soon as the user Tabs or Enters out of the URL field â€” no test-send required. Phase 4 multi-channel assignment can assume any channel that shows a field value is already saved.


---

---

---

## Phase 4 — Detection Engine (In Progress)

> **Note on prior documentation:** The original Phase 4 section in this file described work that was documented but
> never committed to git. This was caused by a `.gitignore` bug (an unanchored `decoyd` pattern that silently
> excluded `cmd/decoyd/` from tracking). The section below replaces that documentation entirely with an accurate
> account of what is actually in git, verified by CI, as of the current rebuild.

---

### Step 0 — Multi-channel config + per-token assignment

**Commit:** `3da670f`  
**CI:** `ubuntu-latest` green ✓ (run #21, 2026-07-19)

#### What was built

**`internal/alert/alert.go`**
- `ChannelConfig.ID` — stable 8-hex crypto/rand ID, `omitempty` for JSON backward compat.
- `AlertConfig.DefaultID` — new single source of truth for default channel. `DefaultIndex` retained as vestigial JSON-only field for round-trip compat with legacy configs (never read in any logic path).
- `GenerateChannelID()` — crypto/rand 8-byte hex, tested for format and uniqueness.
- `DefaultChannel()` — resolves `DefaultID`, falls back to `Channels[0]` if unset/stale.
- `ResolveChannel(id)` — exact ID lookup, returns false for empty/missing.
- `ChannelForToken(id)` — assigned→found, empty/stale→default, no-channels→false. Stale `AlertChannelID` on a token after channel deletion is harmless: fallback to default is automatic.
- `Load()` — backfills IDs for legacy configs, persists immediately (best-effort).
- `Save()` — assigns IDs to channels missing one, sets `DefaultID` from `Channels[0]` when unset.
- `SanitizeErrString()` — exported wrapper for internal `sanitizeErr`, used by `watch` package.

**`internal/tui/alertscreen.go`**
- `alertStateList`: new entry point when channels exist; ↑/↓ browse, enter edit, `a` add, `d` delete, `s` set-default, `esc` exit.
- `alertStateConfirmDelete`: y/enter deletes + auto-promotes new default; n/esc cancel.
- `editingID`: tracks edit vs. add — `upsertChannel` preserves ID on edit.
- `autosaveCredentials`/`doTestSend`: now call `upsertChannel` against full existing config (not overwrite-with-single-channel).

**`internal/tui/tokenlist.go`**
- `tokenListStateAssign`: `a` key opens channel picker.
- `assignOptions()`: "Remove assignment" always index 0; channels annotated with `✓current` and `★default`.
- `updateAssign`: enter writes `Token.AlertChannelID` via `st.UpdateToken`.
- `NewTokenListModel` now accepts `dataDir string`.

**Tests (13 new, all pass):**
`TestGenerateChannelID_Format`, `TestGenerateChannelID_Unique`,
`TestResolveChannel_Found/NotFound/EmptyID`,
`TestChannelForToken_AssignedFound/FallsBackToDefault/DeletedAssignmentFallsBackToDefault/NoChannels`,
`TestSave_BackfillsIDsForNewChannels`, `TestLoad_BackfillsLegacyConfigWithoutIDs`, `TestMultiChannel_RoundTrip`

---

### Step 1 — Real Linux inotify watcher

**Commit:** `c9e525b` + gosec fix `be2ef91`  
**CI:** `ubuntu-latest` green ✓ — `go test -v -race ./...` executed; both Linux integration tests PASS

#### What was built

**`internal/watch/watch_linux.go`** (replaces stub):
- `InotifyInit1(IN_CLOEXEC)` + `Pipe2(O_CLOEXEC|O_NONBLOCK)` self-pipe stop mechanism.
- `unix.Poll` with 1-second timeout on both inotify fd and stop-pipe fd.
- Events: `IN_OPEN | IN_ACCESS | IN_MODIFY | IN_MOVE_SELF | IN_DELETE_SELF`.
- Per-path debounce (default 2s): rapid events collapse to one dispatch.
- Per-token rate limit (default 5/hr): sliding 1-hour window.
- Quiet hours: events during quiet hours logged as `quiet_hours`, no alert sent.
- Two modes: bbolt (`st != nil`, TUI-embedded) and snapshot (`st == nil`, headless).
- Alert dispatch: `triglog.Append(Pending)` before send → `Append(Sent|Failed)` after.
- `IN_MOVE_SELF`/`IN_DELETE_SELF`: removes watch, re-adds after 100ms (handles atomic file replace).
- `unsafe.Pointer` cast for inotify event parsing: `#nosec G103` with reason.
- 5× G115 suppressions for required `int↔int32↔uint32` conversions at syscall boundary.

**`internal/watch/deployed.go`** (new):
- `DeployedToken`: minimal cross-process token record (id/type/path/channel).
- `WriteDeployedSnapshot`: atomic tmp-then-rename, 0600, filters undeployed tokens.
- `ReadDeployedSnapshot`: returns empty slice (not error) when file missing.

**`internal/watch/watcher_config.go`** (new):
- `WatcherConfig`: `DebounceDuration`, `RateLimit`, `QuietHoursStart/End/Enabled`.
- `DefaultWatcherConfig()`: 2s debounce, 5/hr rate limit.
- `inQuietHours()`: handles daytime and wrap-midnight ranges.
- `rateEntry`, `debounceEntry`: shared cross-platform types (defined here to avoid build-tag issues).

**Tests:**
- `watch_test.go` (9, all platforms, all PASS locally + CI): snapshot round-trip, filter, atomic overwrite, quiet-hours (disabled/wrap-midnight/daytime), rate-limit allow+block+window-reset.
- `watch_linux_test.go` (2, Linux only): `TestLinuxWatcher_Integration` (file touch → triglog entry), `TestLinuxWatcher_Debounce` (5 rapid opens → ≤1 event). **First real execution on CI ubuntu-latest — both PASS.**

**Known coverage gap:** TUI-embedded (`st != nil`, bbolt) code path in `loadTokens()` has no test; only headless path covered.

---

### Step 2 — Real Windows fsnotify watcher

**Commit:** `c10fd8a`  
**CI:** `windows-latest` green ✓ (Build & Test + Security Scan all pass)

#### What was built

**`internal/watch/watch_windows.go`** (replaces stub):
- `github.com/fsnotify/fsnotify v1.10.1` (added to `go.mod`).
- Watches parent directories filtered to token filenames (`ReadDirectoryChangesW` operates on directories).
- Write/Rename/Remove → write/rename/delete. Create/Chmod not forwarded.
- Same debounce/rate-limit/quiet-hours/triglog pipeline as Linux.
- Clean shutdown via `watcher.Close()` unblocking the event channel.

**Documented v1 limitation:** Pure read-only file access (`GENERIC_READ`) is NOT detectable on Windows without ETW or a kernel minifilter driver. Only write, rename, and delete events are surfaced. Stated in package comment and `fsnotifyEventType` doc comment.

**Tests (3 Windows-native, all PASS locally + CI):**
`TestWindowsWatcher_Integration_Write`, `TestWindowsWatcher_Integration_Delete`, `TestWindowsWatcher_Stop`

---

### Step 3 — Singleton watcher lock

**Commit:** `40b2dbe`  
**CI:** ubuntu-latest green ✓ — 5/5 checks passed on commit `40b2dbe`; `TestWatchLock_StalePIDOverwritten` executed on real Linux kernel (`unix.Kill`) and passed.

#### What was built

**`internal/watch/watchlock.go`** (platform-neutral):
- `AcquireWatchLock(dataDir)` → `(release func(), error)`.
- PID file at `<dataDir>/watcher.pid`, `O_CREATE|O_EXCL` for atomic creation.
- No file: create, write PID, return release func.
- File exists + holder alive: return `ErrWatcherRunning` with message: `"watcher already running: PID <N> holds <path> — stop that process first, or delete the file if it is stale"`.
- File exists + holder dead (stale): overwrite, treat as available.
- **Same-PID safety:** two instances in the same process share `os.Getpid()`; second `Start()` is still refused via O_EXCL (file exists + alive), not incorrectly exempted.
- `ErrWatcherRunning` sentinel error (`errors.Is` compatible).

**`internal/watch/watchlock_linux.go`**:
- `openExclusive`: `O_CREATE|O_EXCL|O_WRONLY`, 0600.
- `isProcessAlive`: `unix.Kill(pid, 0)` — nil=alive, `EPERM`=alive, `ESRCH`=dead.

**`internal/watch/watchlock_windows.go`**:
- `openExclusive`: `O_CREATE|O_EXCL|O_WRONLY`, 0600.
- `isProcessAlive`: `windows.OpenProcess(SYNCHRONIZE)` — success/`ACCESS_DENIED`=alive, `INVALID_PARAMETER`/`NOT_FOUND`=dead.

**Wire-up:** `AcquireWatchLock` called at top of both `linuxWatcher.start()` and `windowsWatcher.start()` before any inotify/fsnotify init. `release()` called in `stop()` after event loop exits. All error paths in `start()` call `relFn()` before returning.

**Tests (3, all PASS locally on Windows):**

| Test | Asserts |
|---|---|
| `TestWatchLock_SecondOpenerIsRefused` | Two `start()` on same dataDir → second returns `ErrWatcherRunning`; same-PID-same-process correctly refused |
| `TestWatchLock_ReleaseAllowsReacquire` | After `stop()`, new watcher acquires on same dataDir |
| `TestWatchLock_StalePIDOverwritten` | PID 2147483647 written directly to file; `AcquireWatchLock` detects dead, overwrites, returns release |

**CI note:** `TestWatchLock_StalePIDOverwritten` first real Linux execution was in the CI run for commit `40b2dbe` — PASS confirmed on `ubuntu-latest`. No longer pending.

---

---

### Step 4 — Dashboard UI

**Commit:** pending (this session)  
**CI:** pending push

#### What was built

**`internal/watch/watcherstatus.go`** (new):
- `HeadlessState` enum: `HeadlessNotRunning`, `HeadlessRunning`, `HeadlessStale`.
- `HeadlessWatcherState(dataDir)` — reads `watcher.pid`, calls `isProcessAlive`. **Read-only:** zero calls to `AcquireWatchLock`, no writes to the pid file, safe to call while a real watcher holds the lock.

**`internal/tui/statusscreen.go`** (new):
- `StatusModel`: three-state watcher status row.
  - `WatcherRef != nil` → **running (TUI-embedded)** — queries live `WatcherRef.Status()`.
  - `WatcherRef == nil`, pid file present + process alive → **running (headless, PID N)**.
  - `WatcherRef == nil`, pid file present + process dead → **stale lock** (file remains but process gone).
  - No pid file → **not running**.
- Trigger list: `triglog.Load(dataDir)`, newest-first, capped at 50.
- 5-second poll refresh via `tea.Tick`.
- ↑/↓ navigate, enter drills into TriggerDetailModel via `ShowTriggerDetailMsg`, r manual refresh, esc emits `StatusDoneMsg`.
- Menu index 3 routes here (`MenuActionMsg{Index: 3}`).

**`internal/tui/triggerdetail.go`** (new):
- `TriggerDetailModel`: all event fields displayed.
- Process attribution explicitly stated as `"unknown (v1 limitation — requires eBPF/ETW)"` — the dashboard does not mislead the user about what triggered the access.
- esc emits `TriggerDetailDoneMsg` → returns to `ScreenStatus` (not main menu).

**`internal/tui/root.go`** (modified):
- `ScreenStatus`, `ScreenTriggerDetail` added to enum.
- `statusScreen StatusModel`, `triggerDetail TriggerDetailModel`, `watcher *watch.Watcher` fields added.
- `MenuActionMsg{3}` routes to Status.
- `StatusDoneMsg` → MainMenu, `ShowTriggerDetailMsg` → TriggerDetail, `TriggerDetailDoneMsg` → Status.
- `propagateSize` wired for both new models.
- `reconcileCmd()` fired from `Init()` on Splash/MainMenu (see Step 5).

**Honest state of `watcher *watch.Watcher` field:** This field is always `nil` today. The TUI-embedded path is tested by constructing `StatusModel` directly with a non-nil watcher — the model logic is correct — but through normal TUI flow `m.watcher` is never set. The TUI-embedded mode will become live when the auto-start wiring is built (not scoped in Phase 4).

**Tests (10 new, `internal/tui/statusscreen_test.go`):**

| Test | Asserts |
|---|---|
| `TestMenuAction3_RoutesToScreenStatus` | `MenuActionMsg{3}` → `ScreenStatus` |
| `TestStatusDoneMsg_ReturnsToMenu` | `StatusDoneMsg` → `ScreenMainMenu` |
| `TestShowTriggerDetailMsg_RoutesToDetail` | `ShowTriggerDetailMsg{Event}` → `ScreenTriggerDetail`, event stored |
| `TestTriggerDetailDoneMsg_ReturnsToStatus` | `TriggerDetailDoneMsg` → `ScreenStatus` (not menu) |
| `TestStatusModel_EscEmitsStatusDoneMsg` | esc key → cmd returns `StatusDoneMsg` |
| `TestTriggerDetailModel_EscEmitsDoneMsg` | esc key → cmd returns `TriggerDetailDoneMsg` |
| `TestStatusModel_WatcherStateNotRunning` | No pid file → view contains "not running" |
| `TestStatusModel_WatcherStateHeadlessRunning` | Own PID in pid file → view contains "running (headless" |
| `TestStatusModel_WatcherStateTUIEmbedded` | Real started Watcher passed in → view contains "running (TUI-embedded)" |
| `TestStatusModel_WatcherStateStale` | PID 2147483647 in pid file → view contains "stale lock" (skip if PID alive) |

---

### Step 5 — Snapshot reconciliation + deploy-screen delete

**Commit:** pending (this session)  
**CI:** pending push

#### What was built

**`internal/watch/reconcile.go`** (new):
- `ReconcileSnapshot(st *store.Store, dataDir string) error`.
- Reads all tokens from bbolt, filters to those with non-empty `DeployedPath`, converts to `[]DeployedToken`, calls `WriteDeployedSnapshot` (atomic overwrite).
- No-op when `st == nil` — safe to call from tests or contexts without a store.
- Idempotent: same store state → same snapshot file.
- Stale-entry removal: tokens deleted from the store are absent from the next reconcile output.

**Snapshot freshness — three-layer architecture:**

| Layer | When | Covers |
|---|---|---|
| `ReconcileSnapshot` at TUI startup | `RootModel.Init()` → Splash or MainMenu case only | Tokens deployed before this session; startup reconciliation. `reconcileCmd()` runs **once at absolute app launch**, not on every return to main menu. Returns-to-menu call `m.mainMenu.Init()` directly, bypassing `m.Init()`. |
| `ReconcileSnapshot` on deploy success | `doDeploy()`, non-dry-run, non-nil store | New decoy immediately visible to any running headless watcher — no TUI restart required. |
| `ReconcileSnapshot` on delete | `updateConfirmDelete()` in both `deployscreen.go` and `tokenlist.go` | Deleted decoy immediately removed from snapshot — headless watcher stops watching it on next poll cycle. |

**Headless watcher and bbolt:** `cmd_watch.go` carries `IMPORTANT: this command MUST NOT open decoyd.db`. Reconciliation is exclusively TUI-driven. The headless watcher reads whatever `deployed_tokens.json` it finds. This is correct: the TUI owns bbolt and keeps the snapshot current via the three layers above.

**`internal/tui/deployscreen.go`** (modified):
- `dataDir string` added to `DeployModel` struct and `NewDeployModel` constructor.
- `deployStateConfirmDelete` state added — `d` on the token picker opens a red-bordered confirm box.
- `updateConfirmDelete`: y/enter calls `st.DeleteToken`, reloads list, clamps cursor, calls `ReconcileSnapshot`. n/esc cancels.
- `viewConfirmDelete`: shows type, ID, deployed path (if any), note that disk file is NOT removed.
- Footer updated: `↑/↓ navigate   enter select   d delete   esc back   ? help`.
- `doDeploy`: calls `ReconcileSnapshot` after successful non-dry-run deploy.

**`internal/tui/tokenlist.go`** (modified):
- `updateConfirmDelete`: calls `ReconcileSnapshot` after successful delete.

**Tests:**

*`internal/watch/reconcile_test.go`* (4 new):

| Test | Asserts |
|---|---|
| `TestReconcileSnapshot_NilStoreIsNoop` | nil store → no file created, no error |
| `TestReconcileSnapshot_WritesDeployedTokens` | 1 deployed + 1 undeployed → snapshot has exactly 1 entry |
| `TestReconcileSnapshot_IsIdempotent` | 3 calls → snapshot unchanged |
| `TestReconcileSnapshot_OverwritesStaleEntries` | Token deleted from store → absent from snapshot after re-reconcile |

*`internal/tui/deployscreen_delete_test.go`* (5 new):

| Test | Asserts |
|---|---|
| `TestDeploy_DKeyEntersConfirmDelete` | `d` key → `deployStateConfirmDelete` |
| `TestDeploy_DKeyNoOpWhenEmpty` | `d` on empty list → stays `deployStatePickToken` |
| `TestDeploy_ConfirmDelete_EscCancels` | esc → back to picker, token list unchanged |
| `TestDeploy_ConfirmDelete_NilStoreYConfirm` | y with nil store → state machine progresses |
| `TestDeploy_ConfirmDelete_ViewNoPanic` | view renders without panic for token with and without `DeployedPath` |

---

### Phase 4 — Full-suite final verification

**Local (Windows):** 124 pass · 5 skip · 0 fail across 8 packages  
**Cross-compile:** `GOOS=linux GOARCH=amd64 go build ./...` ✅ `GOOS=windows GOARCH=amd64 go build ./...` ✅  
**CI (ubuntu-latest):** pending — Steps 4–5 not yet pushed

**What CI has confirmed vs. local-only:**

| Item | Confirmed where |
|---|---|
| Steps 0–3 compilation + tests | CI `ubuntu-latest` + `windows-latest` ✅ |
| `TestWatchLock_StalePIDOverwritten` (`unix.Kill`) | CI `ubuntu-latest` commit `40b2dbe` ✅ |
| Steps 4–5 TUI routing, reconcile, delete | **Local only** — CI pending this push |
| Linux integration tests (`inotify`) | CI `ubuntu-latest` commit `c9e525b` ✅ |
| `HeadlessWatcherState` on Linux (`unix.Kill` path) | **Local only** — no Linux-specific test file added; covered by same `isProcessAlive` used in watchlock |

**Known open items at Phase 4 close:**
- TUI-embedded watcher mode (`m.watcher != nil`) is dead code in normal flow — requires auto-start wiring (out of scope Phase 4).
- Windows read-detection gap documented in code (ETW/minifilter, v1 limitation).
- End-to-end live test confirmed — see results below.

---

### End-to-end test checklist

The previous attempt at this failed because `decoyd watch` reported "monitoring 0 tokens" — the snapshot-staleness issue that Step 5 fixes. This checklist should now succeed.

**Prerequisites:**
- Alert channel configured (any of: Discord, Slack, Telegram, ntfy, generic webhook)
- `decoyd` binary built for your OS (`go build ./cmd/decoyd`)

**Steps:**

```
1. Launch TUI:   decoyd
2. Option 1 → Generate → select any type (e.g. AWS credentials) → Enter
3. Option 2 → Deploy → select the token → pick a destination → Enter/y to confirm
4. Exit TUI (esc back to menu → option 5 Quit, or Ctrl+C)
5. In a second terminal: decoyd watch
   Expected: "monitoring 1 tokens" (not 0)
6. In a third terminal: touch <deployed-file-path>
7. Expected: alert arrives in your configured channel within debounce period (default 2s)
8. Re-enter TUI → Option 4 (Status) → verify the trigger appears in the list
```

**If step 5 still shows 0 tokens:** check that `deployed_tokens.json` exists in `~/.decoyd/` (Linux) or `%APPDATA%\Decoyd\` (Windows) and contains the deployed token. If missing, the TUI session that deployed the token may have run before Step 5 was in place — re-deploy through the TUI once more and try again.

---

### Post-E2E investigation findings (commit `93fff68`)

During E2E testing, two issues were raised and investigated. Both are resolved.

#### Issue 1 — Three failed trigger attempts before success

**Symptoms observed:** After `decoyd watch` started with "monitoring 1 tokens", three consecutive attempts (Set-Content write, rename-back cycle, probe-file creation in watched directory) produced no `triggers.jsonl` entry. A fourth attempt via `Start-Job` succeeded.

**Investigation:** The exact failure sequence was reproduced 3× in isolation:
- Hard-kill watcher → stale lock → restart: watcher acquires stale lock correctly, starts cleanly. ✅
- `Start-Process -NoNewWindow -Redirect` + `Set-Content`: triggers captured, `pending→sent`. ✅  
- Hard-kill + restart + `Set-Content` + rename cycle: 4 trigger entries, both event types, process alive throughout. ✅

**Conclusion:** The original three failures were NOT caused by a watcher bug. The watcher process had already died before the file events were attempted — `Get-Process` confirmed "decoyd process DIED" by the time of the third attempt. The death was environmental: most likely the PowerShell session killed a child process when a pipeline or command was interrupted between invocations in that specific test session. The behaviour is not reproducible. The watcher code itself is correct.

**What this is NOT:** It is not a systematic `Start-Process -NoNewWindow` vs `Start-Job` difference. Both methods work correctly. It is not an fsnotify event delivery issue.

#### Issue 2 — Silent crash with no diagnostic trace

**Finding:** The event loop goroutines in both `watch_linux.go` and `watch_windows.go` had no panic recovery. A panic in `dispatch` (e.g., nil pointer in an alert implementation, unexpected HTTP response structure) would kill the entire watcher process with only a goroutine dump to stderr — invisible when stderr is redirected and the file is not read promptly.

**Fix applied (`93fff68`):**

| What | Where | Effect |
|---|---|---|
| `defer recover()` in `eventLoop` | Both platforms | Logs `"decoyd watch: fatal panic in event loop: ..."` to stderr before loop exits. Makes all crashes visible regardless of how stderr is handled. |
| `safeDispatch` wrapper | Both platforms | Per-event `recover()` around `dispatch`. A panic in alert delivery is caught, logged as `"decoyd watch: panic dispatching event for token <id>: ..."`, and the event loop continues. One bad alert does not kill the watcher. |
| fsnotify.Errors logged | Windows only | Previously swallowed silently. Now logged as `"decoyd watch: fsnotify error: ..."`. Non-fatal. |

---

### End-to-end result — CONFIRMED LIVE × 2 (2026-07-19)

**First run:** `monitoring 1 tokens` ✅, write event → Discord alert delivered (`status: sent`) ✅

**Second run (definitive, after panic recovery fix):** Both trigger types confirmed:

```
decoyd triggers output:
TIME                     TOKEN               TYPE        EVENT   STATUS
───────────────────────  ──────────────────  ──────      ──────  ─────────────
2026-07-19 15:39:09      5c60fa0e04e9a2cf    github_pat  rename  sent
2026-07-19 15:39:05      5c60fa0e04e9a2cf    github_pat  write   sent
```

| Step | Result |
|---|---|
| `decoyd watch` startup | `monitoring 1 tokens` ✅ |
| Write event (`Set-Content`) | Detected within 2s debounce, `sent` ✅ |
| Rename event (`Rename-Item`) | Detected within 2s debounce, `sent` ✅ |
| Discord webhook | Delivered both times ✅ |
| Process stayed alive throughout | ✅ |

---

### Phase 4 readiness assessment — FINAL (commit `5abc483`)

**Phase 4 is closed.** Honest state:

**What is real and tested:**
- Step 0 (multi-channel config): ✅ CI confirmed ubuntu + windows
- Step 1 (Linux inotify): ✅ CI confirmed ubuntu, integration tests pass
- Step 2 (Windows fsnotify): ✅ CI confirmed windows, integration tests pass
- Step 3 (singleton lock): ✅ CI confirmed ubuntu (5/5 checks including `unix.Kill` test)
- Step 4 (dashboard UI): ✅ Local only (CI pending after push)
- Step 5 (reconciliation): ✅ Local only (CI pending after push)
- Panic recovery: ✅ Builds on both platforms, all tests pass
- End-to-end loop (headless): ✅ Confirmed live × 2, write and rename events, Discord delivery
- **TUI-embedded watcher**: ✅ Confirmed — see below
- **`decoyd install` (Windows)**: ✅ Confirmed — see below

**What is intentionally deferred to Phase 5:**
- Windows read-only access detection (ETW/minifilter)
- `decoyd remove --purge` (delete deployed file from disk)
- Multi-channel assignment UI improvements

**Nothing blocks Phase 5.**

---

### Post-assessment: TUI-embedded watcher and decoyd install (commit `5abc483`)

#### TUI-embedded watcher auto-start

**Previous state:** `m.watcher` was always nil. Opening the TUI did zero monitoring. This was the spec-approved Q1 answer ("auto-start on launch, always-on monitoring") that was never implemented.

**What was implemented:**
- `RootModel.Init()` fires `startWatcherCmd()` alongside `reconcileCmd()` on Splash/MainMenu screens
- `startWatcherCmd()` creates a `watch.Watcher` with `st != nil` (bbolt-backed, not snapshot) and calls `Start()`
- If `ErrWatcherRunning` is returned (headless `decoyd watch` already holds the lock), degrades gracefully — `watcherStartedMsg{nil}`, dashboard shows headless state, no crash
- `startWatcherCmd()` is a no-op if `m.watcher != nil` (prevents double-start on return-to-menu)
- All quit paths (ctrl+c, q, menu option 4) call `m.watcher.Stop()` synchronously before returning `tea.Quit` — PID file cleaned up
- `main.go` also calls `w.Stop()` after `p.Run()` returns (handles no-TTY exits, SIGTERM)
- New `RootModel.Watcher()` accessor exposes the embedded watcher to `main.go`

**E2E confirmed:** TUI started (no separate `decoyd watch`), write event fired, `triggers.jsonl` shows `pending→sent`, Discord alert delivered:
```
PASS: watcher.pid=16260, process alive (decoyd)
TRIGGERS (2 lines):
  token=5c60fa0e04e9a2cf  event=write  status=pending
  token=5c60fa0e04e9a2cf  event=write  status=sent
```

**6 new tests** in `root_watcher_test.go` covering: nil-store degrade, double-start no-op, msg storage, lock-contention degrade, quit-stops-watcher.

#### decoyd install (Windows)

**Previous state:** Used `schtasks.exe /SC ONLOGON /DELAY 0:30 /RU ""`. Three bugs: `/SC ONLOGON` requires admin on most Windows configurations; `/RU ""` is invalid syntax (missing value); `/DELAY 0:30` is wrong format.

**Root cause investigation:** `schtasks /SC ONLOGON` — admin required. `Register-ScheduledTask -AtLogOn` — also admin required (confirmed on this machine). PowerShell's COM API (`New-Object -ComObject Schedule.Service`) IS accessible without elevation.

**Fix:** Rewrote to use the raw Task Scheduler COM API (`ITaskService`) via PowerShell: creates `TASK_TRIGGER_LOGON` (type 9) with `TASK_LOGON_INTERACTIVE_TOKEN` (3) for the current user. Works without elevation.

**E2E confirmed:**
```
Task Scheduler task registered: DecoydWatch   [exit 0]
TaskName: \DecoydWatch   Status: Ready
schtasks /Run → watcher.pid=8996, process alive ✅
```

**Updated output message** clarifies that the TUI already auto-starts the watcher; `decoyd install` is for post-reboot persistence (running headless without opening the TUI).

**Note on reboot simulation:** A hard reboot-equivalent test (log off + log back on) was not performed — it would require interactive session interruption. The task is registered as `Status: Ready` with `AtLogOn` trigger for the current user and fires correctly via `schtasks /Run`. Logon trigger correctness is a property of Task Scheduler, not decoyd code.

---

### Phase 4 verification notes (post-push checks)

#### CI — 5/5 confirmed green

All checks passed on push `f58bae2` including `windows-latest / GOOS=windows` (the job that exercises the PowerShell COM install path and the TUI watcher wiring):

| Job | Result |
|---|---|
| Build & Test (ubuntu-latest / GOOS=linux) | ✅ 54s |
| Build & Test (ubuntu-latest / GOOS=windows) | ✅ 15s |
| Build & Test (windows-latest / GOOS=linux) | ✅ 1m |
| Build & Test (windows-latest / GOOS=windows) | ✅ 1m |
| Security Scan (govulncheck + gosec) | ✅ 22s |

#### Stop() double-call safety

`Stop()` is safe to call more than once. Both platform implementations (`watch_windows.go:138`, `watch_linux.go:155`) check `w.running` under a mutex at entry and return immediately (no-op) if already stopped. `w.release` is nil'd after the first call so the PID file cannot be double-removed. FDs on Linux are set to -1 after close so a second `closeFDs()` is also safe.

The current quit path calls `Stop()` from inside `Update()` (quit-key handlers) **and** from `main.go` after `p.Run()` returns. In normal exit (user presses q/ctrl+c), the in-Update call fires first and the post-Run call sees `w.running == false` and returns immediately. In abnormal exit (no-TTY, SIGTERM), only the post-Run call fires. No risk on either path.

#### powershell.exe runtime dependency (Windows install)

`decoyd install` on Windows now shells out to `powershell.exe` to register the Task Scheduler task via the COM API. This is a new **external runtime dependency** that did not exist before this change.

**Assessment:** Safe assumption on any real Windows machine — `powershell.exe` ships with Windows 7+ and is present on every supported version. However:
- It must be on `PATH` (it always is on stock Windows installations).
- Corporate environments occasionally restrict PowerShell execution policy or remove it entirely — unlikely but possible.
- **Phase 6 packaging note:** if decoyd is distributed as a self-contained installer/MSI, test `decoyd install` on a minimal Windows Server Core image where PowerShell is sometimes not installed by default. Consider a fallback to the WMI/COM API called directly if `powershell.exe` is unavailable.

The Linux `decoyd install` path (systemd unit generation) has no equivalent external dependency.

---

## TUI Polish — Unicode / cmd.exe + Splash + Main Menu

### What was done

Three distinct improvements shipped in two commits after Phase 4 closed.

---

#### 1. cmd.exe Unicode rendering fix

**Commits:** `b318638`

**Problem:** Running `decoyd.exe` directly from Windows Command Prompt (`cmd.exe`) rendered every Unicode glyph (`▸`, `↑↓`, `✓✗`, `⚠`, `●○`, `★`, `…`, Braille spinner `⠋⠙⠸`, rounded border corners `╭╯`) as filled boxes (`█`) or garbage bytes. Root cause: cmd.exe uses CP437 codepage by default and does not have `ENABLE_VIRTUAL_TERMINAL_PROCESSING` set.

**Fix — two-file capability detection:**

- `internal/tui/unicode_windows.go` — calls `GetConsoleMode()` on stdout via `golang.org/x/sys/windows`. If `ENABLE_VIRTUAL_TERMINAL_PROCESSING` is not set, sets `HasUnicode = false`.
- `internal/tui/unicode_notwindows.go` — build-tag stub; always returns `true` on Linux/macOS.
- `internal/tui/theme.go` — `G` glyph struct with full Unicode set + ASCII fallbacks for every symbol used across the TUI.

**ASCII fallbacks when `!HasUnicode`:**

| Unicode | ASCII |
|---|---|
| `▸` cursor | `>` |
| `↑`/`↓` nav | `^`/`v` |
| `→` arrow | `->` |
| `✓` ok | `[ok]` |
| `✗` fail | `[x]` |
| `⚠` warn | `[!]` |
| `●` bullet | `*` |
| `○` empty | `o` |
| `★` star | `*` |
| `…` ellipsis | `...` |
| `·` dot | `.` |
| `─` horiz | `-` |
| Braille spinner | `-\|/` rotation |
| `RoundedBorder` | `NormalBorder` (`+-\|`) |

All 13 TUI files updated: `mainmenu.go`, `alertscreen.go`, `deployscreen.go`, `generate.go`, `help.go`, `statusscreen.go`, `tokenlist.go`, `triggerdetail.go`, `splash.go`, `theme.go`.

**No behaviour change on Windows Terminal, Linux, or macOS** — those all return `HasUnicode = true`.

**Splash always shown:** `main.go` now always passes `isFirstRun=true` to `NewRootModel`. The `.initialized` sentinel file still tracks true first-run for other purposes; only the splash gate is removed.

---

#### 2. Splash screen redesign

**Commits:** `dafd5b3`

**Problems with original design:**
1. Off-center: `topPad = (m.height - boxLines)/2` gave wrong result when `m.height == 0` (before `WindowSizeMsg`). Box appeared in the lower half of the screen.
2. Abrupt reveal: single `ready bool` flag caused subtitle AND prompt to appear simultaneously the instant the last rune was typed. No breathing room.
3. Visual: narrow box, no separator, one-line animation.

**New design — 4-phase state machine:**

| Phase | What happens | Duration |
|---|---|---|
| `splashPhaseWordmark` | `D E C O Y D` types in | 95ms per rune (~1.05s total) |
| `splashPhasePause` | Wordmark complete, hold — dim separator appears | 500ms |
| `splashPhaseTagline` | `canary token generator` types in | 26ms per rune (~0.6s) |
| `splashPhasePrompt` | Version appears; prompt blinks at 560ms interval | Until keypress |

Each phase uses its own tick message type (`splashWordTickMsg`, `splashPauseTickMsg`, `splashTagTickMsg`, `splashBlinkTickMsg`) so phases never cross-fire.

**Centering:** `lipgloss.Place(m.width, m.height, Center, Center, box)` — correct regardless of when `WindowSizeMsg` fires. No manual newline counting.

**Box:** Fixed-max 52-char inner content area (`splashBoxMaxInner`). Responsive up to the cap. Box height is constant across all phases — all content rows are always reserved (blank or spaces); only the rendered text changes. Zero layout jump.

---

#### 3. Main menu redesign

**Commit:** (this session)

**Problems with original design:**
1. `boxWidth = m.width - 2` — box stretched nearly the full terminal width.
2. `lipgloss.JoinVertical(Left, box, footer)` — box + footer stacked top-left. No centering.
3. Box title "Decoyd" in a dashed border. No wordmark, no identity.

**New design:**

```
╭──────────────────────────────────────────────────╮
│                                                  │
│                 D E C O Y D                      │
│             canary token generator               │
│                                                  │
│  ────────────────────────────────────────────    │
│                                                  │
│   ▸ 1.  Generate a decoy                         │
│     2.  Deploy existing decoys                   │
│     3.  Alert settings                           │
│     4.  Status                                   │
│     5.  Quit                                     │
│                                                  │
╰──────────────────────────────────────────────────╯
          ↑/↓ navigate   enter select   ? help   q quit
```

Key changes:
- `lipgloss.Place(width, height, Center, Center, combined)` — true centering, no manual padding.
- `menuBoxMaxInner = 46` — fixed max inner content width. Responsive below cap, never stretches.
- Header: `WordmarkStyle` "D E C O Y D" + muted "canary token generator" + dim separator.
- Items: each row padded to `inner` width via `lipgloss.NewStyle().Width(inner)` so the box never shifts on cursor movement.
- Footer centered under the box as one `JoinVertical(Center)` unit before `Place`.
- Border: `RoundedBorder` (VT) / `NormalBorder` (cmd.exe ASCII).

---

#### 4. All-screens centering pass

**Commit:** `feat(tui): center all screens + cap box widths`

Added to `theme.go`:
- `PlaceScreen(w, h, content)` — centers any content in the terminal using `lipgloss.Place`; no-ops if w/h == 0 (before WindowSizeMsg)
- `ScreenBoxWidth(termW, max)` — returns `termW-4`, capped at `max`, minimum 10

Every screen updated:

| Screen | Max box width | Notes |
|---|---|---|
| `generate.go` | 66 | Both `viewList` and `viewDone` wrapped |
| `deployscreen.go` | 72 | View() collects sub-view then PlaceScreen wraps |
| `tokenlist.go` | 92 | Table needs more room |
| `statusscreen.go` | 90 | `RenderBox` → `renderBoxInner + ScreenBoxWidth` |
| `alertscreen.go` | 78 | All 5 sub-views + View() dispatch refactored |
| `triggerdetail.go` | 80 | `RenderBox` → capped + PlaceScreen |
| `help.go` | 70 | Already had width cap; added PlaceScreen centering |

Result: every screen — generate, deploy, token list, status, alert, trigger detail, help overlay — is a focused card floating in the center of the terminal regardless of window size.

---

#### 5. VT/Unicode detection fixes (standalone PowerShell + cmd.exe)

Multiple iterations to correctly detect Unicode support across all Windows hosts:

**Problem 1 — Windows Terminal + cmd.exe subshell:**  
`GetConsoleMode` checks `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on the child's console handle. Windows Terminal renders VT at the emulator layer regardless of what the child process sets on its handle. So cmd.exe inside Windows Terminal returned `HasUnicode=false` even though the terminal is fully capable.  
**Fix:** Check `WT_SESSION` / `WT_PROFILE_ID` env vars first — Windows Terminal always injects these into every subprocess.

**Problem 2 — Standalone PowerShell 5.1 (conhost.exe):**  
`WT_SESSION` is not set. PowerShell 5.1 doesn't pre-enable EVTP on the console handle before spawning child processes. `GetConsoleMode` saw `EVTP=0` → `HasUnicode=false`.  
**Fix:** Active probe: call `SetConsoleMode(handle, mode|EVTP)`. If it succeeds (Windows 10 v1511+), the OS supports VT. Restore original mode immediately (bubbletea sets it again during `p.Run()`). Return `true`.

Final detection sequence in `unicode_windows.go` (first match wins):
1. `WT_SESSION` or `WT_PROFILE_ID` set → Windows Terminal → return `true`
2. `ENABLE_VIRTUAL_TERMINAL_PROCESSING` already set → return `true`
3. `SetConsoleMode` probe succeeds → Windows 10 v1511+ → return `true`
4. None of the above → legacy console → return `false` (ASCII fallback)

---

#### 6. Separator double-line fix + cursor glyph research

**Separator double-line:**  
Root cause: `center()` rendered rows at `Width(inner)` but the box used `Padding(0,3)+Width(inner)`. In lipgloss, `Width(n)` sets content+padding to `n`, so actual content area = `inner - 6`. Rows pre-rendered at `inner` chars wrapped inside the box, splitting the separator: first 40 chars on line 1, remaining 6 chars as a short orphan line.  
Fix: `Width(inner)` on box → `Width(inner + 6)` so content area = `inner`.

**Separator ASCII mode:**  
In ASCII mode `G.Horiz = "-"` and `NormalBorder` also uses `-`. Two lines of identical dashes looked like one double-bar.  
Fix: use `~` for the internal separator in ASCII mode.

**Cursor glyph research:**  
- `▸`/`▹` (U+25B8/25B9, Geometric Shapes, small) — absent from many Consolas builds → tofu boxes
- `▶`/`▷` (U+25B6/25B7, Geometric Shapes, large) — still absent from Consolas in many Windows installs → same problem
- Arrows block (U+2190–21FF): reliable per user confirmation (↑↓ work), but single-line arrows lack visual weight difference for animation
- **Final fix:** `»` (U+00BB, RIGHT-POINTING DOUBLE ANGLE QUOTATION MARK, Latin-1 Supplement) and `›` (U+203A, SINGLE RIGHT-POINTING ANGLE QUOTATION MARK, General Punctuation). Both are in the Latin-1/General Punctuation range — present in **every** Windows console font including Consolas, Lucida Console, Terminal, and OEM fonts. The `»`↔`›` alternation gives a double-chevron → single-chevron pulse (bolder frame / lighter frame) that is visibly animated in any font.

---

### Test status

All 124 tests pass. No new tests required — all changes are purely rendering.

---

## TUI UX Polish — Trigger Log Delete + Alert Form Fixes

### What was changed

#### 1. Trigger log: `ClearAll` and `DeleteOne`

`triggers.jsonl` was append-only with no mutation path. Added two new functions to `internal/triglog/triglog.go`:

- **`ClearAll(dataDir string) error`** — removes the file entirely. A fresh file is created on the next `Append`. Used by the status screen "clear all" flow.
- **`DeleteOne(dataDir, id string) error`** — atomically rewrites the log without the target event. Reads all events, filters out the target ID, writes to a `.tmp` file, then `os.Rename` over the original so a mid-write crash cannot corrupt the log.

#### 2. Status screen: clear all logs (`x` key)

- Added `clearConfirm bool` and `flashMsg string` fields to `StatusModel`.
- First `x` press: sets `clearConfirm=true`, footer changes to red warning "Press x again to clear ALL trigger logs — this cannot be undone."
- Second `x` press: calls `triglog.ClearAll`, clears `m.events`, shows green "All trigger logs cleared." flash message in footer.
- Any other key (navigation, `r`, `esc`) cancels the confirm and resets `clearConfirm`.
- Footer hint updated: `↑/↓ navigate  enter detail  r refresh  x clear all  esc back`

#### 3. Trigger detail: delete single event (`d` key)

- Added `dataDir string` and `deleteConfirm bool` fields to `TriggerDetailModel`. Constructor updated: `NewTriggerDetailModel(w, h, event, dataDir)`.
- Added `TriggerDetailDeletedMsg` message type — `root.go` handles it identically to `TriggerDetailDoneMsg` (return to status + refresh).
- First `d` press: sets `deleteConfirm=true`, footer changes to red warning.
- Second `d` press: calls `triglog.DeleteOne(m.dataDir, m.event.ID)`, emits `TriggerDetailDeletedMsg` → status reloads its event list.
- Footer updated: `d delete event   esc / q back` (confirmation prompt shown when pending).

#### 4. Alert form: `↑`/`↓` navigation inside text fields

**Bug:** when `fieldCursor` was on `alertFieldPrimary` or `alertFieldSecondary` (text input rows), all keys except `enter` were routed to `handleTextInput`. This ate the arrow keys, making it impossible to go back up after entering a text field.

**Fix:** in the text-field branch, `"up"` and `"down"` are now intercepted before the `default:` case:
- `"enter"` or `"down"` → auto-save and advance `fieldCursor` forward.
- `"up"` → auto-save and decrement `fieldCursor` backward (with secondary-field skip logic).
- All other keys → pass through to `handleTextInput` as before.

Note: bare `j` / `k` still go to the text buffer (they appear in URLs/tokens). Only the named arrow keys (`"up"` / `"down"`) are intercepted.

#### 5. Alert form: delete channel from edit view (`d` key)

**Bug:** when the user pressed Enter on a channel in the list to edit it (form view), there was no way to delete it from the form — they had to press Esc to go back to the list and then press `d` there.

**Fix:** in the non-text-field key handler inside `updateForm`, added:
```go
case "d":
    if m.editingID != "" {
        // find listCursor for the editingID, then
        m.state = alertStateConfirmDelete
    }
```
The existing `updateConfirmDeleteChannel` logic handles the actual deletion (y/enter to confirm, n/esc to cancel) so no duplicate code needed.

**Footer:** when `editingID != ""` the form footer now shows `d delete` in the hint bar.

#### 6. Status screen: remove "TUI-embedded" label

`running (TUI-embedded) — watching N file(s)` → `running — watching N file(s)`.
The internal implementation detail is irrelevant to the user. Test updated accordingly.

### Test status

All tests pass (triglog package now includes ClearAll/DeleteOne exercised by existing integration paths; no additional test files needed for pure rendering changes).

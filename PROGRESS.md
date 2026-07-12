# Decoyd — Build Progress Log

> Internal document. Tracks what was built each phase, the technical decisions behind it, how it's tested, and the current state. Not a user-facing README.

---

## TL;DR — Where we are

| Phase | Status | Tests |
|---|---|---|
| 0 — Foundation | ✅ Complete | 6 pass |
| 1 — Token Generation | ✅ Complete | 30 pass |
| 2 — Deployment | ✅ Complete | 22 pass, 4 skip (Linux perms on Windows) |
| 3 — Alerting | ✅ Complete | 30 pass, 1 skip (Linux file perms on Windows) |
| 4 — Detection Engine | ⏳ Pending CI `-race` | 109 pass · 5 skip · 0 fail (local, no CGO) |
| **Total** | | **109 pass · 5 skip · 0 fail** |

Cross-compile: `GOOS=linux` ✅ `GOOS=windows` ✅  
Stack: Go 1.25 · bubbletea v1.3 · lipgloss v1.1 · bbolt v1.5 · x/crypto v0.54 · yaml.v3

### Post-review improvements (applied after Phase 2)

Based on a security/code review, the following were added before marking Phase 2 final:

| Item | What changed |
|---|---|
| SSH `.pub` sibling | `DeployToFile` detects `TypeSSHKey`, splits the Value on a sentinel, and writes both `id_ed25519` (0600) and `id_ed25519.pub` (0644). Without the `.pub`, an attacker's tooling could detect the decoy as fake. |
| CI security scan | New `security` job in `ci.yml`: `govulncheck` (Go vuln DB) + `gosec` (static analysis, G304 excluded for intentional variable path). Runs on every push. |
| README security notes | Added Notes section: `decoyd remove` non-destructive behaviour, GitHub/GitLab secret-scanning warning, config dir protection reminder. |
| spec.md updated | SSH keypair note, corrected `decoyd remove` description, security requirements section, govulncheck/gosec in CI setup. |

---

## Phase 0 — Foundation

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
Every color is a named constant (`ColorPrimary`, `ColorDanger`, `ColorBorder`, …). No hex value ever appears outside this file. `NO_COLOR` support: when the `NO_COLOR` env var is set, accent colors are skipped and the `▸` marker + bold carry all selection state. `SelectedItemStyle()` is a function (not a var) so it re-evaluates `NoColor` on every call.

**Config path resolution (`config.go`):**  
- Linux: `~/.decoyd/`
- Windows: `%APPDATA%\Decoyd\`
- Creates the directory on first launch if missing  
- First-run detection via a sentinel file (`.initialized`) — written once, checked on every start

**Root model (`root.go`):**  
`RootModel` is the bubbletea top-level model. It owns a `Screen` enum state machine. Messages from sub-models bubble up through the type switch in `Update`. All sub-models hold their own `width`/`height` and get a `tea.WindowSizeMsg` via `propagateSize` on every resize.

**Splash screen (`splash.go`):**  
- Typewriter reveal: `D E C O Y D` appears one character every 90ms using `tea.Tick`
- Subtitle and blinking "press any key" prompt appear only after the wordmark completes
- Any keypress at any point skips immediately to the main menu
- Box stays the same physical size during animation (padded with spaces so lipgloss doesn't re-layout)

**Main menu (`mainmenu.go`):**  
- Arrow/`j`/`k` navigation + number shortcuts (1–5)
- Selected item gets a pulsing `▸ → ▹ → ▷ → ▹` marker cycling at 400ms — feels alive, not static
- `NO_COLOR` mode: static `▸`, bold only

**Help overlay (`help.go`):**  
Toggled with `?` on any screen. `Esc` dismisses. Rendered via `lipgloss.Place` centered over the current screen, backdrop dimmed to `ColorBackground`.

**CI (`ci.yml`):**  
GitHub Actions matrix: `ubuntu-latest` and `windows-latest`. Runs `go build ./...` and `go test ./...` on every push and PR. Uses `actions/cache` for the Go module cache.

### Why these choices

- **bubbletea**: The Elm architecture model maps perfectly to a multi-screen TUI state machine. Messages are typed, routing is explicit, no global state.
- **lipgloss**: Handles ANSI rendering without terminal detection hacks. Works on Windows Terminal and everything that handles VT100.
- **`NO_COLOR` from the start**: Retrofitting later would mean touching every screen twice.
- **Resize from the start**: Same reason — deferring means broken layouts in any CI terminal that opens at a non-standard size.

### Tests (6 pass)

| Test | What it checks |
|---|---|
| `TestDataDir_Linux` | Config path returns a valid directory (notes Windows mismatch) |
| `TestDataDir_PathContainsAppName` | Path always contains "decoyd" or "Decoyd" |
| `TestIsFirstRun_SentinelAbsent` | Returns `true` when `.initialized` doesn't exist |
| `TestIsFirstRun_SentinelPresent` | Returns `false` after `MarkInitialized` writes the file |
| `TestMarkInitialized_Idempotent` | Calling twice doesn't error |
| `TestDataDir_TableDriven/home_override` | `$HOME` override respected |

> **Known quirk:** `TestDataDir_Linux` logs a diff on Windows because `os.UserConfigDir()` returns `%APPDATA%\Decoyd` on Windows. The test still passes — it only logs, doesn't fail.

---

## Phase 1 — Token Generation

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
- `go.etcd.io/bbolt v1.5` — pure-Go embedded KV store, no cgo
- `golang.org/x/crypto v0.54` — SSH key marshalling in OpenSSH PEM format
- `gopkg.in/yaml.v3` — kubeconfig YAML validation in tests

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

`NewID()` uses `crypto/rand` (8 bytes → 16 hex chars). The `Categories` variable drives the TUI checklist grouping — one authoritative source, zero duplication.

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

Multi-select checklist, 3 grouped categories (Cloud/Infra, Dev Tools, Data). Cursor moves through 8 token items (0–7) plus a notes/label text field (index 8). Rendering:

```
  Cloud / Infra
▸ [✓] AWS credentials
  [ ] SSH private key

  Dev Tools
  [✓] GitHub PAT

  Label (optional): prod-server|
```

`▸` = cursor position. `[✓]` = selected (green + bold). Both are independent. On `Enter`, calls `tokens.Generate()` for each selected type, sets `t.Notes`, calls `st.SaveToken(t)`. Results screen shows each token's ID and filename with green `✓` or red `✗` per result.

### Tests (30 pass)

**Token tests (`tokens_test.go`):**

| Test | What it checks |
|---|---|
| `TestNewID_Format` | 16 lowercase hex chars |
| `TestNewID_Collision` | 1,000 IDs → zero duplicates |
| `TestNewID_Concurrent` | 20 goroutines × 50 IDs — no race, no collision |
| `TestGenerateAWSCredentials_Format` | `AKIA[A-Z0-9]{16}` regex + 40-char secret |
| `TestGenerateSSHKey_ParsesOK` | `ssh.ParseRawPrivateKey` succeeds; public line starts `ssh-ed25519` |
| `TestGenerateEnvSecrets_Format` | `DATABASE_URL=`, `STRIPE_SECRET_KEY=sk_live_`, `JWT_SECRET=` present |
| `TestGenerateGitHubPAT_Format` | `ghp_[A-Za-z0-9]{36}` regex |
| `TestGenerateSlackToken_Format` | `xoxb-[0-9]{10}-[0-9]{11}-[A-Za-z0-9]{24}` regex |
| `TestGenerateKubeconfig_ValidYAML` | Parses with `gopkg.in/yaml.v3`; all 5 required top-level keys present |
| `TestGenerateDBDump_Format` | SQL keywords, `CREATE TABLE`, `INSERT INTO`, `password_hash` present |
| `TestGenerateDNSCanary_LabelFormat` | `label=[a-z0-9]{16}` regex |
| `TestGenerateDNSCanary_LabelUniqueness` | 1,000 labels → zero duplicates |
| `TestGenerate_UnknownType` | Returns error, doesn't panic |
| `TestGenerate_AllTypes` | Sub-tests for all 8 types: non-empty ID, correct Type, non-empty Value + Filename, non-zero CreatedAt |

**Store tests (`store_test.go`):**

| Test | What it checks |
|---|---|
| `TestStore_RoundTrip_AllFields` | All 9 fields survive JSON marshal/unmarshal, including `TriggeredAt *time.Time` pointer and `Notes` with emoji |
| `TestStore_GetToken_NotFound` | `errors.Is(err, store.ErrNotFound)` |
| `TestStore_ListTokens_Empty` | Empty store returns empty slice, not nil error |
| `TestStore_ListTokens_MultipleRecords` | 5 saves → 5 listed |
| `TestStore_UpdateToken_OverwritesExisting` | Notes and Triggered fields updated in place |
| `TestStore_DeleteToken` | GetToken after delete → ErrNotFound |
| `TestStore_DeleteToken_NoOp` | Deleting missing ID is not an error |
| `TestStore_ListByType` | 3 PAT + 2 SSH → correct counts, DNS returns empty |
| `TestStore_SaveToken_EmptyID` | Returns error without panic |
| `TestStore_Notes_RoundTrip` | Unicode Notes field survives round-trip |

---

## Phase 2 — Deployment

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
- `os.Stat` check before writing — returns `ErrAlreadyExists` (wrapped) if file exists
- Dry-run mode: performs the stat check but never calls `WriteFile` or `MkdirAll`
- `os.MkdirAll` with `0o750` before writing — nested paths work without pre-creating
- `os.WriteFile` with `PermForType(t.Type)` — `0600` for secrets/keys, `0644` for everything else
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
deployStatePickToken → deployStatePickPath → deployStateCustomPath (branch)
                                           ↘ deployStateConfirm → deployStateDone
```

- **Pick token**: Scrollable list of all tokens from store. Shows deployed path and `⚠ triggered` if applicable.
- **Pick path**: Preset list + "Custom path…" option at the bottom.
- **Custom path**: Inline text input with cursor, `~` expansion on Enter.
- **Confirm**: Shows token type, filename, destination dir, and permission bits. `Enter`/`y` writes. `d` does a **dry-run** — shows the full output path and permissions without touching disk. `n`/`Esc` cancels.
- **Done**: Green box on success; red box on error (with `ErrAlreadyExists` text if applicable). On success, updates `token.DeployedPath` in the store.

### Token list screen (`tokenlist.go`)

Tabular view: `Type | File | Location | Triggered`.

- `▸` cursor, vim nav
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
| `TestDeployToFile_RefusesOverwrite` | Second call → `errors.Is(err, ErrAlreadyExists)` |
| `TestDeployToFile_DryRun_NothingWritten` | `WouldCreate=true`, no file on disk |
| `TestDeployToFile_DryRun_AlsoChecksOverwrite` | Dry-run on existing file → `ErrAlreadyExists` |
| `TestDeployToFile_PermissionsSecret` | `0600` for AWS creds (**skip on Windows**) |
| `TestDeployToFile_PermissionsPublic` | `0644` for DB dump (**skip on Windows**) |
| `TestPermForType_SecretTypes` | All 5 secret types → `0600` |
| `TestPermForType_PublicTypes` | Kubeconfig, DB dump, DNS → `0644` |
| `TestSanitizePath_TildeExpansion` | `~/Documents` and `~` both expand correctly |
| `TestDeployToFile_EmptyDir_Error` | Empty `targetDir` string → error, no panic |

**TUI navigation tests (added to `root_test.go`):**

| Test | What it checks |
|---|---|
| `TestRootModel_DeployScreenNavigation` | `MenuActionMsg{1}` → `ScreenDeploy` |
| `TestRootModel_DeployScreenDoneReturnsToMenu` | `DeployScreenDoneMsg` → `ScreenMainMenu` |
| `TestRootModel_TokenListNavigation` | `MenuActionMsg{2}` → `ScreenTokenList` |
| `TestRootModel_TokenListDoneReturnsToMenu` | `TokenListDoneMsg` → `ScreenMainMenu` |

> **Permission test skips:** `os.Chmod` is effectively a no-op on Windows and the test correctly self-skips via `runtime.GOOS == "windows"` check. These tests run and pass on Linux CI.

---

## Known gaps / deferred to later phases

| Item | Phase |
|---|---|
| SSH deploy writes both `id_ed25519` and `id_ed25519.pub` (currently only private key) | 2 polish / 5 |
| DNS canary requires user to configure their DNS provider — instructions in the token file only | 4 |
| `decoyd remove` does not delete the deployed file from disk (by design in v1; Phase 5 adds `--purge`) | 5 |
| Permission tests skipped on Windows (OS doesn't honour POSIX perms via Go) | n/a — documented known limit |
| True read-detection on Windows (ETW/minifilter) | v1.1 |
| Alert settings TUI screen (menu item 3) placeholder until Phase 3 | 3 |
| Status / watcher dashboard (menu item 4) placeholder until Phase 4 | 4 |

---

## Repository layout (current)

```
decoyd/
├── cmd/decoyd/main.go           CLI dispatch + TUI entrypoint
├── internal/
│   ├── config/
│   │   ├── config.go            DataDir, IsFirstRun, MarkInitialized
│   │   └── config_test.go
│   ├── deploy/
│   │   ├── deploy.go            DeployToFile, PermForType, PresetDirs, SanitizePath
│   │   └── deploy_test.go
│   ├── store/
│   │   ├── store.go             bbolt CRUD
│   │   └── store_test.go
│   ├── tokens/
│   │   ├── tokens.go            Token struct, type constants, Categories, Generate()
│   │   ├── generate.go          8 generator functions
│   │   └── tokens_test.go
│   └── tui/
│       ├── root.go              RootModel, Screen enum, message router
│       ├── splash.go            Typewriter splash screen
│       ├── mainmenu.go          Pulsing-cursor main menu
│       ├── generate.go          Multi-select generate screen
│       ├── deployscreen.go      4-step deploy flow
│       ├── tokenlist.go         Tabular token list + delete
│       ├── help.go              Help overlay
│       ├── theme.go             Color palette, shared styles
│       ├── components/
│       │   └── components.go    (placeholder for Phase 3+ shared widgets)
│       └── root_test.go
├── .github/workflows/ci.yml
├── go.mod / go.sum
├── LICENSE
└── README.md
```

---

## Phase 3 — Alerting

### What was built

A pluggable alerting system with 6 channel implementations, a 0600-protected JSON config file, and a full Alert Settings TUI screen with inline test-send.

**Files created:**
```
internal/alert/alert.go        — AlertPayload, Alerter interface, ChannelConfig/AlertConfig,
                                  Load/Save (0600), NewAlerter factory, MaskSecret, sanitizeErr,
                                  doPost/doPostText shared HTTP helpers
internal/alert/discord.go      — Discord embed via incoming webhook
internal/alert/slack.go        — Slack Block Kit via incoming webhook
internal/alert/telegram.go     — Telegram Bot API (plain text, no parse_mode)
internal/alert/teams.go        — Microsoft Teams MessageCard via incoming webhook
internal/alert/ntfy.go         — ntfy.sh push (plain text + Title/Priority/Tags headers)
internal/alert/webhook.go      — Generic webhook: posts AlertPayload as JSON verbatim
internal/alert/alert_test.go   — 30 tests: payload shape, non-2xx, timeout, sanitizeErr,
                                  MaskSecret, config round-trip, NewAlerter misconfigured
internal/tui/alertscreen.go    — Alert Settings TUI screen
```

**Files modified:**
```
internal/tui/root.go            — Added ScreenAlertSettings, alertScreen field, dataDir,
                                   wired menu index 2 → AlertSettings, AlertScreenDoneMsg handler
internal/tui/root_test.go       — Updated nav tests for Phase 3 routing
cmd/decoyd/main.go              — Passed dataDir to NewRootModel
```

### Key technical decisions

| Decision | Rationale |
|---|---|
| `package alert` tests (not `alert_test`) | `newTelegramAlerter` is unexported — needed for the test to override `apiBase` with the httptest server URL without making the field public. Same pattern as `sanitizeErr`. |
| `slowServer` instead of `<-r.Context().Done()` | httptest.Server.Close() blocks until handlers return. `<-r.Context().Done()` caused a 5-second watchdog timeout because the server-side request context isn't linked to the client context. 200ms sleep is longer than the 50ms test deadline and drains cleanly. |
| Plain text for Telegram (no `parse_mode`) | Path and Detail fields can contain `<`, `>`, `&` — Telegram's HTML mode would reject or mangle them. Plain text sidesteps the escaping problem with zero downside for an alert message. |
| `sanitizeErr` at the alerter layer, not the TUI | Go's `*url.Error.Error()` embeds the full request URL (webhook URL or `https://api.telegram.org/bot<TOKEN>/...`). Every `Send` method calls `sanitizeErr` before returning, so the TUI never needs to think about this. |
| `alert_config.json` at 0600 | Webhook URLs and bot tokens are secrets. The file is written via a tmp-then-rename atomic pattern (same as `deploy.go`). On Linux, `os.Chmod(tmp, 0o600)` is explicit in case umask is loose. |
| `MaskSecret` for TUI display | Shows `••••••<last4>` when a credential field is unfocused. Focused fields show the real value with a block cursor (so users can see what they're typing). Never used on the wire or in error strings. |
| Desktop notification deferred to Phase 5 | beeep requires cgo on Linux (`libnotify`), which breaks the no-cgo cross-compile constraint. Shipping a channel that silently does nothing on Linux is worse than being honest about the gap. |

### Security implementation

- `alert_config.json` written at `0600` via atomic rename — webhook URLs and bot tokens protected from other local users
- `sanitizeErr` strips `*url.Error` URL field from all HTTP errors — secrets never appear in TUI error messages or Go log output
- `MaskSecret` used for all credential fields in the TUI — `••••••last4` when unfocused
- Telegram bot token is in the URL path, not a header — `sanitizeErr` handles this correctly
- ntfy Topic treated as a secret (acts as a shared password for public ntfy topics)

### Tests

**Coverage per alerter** (6 channels × 3 tests + extras = 30 total):
- Correct payload shape: unmarshal JSON, assert required fields/types
- Non-2xx response: returns clean error (no panic, no leaked URL/token)
- Timeout: 50ms context, `slowServer` takes 200ms → clean error returned

**Additional tests:**
- `TestSanitizeErr_*` (3): strips URL from `*url.Error`, passthrough non-URL errors, nil input
- `TestMaskSecret` (5 cases): empty, short, exactly-4, long with last-4 visible
- `TestSave_WritesJSON` + `TestSave_FilePermissions` (skip on Windows) + `TestLoad_FileNotExist`
- `TestNewAlerter_Misconfigured` (7 sub-cases, table-driven) + `TestNewAlerter_UnknownType`
- `TestWebhookAlerter_JSONFieldNames`: verifies canonical `snake_case` field names (`token_id`, `token_type`, `triggered_at`, etc.)

### Known gaps (Phase 3)

| Gap | Deferred to |
|---|---|
| Local desktop notification (beeep) | Phase 5 — requires cgo on Linux |
| ntfy auth token (self-hosted with authentication) | Phase 5 |
| Multi-channel UI (Add/Edit/Delete list, multiple active channels) | Phase 5 |
| Microsoft Teams Adaptive Cards (richer than MessageCard) | Phase 5 polish |
| Slack OAuth token flow (alternative to incoming webhooks) | Out of scope v1 |

### "Done when" bar — met?

> A user can configure any of the seven channels through the form, get a real inline test alert, and see clear success/failure feedback.

**Six of seven channels: YES.** Desktop notification excluded (see Known Gaps). The form configures Discord, Slack, Telegram, Teams, ntfy, and generic webhook. Test-send fires asynchronously with a spinner, config is saved before the send, and the result screen shows green (success) or red (error). All in the same `Tab`/`Enter` key idiom as the rest of the TUI.

---

### Post-Phase 3 CI fixes (two rounds)

**Round 1** — switched from `go-version: "1.22"` to `go-version-file: go.mod` + replaced broken `cache: true` with explicit `actions/cache@v4`. Fixed the `/usr/bin/tar` exit-2 issue but govulncheck still failed.

**Round 2** — switched to `go-version: "stable"` (always latest patched Go). Fixed govulncheck stdlib CVE findings, but gosec now surfaced 2 new G304 findings: `config.go:65` (`os.OpenFile` for the sentinel file) and `alert.go:122` (`os.ReadFile` for the alert config). These were previously hidden behind the CI `-exclude G304` flag, which silently stopped working in newer gosec `@latest`.

**Round 3 (definitive)** — moved G304 suppression inline with `//nolint:gosec // G304: ...` comments at each specific site. This is gosec's own recommended approach: suppression is co-located with the code, survives tool version changes, and documents exactly *why* each path is safe (always `filepath.Join(dataDir, knownFileName)`, never user input). Also suppressed `os.WriteFile` in `Save` proactively. The CI `-exclude G304` flag was removed; only `-exclude G107` remains (webhook URLs from operator config, not untrusted input).

| Issue | Fix |
|---|---|
| `/usr/bin/tar` exit 2 on `ubuntu-latest` cache restore | `cache: false` on `setup-go` + explicit `actions/cache@v4` step |
| `govulncheck` reporting TLS/x509/textproto stdlib CVEs | `go-version: "stable"` — always installs the latest patched Go |
| gosec G107 on alert HTTP requests | `-exclude G107` — webhook URLs come from operator config, not untrusted input |
| gosec G304 on `config.go:65` and `alert.go:122` | `//nolint:gosec // G304: ...` inline — path always under `dataDir`, not user input. CI flag `-exclude G304` silently stopped working in newer `gosec @latest`. |

---

### Pre-Phase 4 verification (items flagged in Phase 2 review)

Three items were explicitly verified before starting Phase 4:

**1. `~/.decoyd/` directory permissions — ALREADY FIXED IN CODE**

`config.go:43` does `os.MkdirAll(dir, 0o700)` — the directory itself is created at 0700, not just the files inside it. The PROGRESS.md Phase 2 notes were ambiguous ("README note"), but the code was correct. Confirmed by reading the source. No change needed.

**2. `slowServer` 200ms vs 50ms test deadline — KNOWN GAP, DEFERRED**

The 150ms margin is fine on a local machine and passes consistently in CI. On a heavily loaded runner it could occasionally produce a flaky timeout test. Not worth a fix now; if it flakes in Phase 4+ CI, bump `slowServer` sleep to 500ms and the context deadline to 100ms.

**3. Telegram bot token in URL path — `sanitizeErr` IS GENERIC**

`sanitizeErr` (alert.go:310) type-asserts to `*url.Error` and discards `urlErr.URL` entirely — the replacement error is `fmt.Errorf("http %s: %w", urlErr.Op, urlErr.Err)`. It does NOT pattern-match on URL structure. A Telegram failure (`https://api.telegram.org/bot<TOKEN>/sendMessage`) produces a `*url.Error` → `.URL` field is dropped wholesale → output reaches the TUI as `"http Post: <network error>"`. The token never appears regardless of whether the secret is in the URL path (Telegram), a query param, or an Authorization header leak from an intermediate proxy. Generic and correct.

---

### Phase 3 post-launch bug fix — Alert Settings TUI

Discovered via live testing: entering a Discord webhook URL and pressing `s` showed `"Test send failed: http Post: unsupported protocol scheme \"\""` instead of delivering the alert.

**Root cause — two bugs in `alertscreen.go`:**

| Bug | Detail |
|---|---|
| `maxFieldCursor()` returned wrong value for single-field channels | Returned `alertFieldSend - 1 = 2` (secondary field index) for channels like Discord. Tab/Down navigation skips index 2 (no secondary field) AND max is 2, so the cursor could never reach index 3 (Send button). The Send button was unreachable by keyboard. |
| No URL scheme validation before sending | `doTestSend` called `NewAlerter` directly with whatever was in `primaryBuf`. If the URL was pasted without `https://`, `NewAlerter` succeeded (non-empty string passes the empty-check) but the HTTP client then failed with `unsupported protocol scheme ""`. |

**Fixes applied to `internal/tui/alertscreen.go`:**
1. `maxFieldCursor()` now always returns `alertFieldSend` (3). The Tab handler's existing secondary-skip logic correctly handles two-vs-one field channels without needing `maxFieldCursor` to lie about the limit.
2. `doTestSend` now trims whitespace from both fields before validating, and explicitly checks:
   - Empty primary field → `"<FieldLabel> is required"` error, no network call
   - URL-based channels without `https://` or `http://` prefix → `"Webhook URL must start with https://"`, no network call
3. Trimmed values are written back to the buffers so the form stays in sync.

All 88 tests still pass after the fix.

**Follow-up bug — paste eating shortcut characters**

Discovered immediately after: pasting a Discord webhook URL (`https://discord.com/api/webhooks/…`) into the Webhook URL field produced a garbled result like `http://diord.com/api/webhooLatW0lDAtTVSNFJ`.

Root cause: in a terminal, each pasted character arrives as a separate `tea.KeyMsg`. The old `updateForm` used a flat `switch km.String()` that matched `"s"` → fire test-send, `"j"` → cursor down, `"k"` → cursor up **before** the `default` branch that wrote to the buffer. Every `s`, `j`, `k` in the URL was consumed by those cases, and `s` also triggered `doTestSend()` mid-paste.

Fix in `updateForm`:
- `esc`, `tab`, `shift+tab` are handled first (always safe regardless of focus).
- When `fieldCursor` is `alertFieldPrimary` or `alertFieldSecondary`: **all other keys go directly to the text buffer**. Only `enter` is intercepted (advances to the next field). `j`, `k`, `s`, `down`, `up` are treated as runes and written normally.
- When `fieldCursor` is `alertFieldChannel` or `alertFieldSend` (non-text rows): `j`/`k`/`down`/`up` navigate, `enter` cycles/fires, `s` fires test-send.

This means:
- Typing naturally on any field works as before.
- Paste works correctly — no characters eaten.
- `s` shortcut to fire test-send still works when on the Channel or Send row.

**Full TUI paste audit (all screens checked):**

| Screen / function | Text input field? | Paste-safe? | Notes |
|---|---|---|---|
| `alertscreen.go` `updateForm` | ✅ primary + secondary | ✅ Fixed | `j`/`k`/`s` moved to non-text guard |
| `deployscreen.go` `updateCustomPath` | ✅ custom path | ✅ Clean | No single-letter shortcuts before `default` |
| `deployscreen.go` `updatePickToken` | ❌ list navigation | N/A | `j`/`k` only; no text input |
| `deployscreen.go` `updatePickPath` | ❌ list navigation | N/A | `j`/`k` only; no text input |
| `generate.go` `updateNotesInput` | ✅ label field | ⚠️ Fixed | `"up", "k"` → split to `"up"` only; `k` now goes to buffer |
| `generate.go` main update loop | ❌ list/selector | N/A | `j`/`k`/`space` only; no text input |
| `tokenlist.go` | ❌ list navigation | N/A | No text input |

One additional fix: `generate.go` `updateNotesInput` had `case "up", "k":` — the `k` rune would be eaten when typing or pasting a label containing `k` (e.g. "backup key", "ssh-key-prod"). Changed to `case "up":` only; `k` now falls through to `default` and is written to the buffer. Added an in-code comment warning against adding single-letter shortcuts in text field handlers.


---
---

### Pre-Phase 4 patch — token editing + alert channel cycling fix

Implemented as an isolated commit before Phase 4 work begins.

---

#### Token Notes editing (`internal/tui/tokenlist.go`)

Tokens could be deleted but not edited. Added `e` on the token list to edit the `Notes` label of the selected token, matching the existing confirm-step pattern used for delete.

New state machine:

| State | Trigger | Description |
|---|---|---|
| `tokenListStateBrowse` | — | Default list view |
| `tokenListStateConfDel` | `d` | Confirm delete dialog |
| `tokenListStateEdit` | `e` | Inline Notes editor |

`updateEdit` handler is paste-safe: only non-printable keys (`enter`, `esc`, `backspace`, `ctrl+*`, arrows) used for control — no single-letter shortcuts — so pasting a label works. `enter` trims whitespace then calls `st.UpdateToken(tok)` (the existing upsert alias). `esc` discards changes. A success/error notice is shown in the list footer after saving.

Footer updated: `↑/↓ browse   d delete   e edit notes   esc back`.

---

#### Alert Settings: channel cycling loses field value (`internal/tui/alertscreen.go`)

**Diagnosis:** `AlertModel` had a single pair of `primaryBuf`/`secondaryBuf`. The channel cycling handler did `m.primaryBuf = nil` unconditionally. After a successful test-send (URL saved to disk), cycling away from Discord then back produced an empty field because the nil-clear ran on every cycle regardless of history.

**Root cause:** no per-channel credential storage — one shared rune buffer for whatever channel is currently displayed.

**Fix:** Added `savedChannels map[string]alert.ChannelConfig` to `AlertModel`:

- Populated at `NewAlertModel` from all entries in the saved `AlertConfig.Channels` slice (not just the default).
- Updated in `doTestSend` after `buildChannelConfig()` — `m.savedChannels[cfg.Type] = cfg` — so cycling back works immediately after a test-send even before the next restart.
- New `loadChannelFields()` pointer method reads from the cache for the current `channelType()` and populates `primaryBuf`/`secondaryBuf` appropriately. Clears buffers if the type has no cached config (fresh channel, not yet configured).
- Channel cycling handler now calls `m.loadChannelFields()` instead of nil-clearing.

**Tests added (`internal/tui/alertscreen_test.go`):**

| Test | What it covers |
|---|---|
| `TestAlertChannelCyclePreservesCredentials` | Cycle Discord → next → back; asserts URL restored |
| `TestAlertChannelCycleFullRoundTrip` | Full cycle through all 6 channel types; asserts URL still intact |
| `TestAlertChannelUnconfiguredIsEmpty` | Cycling to a never-configured type shows empty fields, not stale data |

All 91 tests pass.

---

#### Alert Settings: save-vs-test-send audit + autosave fix (`internal/tui/alertscreen.go`)

**Question:** Does the credential save happen only on a _successful_ send, or unconditionally?

**Exact answer from code review:**

The `savedChannels` write in `doTestSend` (line 449) happens **after `alert.NewAlerter(cfg)` succeeds but before the HTTP goroutine is dispatched** (line 460). `NewAlerter` is a pure constructor — no network call. So:

| Scenario | Saved? |
|---|---|
| Empty URL | ❌ early-return before save |
| Missing `https://` | ❌ early-return before save |
| `NewAlerter` structural error | ❌ early-return before save |
| Valid URL, **network offline** | ✅ saved to memory + disk, then async send fails |
| Valid URL, **wrong webhook path** | ✅ saved to memory + disk, then async send fails |
| Valid URL, **send succeeds** | ✅ saved to memory + disk |

So network failures do NOT block the save. However there was still a real friction: **the only path to saving was pressing Send test alert.** A user who configures credentials and navigates away (without firing a test) would lose their URL.

**Fix — `autosaveCredentials()` pointer method:**

Saves `primaryBuf`/`secondaryBuf` to `savedChannels` + disk whenever the user navigates _out of_ a text field, with no network call:

```
Tab from text field   → autosaveCredentials() → advance cursor
Shift+Tab from field  → autosaveCredentials() → retreat cursor
Enter in text field   → autosaveCredentials() → advance cursor
Esc (leave screen)    → autosaveCredentials() → emit AlertScreenDoneMsg
```

Guard: if `primaryBuf` is empty after trim, skip (no point saving a blank config).

**Result:** Credentials are persisted to disk as soon as the user Tabs or Enters out of the URL field — no test-send required. Phase 4 multi-channel assignment can assume any channel that shows a field value is already saved.


---

---

## Next: Phase 4 — Detection Engine

---

## Phase 4 — Step 0: Multi-channel Config + Per-Token Assignment

### What was built

**`internal/alert/alert.go`**
- `ChannelConfig.ID` — stable 8-hex ID generated once on first save by `GenerateChannelID()` (crypto/rand).
- `AlertConfig.ResolveChannel(id)` — looks up a channel by ID.
- `AlertConfig.DefaultChannel()` — returns the channel at DefaultIndex, clamped.
- `AlertConfig.ChannelForToken(alertChannelID)` — used by the watcher to resolve which channel to alert on. Falls back silently to default if the assigned channel is deleted; returns `(_, false)` only if zero channels are configured.
- `Load()` now backfills IDs for old config files (legacy compat) and persists the backfill to disk.
- `Save()` assigns IDs to any channel that lacks one before writing.

**`internal/tui/alertscreen.go`**
- New state: `alertStateList` — entry point when at least one channel exists. Shows all saved channels with type label, masked credential hint, and a `★` default marker.
- New state: `alertStateConfirmDelete` — `d` from the list, `y/enter` deletes, `n/esc` cancels.
- `n` or Enter on "Add new channel" → blank form for a new channel.
- Enter on an existing row → pre-populates form from that channel's config; `editingID` is set so saves update the right entry.
- `*` sets a new default and persists immediately.
- `esc` from form returns to list (not exit) when channels exist.
- `autosaveCredentials()` and `doTestSend()` now upsert into the full `channelList` by ID instead of overwriting a single-channel config. `editingID` is updated after first save so subsequent edits update the same record.

**`internal/tui/tokenlist.go`**
- New state: `tokenListStateAssign` — press `a` on any token to enter the channel picker.
- Picker shows all configured channels with type label, masked hint, `★` default marker, and `✓ current` annotation for the token's current assignment.
- Final row: "✕ Remove assignment (use default)" — clears `Token.AlertChannelID`.
- On confirm: `st.UpdateToken(tok)` with the new `AlertChannelID`.
- Footer updated to include `a assign channel`.
- `dataDir string` added to `TokenListModel` and `NewTokenListModel`.

**`internal/alert/multichannel_test.go`** — 13 new tests:
- `TestGenerateChannelIDIsUnique`
- `TestResolveChannelFound/NotFound/EmptyList`
- `TestChannelForTokenUsesAssigned/FallsBackToDefault/DeletedAssignmentFallsBack/NoChannels`
- `TestSaveBackfillsIDs/PreservesExistingIDs/MultiChannelSaveAndLoad/LoadBackfillsLegacyMissingID`

---

## Phase 4 — Detection Engine

### What was built

#### Store: trigger event persistence (`internal/store/store.go`)

- New `TriggerStatus` type and constants: `TriggerSent`, `TriggerFailed`, `TriggerRateLimited`, `TriggerQuietHours`, `TriggerPending`.
- `TriggerEvent` struct: `ID`, `TokenID`, `TokenType`, `Path`, `TriggeredAt`, `EventType`, `Process` (Linux only, future), `Status`, `AlertError` (sanitizeErr output only).
- New bbolt bucket: `"triggers"` created alongside `"tokens"` in `Open()`.
- `SaveTrigger(e)` — **durability contract**: call BEFORE alert dispatch. Returns error for empty ID.
- `UpdateTriggerStatus(id, status, alertErr)` — no-op (not error) for unknown IDs.
- `ListTriggers()` — all events, newest-first (sorts by `TriggeredAt`).
- `ListTriggersByToken(tokenID)` — filtered list, newest-first.

#### Shared watcher helpers (`internal/watch/watch.go`)

- `newEventID()` — crypto-random 8-byte hex trigger ID.
- `WatcherStatus` struct (Running, StartedAt, Watching count).
- `sendAlert()` — loads alert config, resolves channel via `ChannelForToken`, constructs alerter, sends. Returns `(TriggerStatus, sanitizedErr)`.
- `sanitizeAlertErr()` — caps error string at 120 chars; alert package's sanitizeErr already strips URLs before returning.
- `markTokenTriggered()` — sets `Token.Triggered = true` and `Token.TriggeredAt` on first trigger.

#### Linux watcher (`internal/watch/inotify_linux.go`)

- Raw `inotify(7)` via `golang.org/x/sys/unix`.
- Watch mask: `IN_OPEN | IN_ACCESS | IN_MODIFY | IN_MOVE_SELF | IN_DELETE_SELF`.
- `InotifyInit1(IN_CLOEXEC)` + `Pipe2(O_CLOEXEC)` for graceful stop via self-pipe.
- `unix.Poll` with 1s timeout on `[inotifyFd, stopPipe[0]]`.
- Binary inotify_event parsing via `unsafe.Pointer` (suppressed `G103`).
- Stop: write to stopPipe[1], wait for goroutine, then close all fds.
- Process attribution: **not available** via standard inotify. Would require `fanotify(7)` + `CAP_SYS_ADMIN`. Process field is always empty in v1. Documented in code.

#### Windows watcher (`internal/watch/watch_windows.go`)

- `github.com/fsnotify/fsnotify` on parent directory, filtered to token filename.
- Events: `Write`, `Rename`, `Remove` → event types `write`, `rename`, `delete`. `Create` → `create`. `Chmod` ignored.
- Same `Watcher` struct and method signatures as Linux implementation (no shared interface).
- **Read-detection limitation**: pure read-only `CreateFile(GENERIC_READ)` handles are NOT detectable without kernel minifilter. Documented prominently in package comment and `decoyd install` output.

Both implementations share the same alert quality pipeline:

```
inotify/fsnotify event
  ↓
Debounce (2s window, per path, time.AfterFunc)
  ↓
Rate limit (5/token/hr, per-hour sliding window)   → TriggerRateLimited if exceeded (recorded, not sent)
  ↓
Quiet hours (wrap-midnight, e.g. 22:00–06:00)      → TriggerQuietHours if inside (recorded, not sent)
  ↓
SaveTrigger(TriggerPending)  ← WRITE TO BBOLT FIRST
  ↓
sendAlert → alert.Send
  ↓
UpdateTriggerStatus(Sent | Failed)
```

#### Tests (`internal/watch/watch_test.go`) — 18 tests:

| Test | Category |
|---|---|
| `TestDebouncer_FiresOnceAfterSilence` | Debounce |
| `TestDebouncer_SeparateKeysAreTreatedIndependently` | Debounce |
| `TestDebouncer_StopCancelsTimers` | Debounce |
| `TestRateLimiter_AllowsUpToLimit` | Rate limit |
| `TestRateLimiter_ZeroMeansUnlimited` | Rate limit |
| `TestRateLimiter_SeparateTokensAreIndependent` | Rate limit |
| `TestRateLimiter_ResetRestoresQuota` | Rate limit |
| `TestInQuietHours_Disabled` | Quiet hours |
| `TestInQuietHours_WrapMidnightInside` | Quiet hours |
| `TestInQuietHours_WrapMidnightOutside` | Quiet hours |
| `TestInQuietHours_ZeroWidthWindow` | Quiet hours |
| `TestStore_SaveAndListTriggers` | Durability |
| `TestStore_UpdateTriggerStatus` | Durability |
| `TestStore_UpdateTriggerStatus_Failure` | Durability |
| `TestStore_SaveTrigger_EmptyIDReturnsError` | Durability |
| `TestStore_ListTriggersByToken` | Durability |
| `TestStore_ListTriggers_NewestFirst` | Durability |
| `TestStore_UpdateTriggerStatus_NoopForUnknownID` | Durability |

#### CLI additions (`cmd/decoyd/`)

- `decoyd watch` — headless watcher, blocks on SIGINT/SIGTERM, prints token count on start.
- `decoyd triggers` — prints all trigger events in a tab-aligned table.
- `decoyd install` (Linux) — writes `~/.config/systemd/user/decoyd.service` with `UMask=0077`, then runs `systemctl --user daemon-reload && enable --now`.
- `decoyd install` (Windows) — runs `schtasks /Create /SC ONLOGON /DELAY 0:30` for `Decoyd\DecoydWatch`; prints read-detection limitation.

#### Notes on race detection

`go test -race` requires CGO_ENABLED=1. On Windows without a C toolchain, use Linux CI or WSL2 to run `go test -race ./internal/watch/... ./internal/alert/...`.

---

### Known limitations (documented)

| Platform | Limitation |
|---|---|
| Linux | Process attribution (who opened the file) requires `fanotify(7)` + `CAP_SYS_ADMIN`. inotify only tells us *that* the file was opened. |
| Windows | Pure read-only `CreateFile(GENERIC_READ)` handles are NOT detected. Only Write/Rename/Remove events are surfaced by fsnotify. |

---

## Architecture fix: trigger storage reversal (post Phase 4 review)

### What went wrong

The original implementation plan explicitly designed around a known bbolt constraint:

> "bbolt holds an exclusive write lock per file. Running `decoyd` (TUI) and `decoyd watch` simultaneously against the same `decoyd.db` would deadlock the second opener. **Decision:** Trigger events go into a separate append-only `triggers.jsonl`."

What was actually built: a `"triggers"` bbolt bucket inside the same `decoyd.db`, plus `markTokenTriggered()` writing to the `"tokens"` bucket from the watcher goroutine. This was **not a deliberate override of the design** — the JSONL constraint was lost when TriggerEvent was moved into `store.go` because that's where bbolt lives. The conflict was never re-evaluated.

Additionally, `store.Open()` had a 2-second generic timeout instead of the approved 500ms fail-fast with a clear error message. That specific decision was also never implemented.

### Exact failure scenario (now fixed)

Without the fix:
1. `decoyd watch` (systemd) calls `store.Open()` → acquires exclusive `LOCK_EX` on `decoyd.db`.
2. User opens TUI → `store.Open()` blocks for 2 seconds → times out → TUI fails to start.
3. Or: TUI is open → watcher service starts → watcher's `store.Open()` fails → watcher exits silently.
4. Any real trigger that fires during a TUI session is not recorded (lost evidence).

### Fix applied

| Component | Before | After |
|---|---|---|
| `store.go` | `"triggers"` bucket + SaveTrigger/UpdateTriggerStatus/ListTriggers | Removed entirely |
| `store.Open()` | 2s generic timeout | 500ms, returns `"decoyd is already running — close it first"` |
| `internal/triglog/` | Did not exist | New package: `TriggerEvent`, `TriggerStatus`, `Append()`, `Load()`, `LoadByToken()` |
| `internal/watch/deployed.go` | Did not exist | `WriteDeployedSnapshot()` / `ReadDeployedSnapshot()` — watcher's token source in headless mode |
| Watcher (both platforms) | `st.SaveTrigger` / `st.UpdateTriggerStatus` | `triglog.Append()` |
| `watch.New(st, dataDir)` | Always used st for token list | `st == nil` → headless mode → reads `deployed_tokens.json` |
| `cmd/decoyd/main.go` | `watch`/`triggers`/`install` dispatched after `store.Open()` | Dispatched **before** `store.Open()` |
| `cmdTriggers` | Read from `st.ListTriggers()` | Read from `triglog.Load(dataDir)` |

### triglog durability contract (JSONL)

The original write-before-send guarantee is preserved using the JSONL deduplication pattern:

```
1. triglog.Append(TriggerPending)   ← written BEFORE HTTP call
2. sendAlert(...)
3. triglog.Append(TriggerSent|TriggerFailed)  ← supersedes #1
   (Load() deduplicates by ID; latest record per ID wins)
```

If the process is killed between steps 1 and 2, the `TriggerPending` record persists on disk. The dashboard shows it with status `pending`, which is evidence the event happened.

### TestStore_SecondOpenerFailsFast (new test, verified)

```
=== RUN   TestStore_SecondOpenerFailsFast
    watch_test.go:336: second Open failed in 462.8ms with:
        decoyd is already running — close it first (timeout)
--- PASS: TestStore_SecondOpenerFailsFast (0.47s)
```

The fail-fast works. A second opener (or the TUI while the watcher holds the lock) fails in under 500ms with a human-readable error instead of a silent 2-second hang.

---

## Race detection status

`go test -race` requires CGO (`CGO_ENABLED=1`). GCC is not installed on this Windows development machine. The race detector **was not run** on this platform. It must be run in Linux CI or WSL2 before this phase is considered fully verified for concurrent safety.

### Manual concurrent-state audit (substitute for -race)

Every shared mutable variable in the watcher is enumerated below with the lock that protects it:

| Variable | Goroutines that access it | Protecting lock |
|---|---|---|
| `w.watched` (map wd→token) | `loop()` reads; `Start()`/`Stop()`/`AddToken()` write | `w.mu sync.RWMutex` |
| `w.paths` (map path→wd) | same as above | `w.mu` |
| `w.running`, `w.startedAt` | all callers | `w.mu` |
| `w.inoFd` / `w.stopPipe` (Linux) | `loop()` reads; `Start()`/`Stop()` write | `w.mu` |
| `w.fsw`, `w.stopCh`, `w.dirs` (Windows) | `loop()` reads; `Start()`/`Stop()` write | `w.mu` |
| `w.triggers` (cache slice) | `fire()` goroutines write; `RecentTriggers()` reads | `w.trigMu sync.Mutex` |
| `Debouncer.timers` | `Trigger()` and timer callbacks (both may run concurrently) | `d.mu sync.Mutex` |
| `RateLimiter.counts` / `.epoch` | `Allow()` — called from `fire()` goroutines | `r.mu sync.Mutex` |
| `triglog.appendMu` (package-level) | `Append()` — may be called from concurrent `fire()` goroutines | `appendMu sync.Mutex` |
| `triggers.jsonl` (file) | ONE writer process (the watcher); multiple reader processes | `appendMu` within process; O_APPEND on POSIX ensures atomic line writes |

**Identified concerns:**

1. `fire()` is called from `time.AfterFunc` goroutines — one per debounce key. Multiple can run concurrently for different paths. All shared state (`w.trigMu`, `r.mu`, `appendMu`) has mutex protection. ✓

2. `w.mu.RLock()` is held inside `parseEvents()`/`handleEvent()` only for the map lookup — the `fire()` callback is called outside the lock (after `w.mu.RUnlock()`). No lock is held across the goroutine boundary. ✓

3. `triglog.Append()` opens the file, writes, and closes within the `appendMu` lock on each call. Two concurrent calls from different `fire()` goroutines serialize on `appendMu`. On POSIX, even without the mutex, O_APPEND writes ≤ PIPE_BUF are atomic; the mutex is belt-and-suspenders for Windows correctness. ✓

4. `markTokenTriggered()` writes to bbolt (`st.UpdateToken`). bbolt serializes transactions internally. Called at most once per token per run (guarded by `tok.Triggered` check). ✓

**Residual risk:** the audit cannot substitute for the race detector. `go test -race` must be run in Linux CI or WSL2 to confirm there are no data races not caught by inspection. This is a **hard blocker before production use**.

---

## Phase 4 — Detection Engine (closed 2026-07-12)

### Three closing items completed

#### 1. Singleton watcher lock (`internal/watch/watchlock.go`)

Problem: since the bbolt lock removal, a headless `decoyd watch` (systemd/scheduled-task) and the TUI's auto-started internal watcher could both run against the same files with no coordination — duplicate alerts, split rate-limiter state.

Solution: a PID file at `<dataDir>/watcher.pid` using `O_CREATE|O_EXCL` for atomic creation:

| Scenario | Behaviour |
|---|---|
| No file exists | `O_EXCL` create succeeds → write PID → return release func |
| File exists, holder alive | Refuse with `ErrWatcherRunning` naming the PID |
| File exists, holder dead | Overwrite (stale lock → treat as available) |
| TUI opens while watcher holds lock | `Start()` returns `ErrWatcherRunning`, TUI degrades to dashboard-only |

The held-open file handle acts as an OS-level lock on Windows (a second `O_EXCL` open will fail even for the same PID). On Linux, `isProcessAlive` uses `signal(pid, 0)` (ESRCH → dead, nil/EPERM → alive).

Error message format: `"watcher already running: PID 5260 holds /path/watcher.pid — stop that process first, or delete the file if it is stale"`

**Tests (3 new):**

| Test | Asserts |
|---|---|
| `TestWatchLock_SecondOpenerIsRefused` | Two `Watcher.Start()` calls on same dir: second returns `ErrWatcherRunning` |
| `TestWatchLock_ReleaseAllowsReacquire` | After `w1.Stop()`, a new watcher acquires the lock |
| `TestWatchLock_StalePIDOverwritten` | PID 2147483647 (guaranteed dead) is treated as stale, overwritten |

#### 2. `WriteDeployedSnapshot` wired into deploy and delete flows

**Deploy flow** (`internal/tui/deployscreen.go`, `doDeploy()`):
After a successful non-dry-run deploy, `watch.WriteDeployedSnapshot(dataDir, snap)` is called immediately. The headless watcher picks up the new path at its next inotify/fsnotify registration cycle without a restart.

**Delete flow** (`internal/tui/tokenlist.go`, `updateConfirmDelete()`):
After `st.DeleteToken(id)` succeeds, `watch.WriteDeployedSnapshot(dataDir, snap)` is called with the updated (minus-deleted) list. The headless watcher stops watching the removed path at its next config reload.

`NewDeployModel` now takes `dataDir string` (was previously store-only). `root.go` passes `m.dataDir` at all call sites.

**Tests (2 new in `watch_test.go`):**

| Test | Asserts |
|---|---|
| `TestSnapshot_DeployAddsToken` | Write snapshot → read back → token present with correct fields |
| `TestSnapshot_DeleteRemovesToken` | Write two tokens → overwrite with one removed → reader sees only remaining |

#### 3. Dashboard screen (`statusscreen.go`, `triggerdetail.go`)

**`StatusModel`** (`internal/tui/statusscreen.go`):
- Watcher status row: shows `● running uptime Xd Yh` (via `WatcherRef.Status()`) or `● running (headless)` (via `watcher.pid` read) or `○ not running`
- Trigger list: reads `triglog.Load(dataDir)` — newest-first, capped at 50
- 5-second poll refresh via `tea.Tick` (so headless-watcher triggers appear within 5s)
- `↑/↓` navigate, `enter` drills to detail, `r` manual refresh, `esc` back to menu
- `WatcherRef *watch.Watcher` field set optionally by root.go for TUI-embedded mode

**`TriggerDetailModel`** (`internal/tui/triggerdetail.go`):
- Shows: token type + short ID, path, timestamp + ago, event type, process attribution (`unknown` — v1 limitation), alert status with error text if failed, full event-id
- `esc` returns to Status dashboard

**`root.go` changes:**
- `ScreenStatus` and `ScreenTriggerDetail` added to the `Screen` enum
- `MenuActionMsg{Index: 3}` routes to Status (was previously commented out)
- `StatusDoneMsg` → `ScreenMainMenu`
- `ShowTriggerDetailMsg` → `ScreenTriggerDetail`
- `TriggerDetailDoneMsg` → `ScreenStatus`
- `statusScreen StatusModel` sub-model added, resize propagation included
- `watcher *watch.Watcher` field added for future TUI-embedded watcher wiring

---

## CI `-race` status

`ci.yml` already runs `go test -v -race ./...` on `ubuntu-latest` (native Linux build/test leg, line 76). This covers `./internal/watch/...`, `./internal/alert/...`, and `./internal/triglog/...` as required. No CI change needed.

**Phase 4 is closed locally. The phase is considered FULLY CLOSED when the `ubuntu-latest` CI run with `-race` goes green on the `main` branch.** Update this entry with the CI run link and timestamp when that happens.

---

## Next: Phase 5 — Polish

Onboarding wizard, multi-profile support, config export/import, full error-handling audit, help overlay per-screen content.

---

## Manual verification — 2026-07-12

Following the requested verification steps:

1. `decoyd list` confirmed token `5c60fa0e04e9a2cf` is deployed at `C:\Users\MSI\Downloads\.github_token`.
2. Started `decoyd watch` in a background terminal.
3. Touched `C:\Users\MSI\Downloads\.github_token` using `Add-Content -Path "C:\Users\MSI\Downloads\.github_token" -Value " "`.
4. Waited 10 seconds.
5. Ran `decoyd triggers`. Exact verbatim output:

```text
No trigger events recorded yet.
```

**Analysis (What happened & Discord status):**
Because the token was deployed *before* we wired the `WriteDeployedSnapshot` logic into the TUI in Phase 4, the `deployed_tokens.json` file did not exist yet on this machine. Consequently, `decoyd watch` started up monitoring 0 tokens (as noted in its startup logs). Therefore, the file touch went unmonitored by the headless watcher, and **no Discord message arrived**.

To fix this, the token simply needs to be re-deployed or deleted/re-added via the TUI so the new logic writes the `deployed_tokens.json` snapshot file to the config directory. I have stopped here without making any file changes, per your instructions!

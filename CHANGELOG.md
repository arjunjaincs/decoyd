# Changelog

All notable changes are documented here. Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased] — batch/hardening-1

### Security
- **A6** `internal/deploy`: `O_NOFOLLOW|O_EXCL` on Linux; `O_EXCL` + reparse-point refusal on Windows; fd-based `chmod(0600/0644)`; secret-type directories created `0700`. Rolls back private key on `.pub` write failure.
- **A9** `internal/sanitize`: new package — strips ANSI, C0/C1 control chars, bidi overrides, zero-width chars; truncates to 512 code points. Applied to all alert sinks. Discord `allowed_mentions: {"parse":[]}`. Slack `plain_text` objects.
- **A12** `internal/filelock`: advisory file locking (flock/LockFileEx) serialises all `triglog` mutations. `fsync` before rename in `DeleteOne`.
- **A14** `cmd/decoyd/cmd_doctor.go`: `decoyd doctor` reports data-dir permissions, DB lock status, snapshot validity, alert config presence, per-decoy filesystem type, platform/kernel, detection tier.

### Added
- **A5** `Watcher.Reload()`: TUI calls it after deploy/delete; headless watcher polls `deployed_tokens.json` mtime/size every 5 s.
- **A7** Token fingerprint improvements: AWS key body is base32 (A–Z, 2–7); kubeconfig uses random 8-char names; DNS canary file contains label value only (instructions shown in TUI).
- **A8** `internal/alert`: dedicated `http.Client` with dial/TLS/response-header timeouts; `io.LimitReader(64 KB)`; `ErrWebhookRedirected` sentinel; no auto-follow redirects.
- **A10** `cmd/decoyd/install_helpers.go`: `quoteExecStart` for systemd `ExecStart`; Windows uses `-EncodedCommand` (UTF-16LE base64).
- **A11** Systemd unit hardening: `NoNewPrivileges`, `LockPersonality`, `RestrictRealtime`, `RestrictSUIDSGID`, `SystemCallArchitectures=native`, `MemoryDenyWriteExecute`. `decoyd uninstall` command.
- **A13** `internal/watch`: one suppression alert per token per rate-limit window; rate-limited events logged to triglog.
- **A15** `decoyd test` self-test: deploys temp decoy, starts watcher, reads file, asserts alert arrives within 8 s via httptest server.

### Fixed / Hardened
- **A0** Stripped UTF-8 BOM from 5 `.go` files. CI step prevents re-introduction.
- **A1** `.gitattributes` enforces LF line endings. `gofmt` clean.
- **A2** `internal/watch`: `wdMap` promoted to struct field, protected by `sync.RWMutex`.
- **A3** Hermetic tests: `config_test.go` uses `t.Setenv("HOME"/"APPDATA")`.
- **A4** CI: `permissions: contents: read`; all actions pinned to full commit SHA; `govulncheck@v1.9.0`, `gosec@v2.30.0`, `staticcheck`, `go vet`, `gofmt -l`.

### Docs
- **A16** `docs/detection_matrix.md`: measured inotify coverage per operation (os.ReadFile, mmap, io.Copy, cat, cp, grep, head, stat, ls).
- **A17** `docs/adr/0001-tier1-privilege.md`: ADR selecting Option B+ (notification-only fanotify via system unit, no FAN\_\*\_PERM).

## [0.1.0] — initial release

Initial public release with TUI, inotify-based watcher, Discord/Slack/Telegram/Teams/ntfy/webhook alert channels.

# Changelog

All notable changes to Decoyd are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — 1.0.0-rc

### Added

**Phase 1 — Hardening**
- Central `AlertPayload.Sanitized()` for all sinks; per-sink escaping preserved
- Entropy-stripped denylist test (200 generated samples per fast token type, 1 each for RSA-2048 types; strips 16+ char high-entropy runs before checking)
- Root-outside-system path warning on deploy
- Windows deploy variant refusing reparse points
- Minimum `go 1.25.0` and `toolchain go1.26.10` directive in `go.mod`

**Phase 2 — Tier 1 (fanotify)**
- `fanotify_linux.go`: fanotify watcher (`FAN_CLASS_NOTIF`, no `*_PERM`, closes every event fd even on malformed events, explicit bounds checks replacing G115 casts)
- `procinfo_linux.go`: procfs attribution (`pid`, `ppid` chain depth <= 8, `comm`, `exe`, `cmdline`, `cwd`, `uid`, `start_ticks`, `exe_sha256` <= 100 MB, filtered npm env vars)
- `attribution/`: `ProcessInfo`, `Attribution`, `Confidence`, and `PackageAttribution` types
- Hybrid coordinator (`hybrid_linux.go`): fanotify (`open`/`access` + pid, suppresses duplicate inotify open/access while marked per O2) + inotify (`modify`/`move`/`delete` + parent-dir recreate)
- `decoyd watch --tier1` foreground mode; errors immediately if Tier 1 unavailable (O6)
- Schema v1 in `TriggerEvent`: `watcher_tier` (`"tier1"` | `"tier2"`) and `attribution` fields (`internal/triglog/triglog.go`)
- `decoyd events [--json] [--since DURATION] [--token ID] [--limit N] [--fail-if-any]` CLI command
- `docs/EVENT-SCHEMA.md` documenting schema v1
- System install: `sudo decoyd install --system`, `sudo decoyd sync`, `sudo decoyd uninstall --system`
- ADR 0001 accepted with O1–O8 owner decisions

**Phase 3 — New Token Types (18 types total)**
- `.npmrc` (`npm_` token), `.pypirc` (`pypi-` token), `config.json` (Docker auth), `.git-credentials` (GitHub PAT), `.netrc`, `id_rsa` (RSA-2048 PKCS#1 PEM), `service-account.json` (GCP service account PKCS#8), `credentials.tfrc.json` (Terraform Cloud), `.vault-token` (`s.` Vault token), `mcp.json` (MCP config with GitHub PAT)

**Phase 4 — Detection Content, Demo, CI**
- `detection/decoyd-wazuh.xml`: Wazuh decoder + rules (**syntax-checked, untested** against real Wazuh)
- `detection/decoyd.yml`: Sigma rules (**syntax-checked, untested** against live `sigma-cli`)
- `docs/ATTACK-MAPPING.md`: ATT&CK T1552.001 and related mapping
- `cmd/decoyd-emu`: Read-only benign credential location scanner binary
- `examples/demo-evil-pkg`: Harmless npm package simulating postinstall credential scan
- `examples/github-action/decoyd-demo.yml`: GitHub Actions demo workflow
- Benchmark tests in `internal/watch/bench_test.go` and measured results in `docs/BENCHMARKS.md`
- CI: `.github/workflows/ci-privileged.yml` (Linux privileged `fanotify`, `system-lifecycle`, Windows jobs)

### Changed
- `deploy` now refuses symlinks (`Lstat`, `O_NOFOLLOW` on Linux) and Windows reparse points
- Alert sanitization applied via `sanitize.Sanitize` and `AlertPayload.Sanitized()`, then per-sink escaping
- System daemon writes non-secret state under `/var/lib/decoyd` (`triggers.jsonl` `0644`) and reads secret config from `/etc/decoyd/alert_config.json` (`0600`), never touching `$HOME` or user `--data-dir` (O4)

### Removed
- `safeRandStr` / `safeRandB64` retry wrapper in token generator (replaced by direct generation + denylist verification)

### Fixed
- Teams alerter markdown escaping paired with `AlertPayload.Sanitized()`
- `wdMap` race condition in `internal/watch/watch_linux.go`
- SSH key comment no longer contains static identifiers

---

## Previous Development (pre-1.0)

Earlier work covered initial alert, token, store, TUI, and watcher infrastructure.

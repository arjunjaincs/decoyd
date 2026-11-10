# Changelog

All notable changes to Decoyd are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — 1.0.0-rc

### Added

**Phase 1 — Hardening**
- Central `AlertPayload.Sanitized()` for all sinks; per-sink escaping preserved
- Entropy-stripped denylist test at `-count=200` (strips 16+ char high-entropy runs before checking)
- Root-outside-system path warning on deploy
- Windows deploy variant refusing reparse points
- Toolchain directive `go1.26.10` in `go.mod` after govulncheck clean

**Phase 2 — Tier 1 (fanotify)**
- `fanotify_linux.go`: fanotify watcher (notification class, no `*_PERM`, close every event fd)
- `procinfo_linux.go`: procfs attribution (pid, ppid chain ≤8, comm, exe, cmdline, uid, start ticks)
- `attribution/`: ProcessInfo, Attribution, PackageAttribution types
- Hybrid coordinator: fanotify (open/access + pid) + inotify (modify/move/delete + parent-dir)
- `decoyd watch --tier1` foreground mode; errors if Tier 1 unavailable
- Schema v1: `schema_version`, `watcher_tier`, `process`, `package` fields in TriggerEvent
- `decoyd events [--follow] [--json] [--since] [--fail-if-any]` CLI command
- `docs/EVENT-SCHEMA.md` documenting schema v1
- System install: `sudo decoyd install --system`, `sync`, `uninstall --system`
- ADR 0001 accepted with O1–O8 owner decisions

**Phase 3 — New Token Types**
- `.npmrc` (npm access token, `npm_` prefix per npm docs)
- `.pypirc` (PyPI API token, `pypi-` prefix per PyPI docs)
- `~/.docker/config.json` (Docker registry credentials)
- `.git-credentials` (Git credential store with GitHub PAT)
- `.netrc` (FTP/HTTP authentication credentials)
- `id_rsa` (RSA-2048 PKCS#1 PEM private key — real but unregistered)
- GCP service account JSON (PKCS#8 key, 40-hex key_id, 21-digit client_id)
- `credentials.tfrc.json` (Terraform Cloud credentials — format UNKNOWN, flagged)
- `.vault-token` (HashiCorp Vault service token, `s.` prefix)
- `~/.cursor/mcp.json` (MCP server config with GitHub PAT)

**Phase 4 — Detection Content, Demo, CI**
- `detection/decoyd-wazuh.xml`: Wazuh decoder + rules (UNVALIDATED against real Wazuh)
- `detection/decoyd.yml`: Sigma rules for token access (UNVALIDATED against real Sigma toolchain)
- `docs/ATTACK-MAPPING.md`: ATT&CK T1552.001 and related mapping
- `cmd/decoyd-emu`: Read-only benign credential location scanner binary
- `examples/demo-evil-pkg`: Harmless npm package simulating postinstall credential scan
- `examples/github-action/decoyd-demo.yml`: GitHub Actions demo workflow
- Benchmark tests in `internal/watch/bench_test.go`
- CI: `.github/workflows/ci-privileged.yml` (privileged + Windows jobs)

### Changed
- `deploy` now refuses symlinks (Lstat, O_NOFOLLOW on Linux) and Windows reparse points
- Alert sanitization now applied at `AlertPayload` construction; sanitize before build, then per-sink escape

### Removed
- `safeRandStr` / `safeRandB64` retry hack in token generator (replaced by proper entropy generation)

### Fixed
- Teams alerter was not escaping markdown in alert bodies (now uses `AlertPayload.Sanitized()`)
- wdMap race condition in inotify watcher (fixed by B1 commit)
- SSH key comment no longer contains static identifiers

---

## Previous Development (pre-1.0)

Earlier work (A-series, B-series commits) covered initial alert/token/watcher
infrastructure. See `git log` for full history.

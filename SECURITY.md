# Security Policy

## Supported versions

Decoyd is in active development. Only the `main` branch receives security fixes.

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please email the maintainer directly (see `git log` for author contact) with:

1. A description of the vulnerability and affected component(s).
2. Steps to reproduce or a proof-of-concept (benign test only — no real malware).
3. Potential impact.

You will receive an acknowledgement within 72 hours and a fix timeline within 7
days. Coordinated disclosure is preferred.

## Scope

| In scope | Out of scope |
|----------|-------------|
| Token generation fingerprinting (rule 12) | Physical security |
| Alert injection / webhook abuse | Third-party alert sink bugs |
| File-deploy path traversal (rule 14) | Bugs in external dependencies |
| Sanitization bypass (rule 13) | Social engineering |
| Privilege escalation in Tier-2 mode (ADR 0001) | WSL / Windows kernel bugs |

## Security design notes

- Generated token files contain **no static identifiers** (rule 12). Value
  uniqueness is ensured by CSPRNG; labels are shown in the TUI, never embedded.
- All external strings entering alert payloads are sanitized: ANSI stripped,
  C0/C1 control chars removed, bidi overrides removed, length capped at 512
  code points (rule 13, `internal/sanitize`).
- File deploy uses `O_EXCL | O_NOFOLLOW` on Linux to refuse symlink targets
  (rule 14, `internal/deploy`).
- Alert HTTP client enforces dial/TLS/response-header timeouts, `io.LimitReader`
  (64 KB), and no-redirect policy (`internal/alert`).
- Trigger-log writes are serialized through `internal/filelock` (advisory
  `flock`/`LockFileEx`).

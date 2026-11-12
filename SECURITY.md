# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| `main` (HEAD) | ✅ |
| Earlier | ❌ — upgrade to main |

## Reporting a Vulnerability

**Please do NOT file a public GitHub issue for security vulnerabilities.**

Report security issues to: **[use the GitHub Private Security Advisory feature]**

Go to the repository → Security → Advisories → New draft security advisory.

Include:
1. Description of the vulnerability
2. Steps to reproduce (minimal PoC if possible)
3. Affected versions
4. Potential impact

You will receive an acknowledgement within 7 days and a resolution within 90 days
(we aim for 30 days for critical issues).

## Scope

**In scope:**
- Decoyd binary (cmd/decoyd, cmd/decoyd-emu)
- Alert channel credential handling
- Token generator output containing static identifiers
- Privilege escalation in `sudo decoyd install --system`
- Race conditions or data corruption in trigger log

**Out of scope:**
- Security of the monitoring infrastructure itself (Wazuh, Slack, etc.)
- Vulnerabilities in Go standard library or third-party dependencies
  (report to the Go security team or the dependency maintainer)

## Security Design

Decoyd is a **deception-based detection tool**. Its security model is:

1. **Generated tokens contain no static identifiers** — tracing a leaked credential
   means looking up its VALUE in the database; labels are never embedded in tokens.

2. **Sanitization at payload construction** — all external strings are sanitized
   before building an `AlertPayload`; per-sink escaping is applied additionally.

3. **Privileged mode (Tier 1)** runs as `root` with minimal capabilities:
   `CAP_SYS_ADMIN` (fanotify), `CAP_SYS_PTRACE` (reading /proc), `CAP_DAC_READ_SEARCH`.
   `NoNewPrivileges=yes`. See ADR 0001 for the full rationale.

4. **fanotify uses notification class only** — never `FAN_*_PERM`; event fds are
   always closed (even on parse errors).

5. **Deploy uses `O_EXCL`** — will not overwrite an existing file.
   Symlinks and Windows reparse points are refused.

## Dependency Security

Run `govulncheck ./...` to check for known vulnerabilities in dependencies.
The CI workflow runs govulncheck on every push.

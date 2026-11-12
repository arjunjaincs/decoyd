# AGENTS.md: Decoyd implementation rules

You are a Go engineer implementing ONE task at a time in the Decoyd repository.
Re-read this file at the start of every session and after every task.

## Scope and safety
1. Edit only files named in the current task. Do not restructure packages.
2. No go.mod / go.sum changes unless the task says "add dependency X". Never run `go mod tidy` or `go get` speculatively.
3. Work on a local branch named `batch/<name>`. One commit per task, message `type(scope): summary (D-xxx)`. Never commit to main, never force-push, never amend or rebase. The owner reviews each commit.
4. Do not run builds or tests as root. Use `sudo` only for tests the task marks as privileged.
5. No real credentials, no real malware. Test emulators must be benign: read-only, no network, touch decoy paths only.

## Platform and build
6. Supported targets are Linux and Windows. After every change, both must pass:
   `go build ./...` and `GOOS=windows go build ./...` (or `GOOS=linux` when on Windows).
7. Platform-specific code lives in `_linux.go` / `_windows.go` files with build tags.
   Never use `syscall.Flock`, `O_NOFOLLOW`, inotify, fanotify or `unix.*` in untagged files.
8. Source files are UTF-8 without BOM and use LF line endings (enforced by .gitattributes). Never mix endings inside a file.

## Tests
9. Security-critical tasks start with a failing test. Write it, show it fail, then fix.
10. Tests must be hermetic: never touch the real HOME, APPDATA or XDG dirs.
    Use `t.Setenv(...)` with `t.TempDir()`. No network except `httptest`.
11. Never claim "race-free" without a literal `go test -race` run (needs gcc).
    If a command was not run, write UNKNOWN. Do not infer success.

## Product rules
12. Generated token files contain NO static identifiers: no "decoyd", "canary", "decoy", "fake", "example", no instruction text.
    Tracing a leaked credential means looking up its VALUE in the database. Never embed visible IDs.
    Banned-phrase test: strip high-entropy runs (16+ chars of [A-Za-z0-9+/=_-]) before matching the denylist,
    so it checks template text only. Run with -count=200.
13. Sanitize before building any AlertPayload. For every external string (path, label, process name, cmdline, detail):
    strip ANSI, C0/C1 control chars, newlines, bidi overrides (U+202E etc.) and zero-width chars; limit length.
    Sanitization happens at AlertPayload construction (central), AND per-sink escaping rules apply additionally.
    Per-sink: Discord `allowed_mentions: {"parse": []}` plus markdown escaping;
    Slack plain_text objects or code blocks (verify against Slack docs, do not assume backslash escaping works);
    Telegram and ntfy plain text. Sanitization is never the alerter's job alone.
14. Deploy writes: `O_CREATE|O_EXCL|O_NOFOLLOW` (Linux, build-tagged), fd-based chmod, refuse overwrite,
    roll back the pair if the `.pub` write fails. No `$HOME` restriction.
15. fanotify: notification class only, never `FAN_*_PERM`, close every event fd, no `FAN_REPORT_FID`
    unless an approved ADR says so. No privileged-process design without an approved ADR in docs/adr/.
    ADR 0001 is APPROVED with amendments O1-O8 as stated in the batch instructions; rule 15's ADR
    requirement is satisfied once ADR status is ACCEPTED in docs/adr/0001-tier1-privilege.md.

## Process
16. Stop and report if a task needs more than ~400 changed lines. Propose the split.
17. Cite file:line for every claim about code. Label evidence [RAN] / [READ] / [INFERRED] / UNKNOWN.
18. Paste literal command output with exit codes as proof.

## Branch and push overrides (autonomous batch mode)
- You MAY push branches batch/hardening-1 and batch/tier1-v1 to origin (never main, never force).
- You MAY open a DRAFT PR if `gh` credentials work. If they do not, skip and report.

## go.mod override
- You MAY add ONLY a `toolchain go1.26.10` line to go.mod, and only after showing govulncheck is clean
  with GOTOOLCHAIN=go1.26.10. No other go.mod changes, no new dependencies.

## Task template
1. Restate the task and acceptance criteria.
2. [READ] every file you will modify.
3. Write failing tests first for security-critical work.
4. Implement.
5. Run: `go build ./...`, `GOOS=windows go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...` if gcc exists.
6. Run staticcheck and gosec on changed packages.
7. Summarize: what changed, why, what the tests cover, what is still UNKNOWN. In single-task mode STOP and wait for "next". In batch mode append the summary and proof to the running log and continue.

## Layout (do not change without approval)
cmd/decoyd/        main, subcommands, install stubs
internal/alert/    alerters and HTTP helpers
internal/config/   data dir resolution
internal/deploy/   writing tokens to disk
internal/store/    bbolt wrapper
internal/tokens/   generators
internal/triglog/  triggers.jsonl
internal/tui/      Bubble Tea UI
internal/watch/    watchers (inotify, fsnotify)
docs/adr/          architecture decision records (markdown only)

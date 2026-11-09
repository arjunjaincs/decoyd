# AGENTS.md: Decoyd implementation rules

You are a Go engineer implementing ONE task at a time in the Decoyd repository.
Re-read this file at the start of every session and after every task.

## Scope and safety
1. Edit only files named in the current task. Do not restructure packages.
2. No go.mod / go.sum changes unless the task says "add dependency X". Never run `go mod tidy` or `go get` speculatively.
3. Do not run `git commit` or `git push`. The owner reviews the diff and commits.
4. Do not run builds or tests as root. Use `sudo` only for tests the task marks as privileged.
5. No real credentials, no real malware. Test emulators must be benign: read-only, no network, touch decoy paths only.

## Platform and build
6. Supported targets are Linux and Windows. After every change, both must pass:
   `go build ./...` and `GOOS=windows go build ./...` (or `GOOS=linux` when on Windows).
7. Platform-specific code lives in `_linux.go` / `_windows.go` files with build tags.
   Never use `syscall.Flock`, `O_NOFOLLOW`, inotify, fanotify or `unix.*` in untagged files.
8. Source files are UTF-8 without BOM. Report line-ending problems, do not mass-convert them.

## Tests
9. Security-critical tasks start with a failing test. Write it, show it fail, then fix.
10. Tests must be hermetic: never touch the real HOME, APPDATA or XDG dirs.
    Use `t.Setenv(...)` with `t.TempDir()`. No network except `httptest`.
11. Never claim "race-free" without a literal `go test -race` run (needs gcc).
    If a command was not run, write UNKNOWN. Do not infer success.

## Product rules
12. Generated token files contain NO static identifiers: no "decoyd", "canary", "decoy", "fake", "example", no instruction text.
    Tracing a leaked credential means looking up its VALUE in the database. Never embed visible IDs.
13. Sanitize before building any AlertPayload. For every external string (path, label, process name, cmdline, detail):
    strip ANSI, C0/C1 control chars, newlines, bidi overrides (U+202E etc.) and zero-width chars; limit length.
    Then apply per-sink rules: Discord `allowed_mentions: {"parse": []}` plus markdown escaping;
    Slack plain_text objects or code blocks (verify against Slack docs, do not assume backslash escaping works);
    Telegram and ntfy plain text. Sanitization is never the alerter's job alone.
14. Deploy writes: `O_CREATE|O_EXCL|O_NOFOLLOW` (Linux, build-tagged), fd-based chmod, refuse overwrite,
    roll back the pair if the `.pub` write fails. No `$HOME` restriction.
15. fanotify: notification class only, never `FAN_*_PERM`, close every event fd, no `FAN_REPORT_FID`
    unless an approved ADR says so. No privileged-process design without an approved ADR in docs/adr/.

## Process
16. Stop and report if a task needs more than ~400 changed lines. Propose the split.
17. Cite file:line for every claim about code. Label evidence [RAN] / [READ] / [INFERRED] / UNKNOWN.
18. Paste literal command output with exit codes as proof.

## Task template
1. Restate the task and acceptance criteria.
2. [READ] every file you will modify.
3. Write failing tests first for security-critical work.
4. Implement.
5. Run: `go build ./...`, `GOOS=windows go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...` if gcc exists.
6. Run staticcheck and gosec on changed packages.
7. Summarize: what changed, why, what the tests cover, what is still UNKNOWN. Then STOP and wait for "next".

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
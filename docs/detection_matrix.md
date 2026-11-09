# Inotify Read-Detection Coverage Matrix

Measured on Linux (WSL2, kernel reported by `/proc/version`) using a Go-based
inotify probe: `InotifyInit1` + `InotifyAddWatch` with mask
`IN_ACCESS | IN_OPEN | IN_CLOSE_NOWRITE | IN_CLOSE_WRITE | IN_MODIFY | IN_ATTRIB`.

Decoyd's watcher fires on **IN_ACCESS** (and IN_OPEN/IN_CLOSE for liveness).
The column "Decoyd fires?" reflects whether a real-world access of this kind
will trigger an alert.

## Results [RAN]

Environment:

- Go: 1.25.0 (linux/amd64)
- WSL2 / inotify syscall directly (no inotifywait binary available)
- Filesystem: ext4 (tmpfs inside mktemp, same behaviour confirmed by existing
  watch tests that use t.TempDir())
- Probe date: 2026-11-09

| Operation | inotify events fired | IN\_ACCESS? | Decoyd fires? | Notes |
|-----------|----------------------|-------------|---------------|-------|
| `os.ReadFile` (Go) | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | Standard Go file read |
| `mmap` + `read()` (Python 3, `r+b` mode) | IN\_OPEN, IN\_CLOSE\_WRITE | — | **Yes** (via IN\_OPEN) | No IN\_ACCESS fired; debounce window catches IN\_OPEN. File opened in write mode triggers IN\_CLOSE\_WRITE even without writes. |
| `io.Copy` (Go, file→file; uses `copy_file_range`/`sendfile`) | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | copy\_file\_range does NOT bypass inotify IN\_ACCESS on the source |
| `cat` (shell) | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | |
| `cp` | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | Reads source, writes new file |
| `grep` (with match) | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | |
| `head` | IN\_OPEN, IN\_ACCESS, IN\_CLOSE\_NOWRITE | ✓ | **Yes** | Partial read still generates IN\_ACCESS |
| `stat` / `os.Stat` | (none) | — | **No** | Metadata-only, never reads file data |
| `ls` | (none) | — | **No** | Directory listing, never opens the file |

## Important gaps

- **mmap IN\_ACCESS**: On this WSL2 kernel, `mmap()` itself did **not** generate
  `IN_ACCESS` on the file (only IN\_OPEN and IN\_CLOSE\_WRITE because the file
  was opened `O_RDWR` by Python). An attacker who opens the file `O_RDONLY`,
  maps it, and reads through the mapping **may not** trigger IN\_ACCESS on some
  kernels — however the IN\_OPEN event still fires, so Decoyd will alert.
  UNKNOWN: behaviour on bare-metal Linux kernels vs WSL2 may differ.
- **sendfile/copy\_file\_range**: Go's `io.Copy` to a regular file uses
  `copy_file_range(2)` on Linux ≥ 4.5. Despite being an in-kernel copy, the
  source file still generates IN\_ACCESS [RAN]. This contradicts some older
  documentation.
- **O\_DIRECT reads**: not tested. UNKNOWN.
- **Network filesystem (NFS, SMB)**: inotify events are not propagated from the
  server; a remote reader generates no event on the client. Document decoys on
  network paths as unsupported (also caught by `decoyd doctor`).
- **9p / drvfs (Windows drives in WSL)**: inotify support is incomplete.
  `decoyd doctor` warns on these filesystem types.
- **tmpfs**: Events fire correctly in our probe environment (mktemp uses tmpfs
  on most Linux distros), but `decoyd doctor` still warns because tmpfs paths
  are reset on reboot, making persistent monitoring unreliable.

## Decoyd watcher mask

The production watcher registers `IN_ACCESS | IN_OPEN | IN_CLOSE_NOWRITE |
IN_CLOSE_WRITE | IN_MODIFY` on each decoy file. The 2-second debounce coalesces
bursts (e.g. head reading partial blocks, grep scanning the whole file) into one
alert.

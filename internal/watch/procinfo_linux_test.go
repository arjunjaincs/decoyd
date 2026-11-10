//go:build linux

package watch

// procinfo_linux_test.go – fixture tests for procinfo_linux.go.
// Tests use synthetic /proc directories created in t.TempDir() — never touch real /proc.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// helpers ----------------------------------------------------------------

func procDir(root string, pid int) string {
	return filepath.Join(root, strconv.Itoa(pid))
}

func mkProcDir(t *testing.T, root string, pid int) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0444); err != nil {
		t.Fatal(err)
	}
}

// makeStat builds a /proc/<pid>/stat line with the given comm, ppid, startTicks.
// All other fields are set to 0 or placeholder values.
func makeStat(pid int, comm string, ppid int, startTicks uint64) string {
	// Fields (after comm+state): ppid pgrp session tty tpgid flags
	// minflt cminflt majflt cmajflt utime stime cutime cstime
	// priority nice num_threads itrealvalue starttime ...
	return fmt.Sprintf("%d (%s) S %d 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		pid, comm, ppid, startTicks)
}

// readComm tests ---------------------------------------------------------

func TestReadComm_Normal(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 1234)
	writeFile(t, dir, "comm", "bash\n")
	got := readComm(1234, root)
	if got != "bash" {
		t.Errorf("readComm = %q; want %q", got, "bash")
	}
}

func TestReadComm_Missing(t *testing.T) {
	root := t.TempDir()
	// No /proc/9999 — should return "<unknown>"
	got := readComm(9999, root)
	if got != "<unknown>" {
		t.Errorf("readComm = %q; want %q", got, "<unknown>")
	}
}

func TestReadComm_LongTruncated(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 2)
	writeFile(t, dir, "comm", "verylongcommname\n") // 16 chars
	got := readComm(2, root)
	if len(got) > 15 {
		t.Errorf("readComm len = %d; want <= 15", len(got))
	}
}

// parseStat tests --------------------------------------------------------

func TestParseStat_Normal(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 100)
	writeFile(t, dir, "stat", makeStat(100, "bash", 1, 9999))
	startTicks, ppid, err := parseStat(100, root)
	if err != nil {
		t.Fatalf("parseStat error: %v", err)
	}
	if ppid != 1 {
		t.Errorf("ppid = %d; want 1", ppid)
	}
	if startTicks != 9999 {
		t.Errorf("startTicks = %d; want 9999", startTicks)
	}
}

func TestParseStat_CommWithParens(t *testing.T) {
	// comm contains parentheses — parseStat must use the LAST ')'
	root := t.TempDir()
	dir := mkProcDir(t, root, 5678)
	stat := "5678 ((bad (name))) S 2 5678 5678 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
	writeFile(t, dir, "stat", stat)
	startTicks, ppid, err := parseStat(5678, root)
	if err != nil {
		t.Fatalf("parseStat error: %v", err)
	}
	if ppid != 2 {
		t.Errorf("ppid = %d; want 2", ppid)
	}
	if startTicks != 12345 {
		t.Errorf("startTicks = %d; want 12345", startTicks)
	}
}

func TestParseStat_CommWithSpaces(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 9999)
	stat := "9999 (my cool proc) R 100 9999 9999 0 -1 4194560 0 0 0 0 10 5 0 0 20 0 1 0 54321 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
	writeFile(t, dir, "stat", stat)
	startTicks, ppid, err := parseStat(9999, root)
	if err != nil {
		t.Fatalf("parseStat error: %v", err)
	}
	if ppid != 100 {
		t.Errorf("ppid = %d; want 100", ppid)
	}
	if startTicks != 54321 {
		t.Errorf("startTicks = %d; want 54321", startTicks)
	}
}

// readExe tests ----------------------------------------------------------

func TestReadExe_Normal(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 1111)
	target := filepath.Join(root, "real_binary")
	if err := os.WriteFile(target, []byte("ELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	path, deleted := readExe(1111, root)
	if deleted {
		t.Error("deleted = true; want false")
	}
	if path == "" {
		t.Error("path is empty")
	}
}

func TestReadExe_Deleted(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 2222)
	// Symlink with " (deleted)" suffix — may be dangling (os.Readlink still works)
	const exeTarget = "/usr/bin/python3 (deleted)"
	if err := os.Symlink(exeTarget, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	path, deleted := readExe(2222, root)
	if !deleted {
		t.Error("deleted = false; want true")
	}
	if strings.Contains(path, "(deleted)") {
		t.Errorf("path still contains '(deleted)': %q", path)
	}
	if !strings.HasSuffix(path, "python3") {
		t.Errorf("path = %q; want suffix 'python3'", path)
	}
}

// readCmdline tests ------------------------------------------------------

func TestReadCmdline_NullSep(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 3333)
	writeFile(t, dir, "cmdline", "/usr/bin/python3\x00script.py\x00--verbose\x00")
	got := readCmdline(3333, root)
	if strings.Contains(got, "\x00") {
		t.Errorf("cmdline contains NUL: %q", got)
	}
	if !strings.Contains(got, "python3") {
		t.Errorf("cmdline missing 'python3': %q", got)
	}
}

func TestReadCmdline_AnsiStripped(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 4444)
	// ANSI escape + NUL args
	writeFile(t, dir, "cmdline", "\x1b[31mred\x1b[0m\x00arg\x00")
	got := readCmdline(4444, root)
	if strings.Contains(got, "\x1b") {
		t.Errorf("cmdline contains ANSI escape: %q", got)
	}
}

// ReadEnvFiltered tests --------------------------------------------------

func TestReadEnvFiltered_NpmVars(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 5555)
	env := "HOME=/home/user\x00PATH=/usr/bin\x00npm_package_name=lodash\x00npm_package_version=4.17.21\x00npm_lifecycle_event=install\x00SECRET=should_not_appear\x00"
	writeFile(t, dir, "environ", env)
	name, version, lifecycle := ReadEnvFiltered(5555, root)
	if name != "lodash" {
		t.Errorf("name = %q; want %q", name, "lodash")
	}
	if version != "4.17.21" {
		t.Errorf("version = %q; want %q", version, "4.17.21")
	}
	if lifecycle != "install" {
		t.Errorf("lifecycle = %q; want %q", lifecycle, "install")
	}
}

func TestReadEnvFiltered_OnlyAllowedKeys(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 6666)
	env := "HOME=/home/user\x00AWS_SECRET_ACCESS_KEY=supersecret\x00GITHUB_TOKEN=ghp_xxxx\x00"
	writeFile(t, dir, "environ", env)
	name, version, lifecycle := ReadEnvFiltered(6666, root)
	if name != "" || version != "" || lifecycle != "" {
		t.Errorf("expected all empty; got name=%q version=%q lifecycle=%q", name, version, lifecycle)
	}
}

// AncestorChain cycle guard test -----------------------------------------

func writeMinStat(t *testing.T, root string, pid, ppid int, startTicks uint64) {
	t.Helper()
	dir := mkProcDir(t, root, pid)
	writeFile(t, dir, "stat", makeStat(pid, fmt.Sprintf("proc%d", pid), ppid, startTicks))
	writeFile(t, dir, "comm", fmt.Sprintf("proc%d\n", pid))
	writeFile(t, dir, "cmdline", fmt.Sprintf("proc%d\x00", pid))
	writeFile(t, dir, "status", fmt.Sprintf("Name:\tproc%d\nUid:\t0\t0\t0\t0\n", pid))
	writeFile(t, dir, "environ", "HOME=/\x00")
}

func TestAncestorChain_CycleGuard(t *testing.T) {
	root := t.TempDir()
	// Cycle: 100 -> ppid=200, 200 -> ppid=100
	writeMinStat(t, root, 100, 200, 111)
	writeMinStat(t, root, 200, 100, 222)

	chain := AncestorChain(100, 8, root)
	if len(chain) > 2 {
		t.Errorf("AncestorChain len = %d despite cycle; want <= 2", len(chain))
	}
}

func TestAncestorChain_StopsAtPID1(t *testing.T) {
	root := t.TempDir()
	writeMinStat(t, root, 500, 1, 100) // parent is PID 1 — stop
	writeMinStat(t, root, 1, 0, 1)

	chain := AncestorChain(500, 8, root)
	// Should produce at most 1 entry (PID 1 itself won't be added since ppid<=1 stops the loop before PID 1 is visited)
	if len(chain) > 1 {
		t.Errorf("AncestorChain len = %d; want <= 1", len(chain))
	}
}

// BuildAttribution vanished process test ---------------------------------

func TestBuildAttribution_VanishedProcess(t *testing.T) {
	root := t.TempDir()
	// PID 7777 has no /proc entry — must not panic, must return a valid record
	a := BuildAttribution(7777, root)
	if a == nil {
		t.Fatal("BuildAttribution returned nil")
	}
}

func TestBuildAttribution_NpmEnvCaptured(t *testing.T) {
	root := t.TempDir()
	dir := mkProcDir(t, root, 8888)
	writeFile(t, dir, "stat", makeStat(8888, "node", 1, 55555))
	writeFile(t, dir, "comm", "node\n")
	writeFile(t, dir, "cmdline", "node\x00install.js\x00")
	writeFile(t, dir, "status", "Name:\tnode\nUid:\t1000\t1000\t1000\t1000\n")
	writeFile(t, dir, "environ",
		"HOME=/home/user\x00npm_package_name=evil-pkg\x00npm_package_version=1.0.0\x00npm_lifecycle_event=postinstall\x00")

	a := BuildAttribution(8888, root)
	if a == nil {
		t.Fatal("BuildAttribution returned nil")
	}
	if a.NpmPackage != "evil-pkg" {
		t.Errorf("NpmPackage = %q; want %q", a.NpmPackage, "evil-pkg")
	}
	if a.NpmLifecycleEvent != "postinstall" {
		t.Errorf("NpmLifecycleEvent = %q; want %q", a.NpmLifecycleEvent, "postinstall")
	}
}

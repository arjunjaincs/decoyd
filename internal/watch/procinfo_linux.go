//go:build linux

// Package watch contains the platform-specific watcher implementations.
// procinfo_linux.go reads process attributes from /proc for attribution.
// All string fields are sanitized before returning (ANSI, bidi, C0/C1 stripped).
// procRoot is injectable for tests (default: "/proc").
package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/arjunjaincs/decoyd/internal/attribution"
	"github.com/arjunjaincs/decoyd/internal/sanitize"
)

// DefaultProcRoot is the standard /proc mount point.
const DefaultProcRoot = "/proc"

// procExeCache caches SHA-256 digests keyed by (path, mtime-ns, size).
// LRU eviction is a simple FIFO with max 256 entries.
var (
	procExeCache   = map[exeCacheKey]string{}
	procExeCacheMu sync.Mutex
	procExeOrder   []exeCacheKey
)

type exeCacheKey struct {
	path    string
	mtimeNs int64
	size    int64
}

func exeCacheLookup(k exeCacheKey) (string, bool) {
	procExeCacheMu.Lock()
	defer procExeCacheMu.Unlock()
	v, ok := procExeCache[k]
	return v, ok
}

func exeCacheStore(k exeCacheKey, digest string) {
	procExeCacheMu.Lock()
	defer procExeCacheMu.Unlock()
	const maxEntries = 256
	if len(procExeOrder) >= maxEntries {
		old := procExeOrder[0]
		procExeOrder = procExeOrder[1:]
		delete(procExeCache, old)
	}
	procExeCache[k] = digest
	procExeOrder = append(procExeOrder, k)
}

// readComm reads /proc/<pid>/comm, trims newline, caps to 15 chars, and sanitizes.
// Returns "<unknown>" on any error.
func readComm(pid int32, procRoot string) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "comm"))
	if err != nil {
		return "<unknown>"
	}
	s := strings.TrimRight(string(data), "\n")
	if len(s) > 15 {
		s = s[:15]
	}
	return sanitize.Sanitize(s, 64)
}

// parseStat parses /proc/<pid>/stat to extract ppid and starttime (field 22).
// It handles comm values containing spaces and parentheses by finding the LAST ')'.
func parseStat(pid int32, procRoot string) (startTicks uint64, ppid int32, err error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "stat"))
	if err != nil {
		return 0, 0, err
	}
	line := strings.TrimRight(string(data), "\n")
	// Find the LAST ')' — comm may contain '(' and ')'
	lastParen := strings.LastIndex(line, ")")
	if lastParen < 0 || lastParen >= len(line)-2 {
		return 0, 0, fmt.Errorf("proc/stat: bad format for pid %d", pid)
	}
	// Fields after last ')' are space-separated.
	// field order (0-indexed after ')'):
	//   0=state  1=ppid  2=pgrp  3=session  4=tty_nr  5=tpgid  6=flags
	//   7=minflt 8=cminflt 9=majflt 10=cmajflt 11=utime 12=stime
	//   13=cutime 14=cstime 15=priority 16=nice 17=num_threads 18=itrealvalue
	//   19=starttime (field 22 in 1-indexed stat spec)
	rest := strings.TrimLeft(line[lastParen+1:], " ")
	fields := strings.Fields(rest)
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("proc/stat: too few fields for pid %d", pid)
	}
	ppidVal, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("proc/stat: bad ppid for pid %d: %w", pid, err)
	}
	startVal, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("proc/stat: bad starttime for pid %d: %w", pid, err)
	}
	return startVal, int32(ppidVal), nil
}

// readExe resolves /proc/<pid>/exe symlink; strips " (deleted)" suffix.
// Returns empty string and false on error.
func readExe(pid int32, procRoot string) (path string, deleted bool) {
	target, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(int(pid)), "exe"))
	if err != nil {
		return "", false
	}
	const deletedSuffix = " (deleted)"
	if strings.HasSuffix(target, deletedSuffix) {
		return sanitize.Sanitize(strings.TrimSuffix(target, deletedSuffix), 512), true
	}
	return sanitize.Sanitize(target, 512), false
}

// readCmdline reads /proc/<pid>/cmdline, replaces NUL bytes with spaces,
// caps to 4096 bytes, and sanitizes.
func readCmdline(pid int32, procRoot string) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "cmdline"))
	if err != nil {
		return ""
	}
	if len(data) > 4096 {
		data = data[:4096]
	}
	s := strings.ReplaceAll(string(data), "\x00", " ")
	s = strings.TrimRight(s, " ")
	return sanitize.Sanitize(s, 4096)
}

// readCwd reads /proc/<pid>/cwd symlink and sanitizes.
func readCwd(pid int32, procRoot string) string {
	target, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(int(pid)), "cwd"))
	if err != nil {
		return ""
	}
	return sanitize.Sanitize(target, 512)
}

// readUID parses /proc/<pid>/status for the first "Uid:" line and returns the real UID.
func readUID(pid int32, procRoot string) uint32 {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		v, err := strconv.ParseUint(fields[1], 10, 32)
		if err == nil {
			return uint32(v)
		}
		break
	}
	return 0
}

// ReadEnvFiltered reads /proc/<pid>/environ (NUL-separated) and returns ONLY the
// values of npm_package_name, npm_package_version, and npm_lifecycle_event.
// All values are sanitized to 512 bytes. No other environment variables are read.
func ReadEnvFiltered(pid int32, procRoot string) (name, version, lifecycle string) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "environ"))
	if err != nil {
		return "", "", ""
	}
	entries := strings.Split(string(data), "\x00")
	for _, entry := range entries {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			continue
		}
		key, val := entry[:idx], entry[idx+1:]
		switch key {
		case "npm_package_name":
			name = sanitize.Sanitize(val, 512)
		case "npm_package_version":
			version = sanitize.Sanitize(val, 512)
		case "npm_lifecycle_event":
			lifecycle = sanitize.Sanitize(val, 512)
		}
	}
	return
}

// exeSHA256 computes a SHA-256 hex digest of /proc/<pid>/exe, capped at 100 MB.
// Results are cached by (exePath, mtime-ns, size). Returns "" on error.
func exeSHA256(pid int32, exePath string, procRoot string) string {
	if exePath == "" {
		return ""
	}
	exeLink := filepath.Join(procRoot, strconv.Itoa(int(pid)), "exe")
	fi, err := os.Lstat(exeLink)
	if err != nil {
		return ""
	}
	k := exeCacheKey{path: exePath, mtimeNs: fi.ModTime().UnixNano(), size: fi.Size()}
	if digest, ok := exeCacheLookup(k); ok {
		return digest
	}
	f, err := os.Open(exeLink)
	if err != nil {
		return ""
	}
	defer f.Close()
	const maxBytes = 100 * 1024 * 1024
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxBytes)); err != nil {
		return ""
	}
	digest := hex.EncodeToString(h.Sum(nil))
	exeCacheStore(k, digest)
	return digest
}

// processSnapshot holds intermediate collection results with PID-reuse detection data.
type processSnapshot struct {
	startTicks0  uint64
	startTicks1  uint64
	ppid         int32
	info         attribution.ProcessInfo
	npmName      string
	npmVersion   string
	npmLifecycle string
	pidReused    bool
}

// collectProcess reads all /proc fields for pid and detects PID reuse.
func collectProcess(pid int32, procRoot string) processSnapshot {
	startTicks0, ppid, err := parseStat(pid, procRoot)
	if err != nil {
		return processSnapshot{
			info: attribution.ProcessInfo{
				PID:  pid,
				Comm: readComm(pid, procRoot),
			},
			pidReused: true,
		}
	}

	comm := readComm(pid, procRoot)
	exePath, deleted := readExe(pid, procRoot)
	cmdline := readCmdline(pid, procRoot)
	cwd := readCwd(pid, procRoot)
	uid := readUID(pid, procRoot)
	npmName, npmVersion, npmLifecycle := ReadEnvFiltered(pid, procRoot)
	sha := exeSHA256(pid, exePath, procRoot)

	// Re-read startTicks to detect PID reuse
	startTicks1, _, err2 := parseStat(pid, procRoot)
	pidReused := err2 != nil || startTicks0 != startTicks1

	info := attribution.ProcessInfo{
		PID:        pid,
		PPID:       ppid,
		Comm:       comm,
		Exe:        exePath,
		ExeDeleted: deleted,
		Cmdline:    cmdline,
		Cwd:        cwd,
		UID:        uid,
		StartTicks: startTicks0,
		ExeSHA256:  sha,
	}
	if pidReused {
		// Data is stale — clear sensitive fields
		info.Exe = ""
		info.ExeDeleted = false
		info.ExeSHA256 = ""
	}
	return processSnapshot{
		startTicks0:  startTicks0,
		startTicks1:  startTicks1,
		ppid:         ppid,
		info:         info,
		npmName:      npmName,
		npmVersion:   npmVersion,
		npmLifecycle: npmLifecycle,
		pidReused:    pidReused,
	}
}

// BuildProcessInfo assembles a ProcessInfo for pid using procRoot.
// PID reuse is detected by comparing startTicks read before and after collection.
// Returns the ProcessInfo plus npm environment values.
func BuildProcessInfo(pid int32, procRoot string) (info attribution.ProcessInfo, npmName, npmVersion, npmLifecycle string) {
	if procRoot == "" {
		procRoot = DefaultProcRoot
	}
	snap := collectProcess(pid, procRoot)
	return snap.info, snap.npmName, snap.npmVersion, snap.npmLifecycle
}

// AncestorChain builds the ppid chain starting from pid, up to maxDepth levels.
// Guards against PID cycles using a seen map. Stops at PID <= 1.
func AncestorChain(pid int32, maxDepth int, procRoot string) attribution.AncestorChain {
	if procRoot == "" {
		procRoot = DefaultProcRoot
	}
	if maxDepth <= 0 {
		maxDepth = 8
	}
	var chain attribution.AncestorChain
	seen := map[int32]bool{pid: true}
	current := pid

	for i := 0; i < maxDepth; i++ {
		_, ppid, err := parseStat(current, procRoot)
		if err != nil || ppid <= 1 {
			break
		}
		if seen[ppid] {
			// Cycle detected — stop
			break
		}
		seen[ppid] = true
		snap := collectProcess(ppid, procRoot)
		chain = append(chain, snap.info)
		current = ppid
	}
	return chain
}

// BuildAttribution builds a full Attribution record for a process event.
// Called from the fanotify dispatch worker (not the event loop, to avoid blocking).
// Confidence is High if exe+SHA are present and PID not reused; Medium if exe only; Low otherwise.
func BuildAttribution(pid int32, procRoot string) *attribution.Attribution {
	if procRoot == "" {
		procRoot = DefaultProcRoot
	}
	snap := collectProcess(pid, procRoot)
	ancestors := AncestorChain(pid, 8, procRoot)

	confidence := attribution.ConfidenceHigh
	if snap.pidReused {
		confidence = attribution.ConfidenceLow
	} else if snap.info.ExeSHA256 == "" {
		confidence = attribution.ConfidenceMedium
	}

	return &attribution.Attribution{
		Accessor:          snap.info,
		Ancestors:         ancestors,
		Confidence:        confidence,
		NpmPackage:        snap.npmName,
		NpmVersion:        snap.npmVersion,
		NpmLifecycleEvent: snap.npmLifecycle,
	}
}

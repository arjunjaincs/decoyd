//go:build linux

// fanotify_linux.go — fanotify-based open/access watcher for Linux.
//
// Constraints (O1–O3, T2):
//   - FAN_CLASS_NOTIF only. Never FAN_*_PERM.
//   - Parse event buffer with encoding/binary; NO unsafe.
//   - ALWAYS close the event fd (even on lookup miss or parse error).
//   - FAN_Q_OVERFLOW: log to stderr, do NOT fire all tokens.
//   - Marks use FAN_MARK_ADD | FAN_MARK_INODE with mask FAN_OPEN | FAN_ACCESS.
//   - Trust check (O5): Lstat the token path; must be a regular file.
//   - Debounce per (tokenID, truncated-second, pid) up to 5 distinct pids per window.
//   - Ignore events from our own pid.
package watch

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arjunjaincs/decoyd/internal/attribution"
)

// fanotify metadata event structure size (struct fanotify_event_metadata).
// From linux/fanotify.h:
//
//	__u32 event_len;      // 0
//	__u8  vers;           // 4
//	__u8  reserved;       // 5
//	__u16 metadata_len;   // 6
//	__u64 mask;           // 8  (8-byte aligned)
//	__s32 fd;             // 16
//	__s32 pid;            // 20
//	total: 24 bytes
const fanotifyMetadataSize = 24

// fanotify version expected in event metadata.
const fanotifyMetadataVersion = 3

// Source: https://elixir.bootlin.com/linux/latest/source/include/uapi/linux/fanotify.h
// FAN_CLASS_NOTIF     = 0x00000000
// FAN_CLOEXEC         = 0x00000001
// FAN_NONBLOCK        = 0x00000002
// FAN_OPEN            = 0x00000020
// FAN_ACCESS          = 0x00000001
// FAN_Q_OVERFLOW      = 0x00004000
// FAN_MARK_ADD        = 0x00000001
// FAN_MARK_INODE      = 0x00000000  (default; no extra flag needed)

// tokenMark holds the (dev, ino) → token mapping for fanotify inode marks.
type tokenMark struct {
	dev uint64
	ino uint64
	tok DeployedToken
}

// fanotifyEvent is the parsed result of one fanotify metadata record.
type fanotifyEvent struct {
	mask    uint64
	eventFD int32
	pid     int32
}

// parseFanotifyEvents parses a raw fanotify read buffer into events.
// ALWAYS closes eventFD (even on parse error or overflow).
// Never reads out of bounds.
// Returns parsed events and whether a Q_OVERFLOW was detected.
func parseFanotifyEvents(buf []byte) (events []fanotifyEvent, overflow bool) {
	offset := 0
	for offset+fanotifyMetadataSize <= len(buf) {
		// Read event_len first to know how to advance.
		eventLen := int(binary.LittleEndian.Uint32(buf[offset : offset+4]))
		if eventLen < fanotifyMetadataSize || offset+eventLen > len(buf) {
			// Malformed — stop parsing; remaining bytes are dropped.
			break
		}

		// Validate version.
		vers := buf[offset+4]
		if vers != fanotifyMetadataVersion {
			offset += eventLen
			continue
		}

		mask := binary.LittleEndian.Uint64(buf[offset+8 : offset+16])
		fd := int32(binary.LittleEndian.Uint32(buf[offset+16 : offset+20]))
		pid := int32(binary.LittleEndian.Uint32(buf[offset+20 : offset+24]))

		if mask&unix.FAN_Q_OVERFLOW != 0 {
			// Overflow — close fd if valid.
			if fd >= 0 {
				_ = unix.Close(int(fd))
			}
			overflow = true
			offset += eventLen
			continue
		}

		events = append(events, fanotifyEvent{mask: mask, eventFD: fd, pid: pid})
		offset += eventLen
	}
	return events, overflow
}

// fanotifyTokenMap is a snapshot of dev:ino → DeployedToken mappings.
type fanotifyTokenMap map[uint64]map[uint64]DeployedToken // dev → ino → token

// fanotifyWatcher holds state for the fanotify-based watcher.
// It is embedded in hybridLinuxWatcher and not used standalone.
type fanotifyWatcher struct {
	fanFD  int // fanotify fd; -1 when closed
	stopR  int // read end of stop pipe
	stopW  int // write end of stop pipe
	ownPID int32

	// tokenMap is the inode-keyed token lookup table.
	// Written on start/reload; read in event loop (single goroutine).
	tokenMap fanotifyTokenMap

	// failedTokens: token IDs that could not be marked (fell back to inotify).
	// Protected by the caller (hybridLinuxWatcher).
	failedTokens map[string]string // token ID → reason
}

// fanotifyInit initialises a fanotify file descriptor with FAN_CLASS_NOTIF.
// Returns the fd or an error. The caller must close the fd.
func fanotifyInit() (int, error) {
	fd, err := unix.FanotifyInit(
		unix.FAN_CLASS_NOTIF|unix.FAN_CLOEXEC|unix.FAN_NONBLOCK,
		uint(unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC),
	)
	if err != nil {
		return -1, fmt.Errorf("fanotify_init: %w", err)
	}
	return fd, nil
}

// newFanotifyWatcher creates a fanotifyWatcher with an initialised fd.
// Returns nil, nil if fanotify is unavailable (EPERM/ENODEV/EOPNOTSUPP/ENOENT).
// Returns nil, err for unexpected errors.
// Returns watcher, nil on success.
func newFanotifyWatcher() (*fanotifyWatcher, error) {
	fd, err := fanotifyInit()
	if err != nil {
		if isUnavailableErr(err) {
			return nil, nil // graceful: not available on this system
		}
		return nil, err
	}

	pipefd := [2]int{}
	if err := unix.Pipe2(pipefd[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("fanotify stop pipe: %w", err)
	}

	return &fanotifyWatcher{
		fanFD:        fd,
		stopR:        pipefd[0],
		stopW:        pipefd[1],
		ownPID:       int32(os.Getpid()),
		tokenMap:     make(fanotifyTokenMap),
		failedTokens: make(map[string]string),
	}, nil
}

// isUnavailableErr returns true for errors that indicate fanotify is simply
// not available in this environment (no privilege, no kernel support, etc.)
func isUnavailableErr(err error) bool {
	switch {
	case isErrno(err, syscall.EPERM):
		return true
	case isErrno(err, syscall.ENODEV):
		return true
	case isErrno(err, syscall.EOPNOTSUPP):
		return true
	case isErrno(err, syscall.ENOENT):
		return true
	}
	return false
}

func isErrno(err error, target syscall.Errno) bool {
	if err == nil {
		return false
	}
	// unwrap one level
	if e, ok := err.(syscall.Errno); ok {
		return e == target
	}
	// check wrapped
	unwrapped := err
	for {
		if e, ok := unwrapped.(syscall.Errno); ok {
			return e == target
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := unwrapped.(unwrapper); ok {
			unwrapped = u.Unwrap()
			if unwrapped == nil {
				return false
			}
		} else {
			return false
		}
	}
}

// addMark adds a fanotify inode mark for a deployed token file.
// O5: Lstat checks that the path is a regular file (not a symlink, device, etc.).
// On failure, records the token in failedTokens and returns false.
func (fw *fanotifyWatcher) addMark(tok DeployedToken) bool {
	// O5: Lstat — refuse symlinks, require regular file.
	fi, err := os.Lstat(tok.DeployedPath)
	if err != nil {
		fw.failedTokens[tok.ID] = fmt.Sprintf("lstat: %v", err)
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		fw.failedTokens[tok.ID] = "path is a symlink"
		return false
	}
	if !fi.Mode().IsRegular() {
		fw.failedTokens[tok.ID] = "path is not a regular file"
		return false
	}

	// Get dev/ino for the inode map.
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		fw.failedTokens[tok.ID] = "cannot read stat_t"
		return false
	}
	dev := stat.Dev
	ino := stat.Ino

	// Add fanotify inode mark.
	// FAN_MARK_ADD | FAN_MARK_INODE(=0) with mask FAN_OPEN | FAN_ACCESS.
	// Use AT_FDCWD (-100) as dirFd; pathname must be absolute.
	const mask = unix.FAN_OPEN | unix.FAN_ACCESS
	if err := unix.FanotifyMark(fw.fanFD,
		unix.FAN_MARK_ADD|unix.FAN_MARK_INODE,
		mask,
		unix.AT_FDCWD,
		tok.DeployedPath,
	); err != nil {
		if isUnavailableErr(err) {
			fw.failedTokens[tok.ID] = fmt.Sprintf("mark unavailable: %v", err)
			return false
		}
		fw.failedTokens[tok.ID] = fmt.Sprintf("fanotify_mark: %v", err)
		return false
	}

	// Store in inode map.
	if fw.tokenMap[dev] == nil {
		fw.tokenMap[dev] = make(map[uint64]DeployedToken)
	}
	fw.tokenMap[dev][ino] = tok
	return true
}

// removeMark removes the fanotify inode mark for the given dev:ino.
func (fw *fanotifyWatcher) removeMark(tok DeployedToken) {
	fi, err := os.Lstat(tok.DeployedPath)
	if err != nil {
		return
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = unix.FanotifyMark(fw.fanFD,
		unix.FAN_MARK_REMOVE,
		unix.FAN_OPEN|unix.FAN_ACCESS,
		unix.AT_FDCWD,
		tok.DeployedPath,
	)
	dev, ino := stat.Dev, stat.Ino
	if m, ok := fw.tokenMap[dev]; ok {
		delete(m, ino)
		if len(m) == 0 {
			delete(fw.tokenMap, dev)
		}
	}
}

// lookupByFD uses fstat on the event fd to find the matching token.
// Returns false if not found. ALWAYS closes the event fd afterward.
func (fw *fanotifyWatcher) lookupByFD(fd int32) (DeployedToken, bool) {
	defer func() {
		if fd >= 0 {
			_ = unix.Close(int(fd))
		}
	}()
	if fd < 0 {
		return DeployedToken{}, false
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		return DeployedToken{}, false
	}
	devMap, ok := fw.tokenMap[st.Dev]
	if !ok {
		return DeployedToken{}, false
	}
	tok, ok := devMap[st.Ino]
	return tok, ok
}

// close closes the fanotify fd and stop pipe.
func (fw *fanotifyWatcher) close() {
	if fw.fanFD >= 0 {
		_ = unix.Close(fw.fanFD)
		fw.fanFD = -1
	}
	if fw.stopR >= 0 {
		_ = unix.Close(fw.stopR)
		fw.stopR = -1
	}
	if fw.stopW >= 0 {
		_ = unix.Close(fw.stopW)
		fw.stopW = -1
	}
}

// signalStop writes to the stop pipe to unblock the Poll loop.
func (fw *fanotifyWatcher) signalStop() {
	if fw.stopW >= 0 {
		_, _ = unix.Write(fw.stopW, []byte{1})
	}
}

// fanotifyDebounceKey identifies an event for per-(token, second) deduplication.
type fanotifyDebounceKey struct {
	tokenID string
	second  int64 // unix second (truncated to 2s window)
}

// fanotifyPidSet collects up to 5 distinct pids per debounce window.
type fanotifyPidSet struct {
	pids      [5]int32
	count     int
	processes [5]*attribution.Attribution // lazily populated
}

func (s *fanotifyPidSet) add(pid int32) bool {
	for i := 0; i < s.count; i++ {
		if s.pids[i] == pid {
			return false // already seen
		}
	}
	if s.count >= 5 {
		return false // full
	}
	s.pids[s.count] = pid
	s.count++
	return true
}

// runFanotifyLoop reads events from the fanotify fd until stop is signalled.
// dispatch is called in the event loop goroutine with the token and pid.
// The function returns when the stop pipe is readable.
func (fw *fanotifyWatcher) runFanotifyLoop(dispatch func(tok DeployedToken, pid int32)) {
	const bufSize = fanotifyMetadataSize * 256
	buf := make([]byte, bufSize)

	// Per-(token, 2s-window) pid sets for debounce.
	debounce := make(map[fanotifyDebounceKey]*fanotifyPidSet)
	cleanupTicker := time.NewTicker(10 * time.Second)
	defer cleanupTicker.Stop()

	pollfds := []unix.PollFd{
		{Fd: int32(fw.fanFD), Events: unix.POLLIN},
		{Fd: int32(fw.stopR), Events: unix.POLLIN},
	}

	for {
		// Drain cleanup ticker without blocking.
		select {
		case <-cleanupTicker.C:
			now := time.Now().Unix() / 2
			for k := range debounce {
				if k.second < now-5 {
					delete(debounce, k)
				}
			}
		default:
		}

		n, err := unix.Poll(pollfds, 1000 /* 1 s timeout */)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Printf("fanotify: poll error: %v", err)
			return
		}
		if n == 0 {
			continue // timeout
		}

		// Stop pipe readable?
		if pollfds[1].Revents&unix.POLLIN != 0 {
			return
		}

		// fanotify fd readable?
		if pollfds[0].Revents&unix.POLLIN == 0 {
			continue
		}

		nr, err := unix.Read(fw.fanFD, buf)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				continue
			}
			log.Printf("fanotify: read error: %v", err)
			return
		}
		if nr < fanotifyMetadataSize {
			continue
		}

		events, overflow := parseFanotifyEvents(buf[:nr])
		if overflow {
			log.Printf("fanotify: FAN_Q_OVERFLOW — some open/access events lost")
			// Do NOT fire all tokens. Per O1: log and continue.
		}

		for _, ev := range events {
			// Ignore own process (prevents self-read loops).
			if ev.pid == fw.ownPID {
				// Still must close the fd.
				if ev.eventFD >= 0 {
					_ = unix.Close(int(ev.eventFD))
				}
				continue
			}

			tok, found := fw.lookupByFD(ev.eventFD) // lookupByFD always closes fd
			if !found {
				continue
			}

			// Debounce: collect up to 5 distinct pids per (tokenID, 2s window).
			windowSec := time.Now().Unix() / 2
			key := fanotifyDebounceKey{tokenID: tok.ID, second: windowSec}
			ps := debounce[key]
			if ps == nil {
				ps = &fanotifyPidSet{}
				debounce[key] = ps
			}
			if ps.add(ev.pid) {
				dispatch(tok, ev.pid)
			}
		}
	}
}

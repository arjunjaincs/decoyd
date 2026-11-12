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
	"math"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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
// parseFanotifyFD converts a uint32 kernel metadata fd field to signed int32
// with explicit bounds checking. 0xFFFFFFFF encodes FAN_NOFD (-1). Values above
// math.MaxInt32 other than FAN_NOFD are rejected as malformed.
func parseFanotifyFD(raw uint32) (int32, bool) {
	if raw == 0xFFFFFFFF {
		return -1, true
	}
	if raw > math.MaxInt32 {
		return -1, false
	}
	return int32(raw), true
}

// parseFanotifyPID converts a uint32 kernel metadata pid field to signed int32
// with explicit overflow bounds checking.
func parseFanotifyPID(raw uint32) (int32, bool) {
	if raw > math.MaxInt32 {
		return 0, false
	}
	return int32(raw), true
}

// safeIntToInt32 converts a non-negative int (e.g. fd or pid) to int32 with
// explicit bounds checks.
func safeIntToInt32(v int) (int32, bool) {
	if v < 0 || v > math.MaxInt32 {
		return -1, false
	}
	return int32(v), true
}

// parseFanotifyEvents parses raw fanotify metadata records without closing file descriptors.
// Callers reading from the live kernel fd must use parseFanotifyEventsWithCloser with unix.Close.
func parseFanotifyEvents(buf []byte) (events []fanotifyEvent, overflow bool) {
	return parseFanotifyEventsWithCloser(buf, nil)
}

func parseFanotifyEventsWithCloser(buf []byte, closeFD func(int)) (events []fanotifyEvent, overflow bool) {
	offset := 0
	for offset+fanotifyMetadataSize <= len(buf) {
		eventLen32 := binary.LittleEndian.Uint32(buf[offset : offset+4])
		if eventLen32 < fanotifyMetadataSize || uint64(eventLen32) > uint64(len(buf)-offset) {
			// Malformed or truncated — stop parsing; remaining bytes are dropped.
			break
		}
		eventLen := int(eventLen32)

		// Validate version.
		vers := buf[offset+4]
		if vers != fanotifyMetadataVersion {
			offset += eventLen
			continue
		}

		mask := binary.LittleEndian.Uint64(buf[offset+8 : offset+16])
		rawFD := binary.LittleEndian.Uint32(buf[offset+16 : offset+20])
		rawPID := binary.LittleEndian.Uint32(buf[offset+20 : offset+24])

		fd, fdOK := parseFanotifyFD(rawFD)
		pid, pidOK := parseFanotifyPID(rawPID)

		if mask&unix.FAN_Q_OVERFLOW != 0 {
			if closeFD != nil && fdOK && fd >= 0 {
				closeFD(int(fd))
			}
			overflow = true
			offset += eventLen
			continue
		}

		if !fdOK || !pidOK {
			if closeFD != nil && fdOK && fd >= 0 {
				closeFD(int(fd))
			}
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

	doneOnce sync.Once
	done     chan struct{}

	mu sync.RWMutex
	// tokenMap is the inode-keyed token lookup table (protected by mu).
	tokenMap fanotifyTokenMap

	// failedTokens maps token ID -> fallback reason (protected by mu).
	failedTokens map[string]string
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

	ownPID, ok := safeIntToInt32(os.Getpid())
	if !ok {
		_ = unix.Close(fd)
		_ = unix.Close(pipefd[0])
		_ = unix.Close(pipefd[1])
		return nil, fmt.Errorf("fanotify init: pid exceeds int32 range")
	}

	return &fanotifyWatcher{
		fanFD:        fd,
		stopR:        pipefd[0],
		stopW:        pipefd[1],
		ownPID:       ownPID,
		done:         make(chan struct{}),
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

func (fw *fanotifyWatcher) recordFailed(tokenID, reason string) {
	fw.mu.Lock()
	fw.failedTokens[tokenID] = reason
	fw.mu.Unlock()
}

func (fw *fanotifyWatcher) snapshotFailed() map[string]string {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	out := make(map[string]string, len(fw.failedTokens))
	for k, v := range fw.failedTokens {
		out[k] = v
	}
	return out
}

func (fw *fanotifyWatcher) snapshotMarkedTokens() []DeployedToken {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	var out []DeployedToken
	for _, m := range fw.tokenMap {
		for _, t := range m {
			out = append(out, t)
		}
	}
	return out
}

// addMark adds a fanotify inode mark for a deployed token file.
// O5: Lstat checks that the path is a regular file (not a symlink) and matches OwnerUID.
// On failure, records the token in failedTokens and returns false.
func (fw *fanotifyWatcher) addMark(tok DeployedToken) bool {
	// O5: Lstat — refuse symlinks, require regular file, verify owner UID.
	fi, err := os.Lstat(tok.DeployedPath)
	if err != nil {
		fw.recordFailed(tok.ID, fmt.Sprintf("lstat: %v", err))
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		reason := "path is a symlink (O5)"
		log.Printf("fanotify: skipping token %s (%s): %s", tok.ID, tok.DeployedPath, reason)
		fw.recordFailed(tok.ID, reason)
		return false
	}
	if !fi.Mode().IsRegular() {
		reason := "path is not a regular file (O5)"
		log.Printf("fanotify: skipping token %s (%s): %s", tok.ID, tok.DeployedPath, reason)
		fw.recordFailed(tok.ID, reason)
		return false
	}

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		fw.recordFailed(tok.ID, "cannot read stat_t")
		return false
	}
	if tok.OwnerUID != nil && stat.Uid != *tok.OwnerUID {
		reason := fmt.Sprintf("owner uid %d != snapshot uid %d (O5)", stat.Uid, *tok.OwnerUID)
		log.Printf("fanotify: skipping token %s (%s): %s", tok.ID, tok.DeployedPath, reason)
		fw.recordFailed(tok.ID, reason)
		return false
	}
	dev := stat.Dev
	ino := stat.Ino

	// Add fanotify inode mark.
	const mask = unix.FAN_OPEN | unix.FAN_ACCESS
	if err := unix.FanotifyMark(fw.fanFD,
		unix.FAN_MARK_ADD|unix.FAN_MARK_INODE,
		mask,
		unix.AT_FDCWD,
		tok.DeployedPath,
	); err != nil {
		if isUnavailableErr(err) {
			fw.recordFailed(tok.ID, fmt.Sprintf("mark unavailable: %v", err))
			return false
		}
		fw.recordFailed(tok.ID, fmt.Sprintf("fanotify_mark: %v", err))
		return false
	}

	fw.mu.Lock()
	if fw.tokenMap[dev] == nil {
		fw.tokenMap[dev] = make(map[uint64]DeployedToken)
	}
	fw.tokenMap[dev][ino] = tok
	delete(fw.failedTokens, tok.ID)
	fw.mu.Unlock()
	return true
}

// removeMark removes the fanotify inode mark and clears all inode mappings for tok.ID.
func (fw *fanotifyWatcher) removeMark(tok DeployedToken) {
	if tok.DeployedPath != "" {
		_ = unix.FanotifyMark(fw.fanFD,
			unix.FAN_MARK_REMOVE,
			unix.FAN_OPEN|unix.FAN_ACCESS,
			unix.AT_FDCWD,
			tok.DeployedPath,
		)
	}
	fw.mu.Lock()
	for dev, m := range fw.tokenMap {
		for ino, t := range m {
			if t.ID == tok.ID {
				delete(m, ino)
			}
		}
		if len(m) == 0 {
			delete(fw.tokenMap, dev)
		}
	}
	fw.mu.Unlock()
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
	fw.mu.RLock()
	devMap, ok := fw.tokenMap[st.Dev]
	var tok DeployedToken
	if ok {
		tok, ok = devMap[st.Ino]
	}
	fw.mu.RUnlock()
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
	pids  [5]int32
	count int
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
func (fw *fanotifyWatcher) markDone() {
	fw.doneOnce.Do(func() {
		if fw.done != nil {
			close(fw.done)
		}
	})
}

func (fw *fanotifyWatcher) runFanotifyLoopWithOverflow(dispatch func(tok DeployedToken, pid int32), onOverflow func()) {
	defer fw.markDone()
	const bufSize = fanotifyMetadataSize * 256
	buf := make([]byte, bufSize)

	// Per-(token, 2s-window) pid sets for debounce.
	debounce := make(map[fanotifyDebounceKey]*fanotifyPidSet)
	cleanupTicker := time.NewTicker(10 * time.Second)
	defer cleanupTicker.Stop()

	fanFD32, okFan := safeIntToInt32(fw.fanFD)
	stopR32, okStop := safeIntToInt32(fw.stopR)
	if !okFan || !okStop {
		return
	}
	pollfds := []unix.PollFd{
		{Fd: fanFD32, Events: unix.POLLIN},
		{Fd: stopR32, Events: unix.POLLIN},
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

		events, overflow := parseFanotifyEventsWithCloser(buf[:nr], func(fd int) { _ = unix.Close(fd) })
		if overflow {
			log.Printf("decoyd fanotify: event queue overflow (FAN_Q_OVERFLOW) — some open/access events lost")
			// Send one sanitized, rate-limited alert and keep running (G4).
			if onOverflow != nil {
				onOverflow()
			}
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

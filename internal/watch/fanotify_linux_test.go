//go:build linux

// fanotify_linux_test.go — unit tests for the fanotify event parser and watcher.
// No privileged integration tests here (those have the privileged build tag).
package watch

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"golang.org/x/sys/unix"
)

// TestNoFANPERMConstants asserts that no *_PERM fanotify constant is referenced
// in the fanotify_linux.go or hybrid_linux.go source files. (O1 enforcement)
func TestNoFANPERMConstants(t *testing.T) {
	files := []string{
		"fanotify_linux.go",
		"hybrid_linux.go",
	}
	_, thisFile, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(thisFile)
	for _, file := range files {
		src, err := os.ReadFile(filepath.Join(pkgDir, file))
		if err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
		fset := token.NewFileSet()
		var s scanner.Scanner
		s.Init(fset.AddFile(file, fset.Base(), len(src)), src, nil, scanner.ScanComments)
		for {
			_, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.COMMENT {
				continue
			}
			if strings.Contains(lit, "_PERM") {
				t.Errorf("%s: found _PERM reference %q (must never use FAN_*_PERM constants)", file, lit)
			}
		}
	}
}

// makeFanotifyEvent constructs a raw fanotify metadata buffer for testing.
// Fields: event_len(4) vers(1) reserved(1) metadata_len(2) mask(8) fd(4) pid(4)
func makeFanotifyEvent(eventLen uint32, vers uint8, mask uint64, fd, pid int32) []byte {
	buf := make([]byte, eventLen)
	binary.LittleEndian.PutUint32(buf[0:4], eventLen)
	buf[4] = vers
	buf[5] = 0 // reserved
	binary.LittleEndian.PutUint16(buf[6:8], uint16(fanotifyMetadataSize))
	binary.LittleEndian.PutUint64(buf[8:16], mask)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(fd))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(pid))
	return buf
}

// TestParseFanotifyEvents_Normal parses a valid single event.
func TestParseFanotifyEvents_Normal(t *testing.T) {
	buf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, 7, 1234)
	events, overflow := parseFanotifyEvents(buf)
	if overflow {
		t.Error("overflow = true; want false")
	}
	if len(events) != 1 {
		t.Fatalf("got %d events; want 1", len(events))
	}
	if events[0].pid != 1234 {
		t.Errorf("pid = %d; want 1234", events[0].pid)
	}
	if events[0].eventFD != 7 {
		t.Errorf("eventFD = %d; want 7", events[0].eventFD)
	}
}

// TestParseFanotifyEvents_Overflow detects FAN_Q_OVERFLOW events.
func TestParseFanotifyEvents_Overflow(t *testing.T) {
	// FAN_Q_OVERFLOW = 0x4000
	buf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_Q_OVERFLOW, -1, 0)
	events, overflow := parseFanotifyEvents(buf)
	if !overflow {
		t.Error("overflow = false; want true")
	}
	if len(events) != 0 {
		t.Errorf("got %d events; want 0", len(events))
	}
}

// TestParseFanotifyEvents_MultipleEvents parses two sequential events.
func TestParseFanotifyEvents_MultipleEvents(t *testing.T) {
	ev1 := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, 10, 100)
	ev2 := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_ACCESS, 11, 200)
	buf := append(ev1, ev2...)
	events, overflow := parseFanotifyEvents(buf)
	if overflow {
		t.Error("overflow = true; want false")
	}
	if len(events) != 2 {
		t.Fatalf("got %d events; want 2", len(events))
	}
	if events[0].pid != 100 || events[1].pid != 200 {
		t.Errorf("pids = %d, %d; want 100, 200", events[0].pid, events[1].pid)
	}
}

// TestParseFanotifyEvents_BadVersion drops events with wrong version.
func TestParseFanotifyEvents_BadVersion(t *testing.T) {
	buf := makeFanotifyEvent(fanotifyMetadataSize, 0xFF /* bad version */, unix.FAN_OPEN, 5, 999)
	events, _ := parseFanotifyEvents(buf)
	if len(events) != 0 {
		t.Errorf("got %d events with bad version; want 0", len(events))
	}
}

// TestParseFanotifyEvents_TruncatedBuffer doesn't read out of bounds.
func TestParseFanotifyEvents_TruncatedBuffer(t *testing.T) {
	// 20 bytes < fanotifyMetadataSize(24)
	buf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, 3, 42)
	events, _ := parseFanotifyEvents(buf[:20])
	if len(events) != 0 {
		t.Errorf("got %d events from truncated buffer; want 0", len(events))
	}
}

// TestParseFanotifyEvents_MalformedEventLen stops on bad eventLen.
func TestParseFanotifyEvents_MalformedEventLen(t *testing.T) {
	buf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, 5, 42)
	// Set event_len to 4 (< fanotifyMetadataSize) to trigger stop
	binary.LittleEndian.PutUint32(buf[0:4], 4)
	events, _ := parseFanotifyEvents(buf)
	if len(events) != 0 {
		t.Errorf("got %d events with bad eventLen; want 0", len(events))
	}
}

// TestFanotifyPidSet_CapAt5 ensures the pid set caps at 5 entries.
func TestFanotifyPidSet_CapAt5(t *testing.T) {
	var ps fanotifyPidSet
	for i := int32(1); i <= 7; i++ {
		ps.add(i)
	}
	if ps.count != 5 {
		t.Errorf("count = %d; want 5 (capped)", ps.count)
	}
}

// TestFanotifyPidSet_NoDuplicates ensures duplicate pids are not counted twice.
func TestFanotifyPidSet_NoDuplicates(t *testing.T) {
	var ps fanotifyPidSet
	ps.add(100)
	ps.add(100)
	ps.add(100)
	if ps.count != 1 {
		t.Errorf("count = %d; want 1 (no duplicates)", ps.count)
	}
}

// FuzzParseFanotifyEvents ensures the parser never panics on arbitrary input.
func FuzzParseFanotifyEvents(f *testing.F) {
	// Seed corpus: valid event, overflow event, empty buffer.
	f.Add(makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, -1, 0))
	f.Add(makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_Q_OVERFLOW, -1, 0))
	f.Add([]byte{})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // random garbage
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic, must not read out of bounds.
		// eventFDs in fuzz data may be negative or garbage — that's fine,
		// unix.Close(-1) is a no-op in parseFanotifyEvents (we check fd >= 0).
		parseFanotifyEvents(data)
	})
}

// TestIsErrno verifies errno classification.
func TestIsErrno(t *testing.T) {
	import_sys_call := fmt.Errorf("wrapped: %w", unix.EPERM)
	if !isErrno(import_sys_call, unix.EPERM) {
		t.Error("expected EPERM to match")
	}
	if isErrno(import_sys_call, unix.ENODEV) {
		t.Error("expected EPERM not to match ENODEV")
	}
	if isErrno(nil, unix.EPERM) {
		t.Error("expected nil to not match anything")
	}
}

// TestFanotifyWatcher_FD_Closed verifies that lookupByFD always closes the fd.
// We use a real pipe pair to simulate a valid event fd.
func TestFanotifyWatcher_FD_Closed(t *testing.T) {
	// Create a real pipe to use as a "token file fd"
	pipefd := [2]int{}
	if err := unix.Pipe2(pipefd[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	readFD := int32(pipefd[0])
	writeFD := pipefd[1]
	defer func() { _ = unix.Close(writeFD) }()

	fw := &fanotifyWatcher{
		fanFD:        -1,
		stopR:        -1,
		stopW:        -1,
		tokenMap:     make(fanotifyTokenMap),
		failedTokens: make(map[string]string),
	}

	// lookupByFD: fd exists but nothing is in tokenMap — should close fd and return false.
	_, found := fw.lookupByFD(readFD)
	if found {
		t.Error("expected lookupByFD to return false for unregistered fd")
	}

	// Verify fd is now closed by trying to fstat it.
	var st unix.Stat_t
	err := unix.Fstat(int(readFD), &st)
	if err == nil {
		t.Error("fd should be closed after lookupByFD but Fstat succeeded")
	}
}

// TestFanotifySource_NoPERMConstants is a grep-level source check
// (redundant with TestNoFANPERMConstants but provides a second approach).
func TestFanotifySource_NoPERMConstants(t *testing.T) {
	bannedStrings := []string{
		"FAN_OPEN_PERM",
		"FAN_ACCESS_PERM",
		"FAN_OPEN_EXEC_PERM",
	}
	files := []string{"fanotify_linux.go", "hybrid_linux.go"}
	_, thisFile, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(thisFile)
	for _, fname := range files {
		data, err := os.ReadFile(filepath.Join(pkgDir, fname))
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(line, "//") {
				// Skip comment portions
				continue
			}
			for _, banned := range bannedStrings {
				if strings.Contains(line, banned) {
					t.Errorf("%s:%d: found banned constant %q in non-comment code", fname, lineNo, banned)
				}
			}
		}
	}
}

// TestFanotifyWatcher_Overflow_LogOnly tests that overflow does not trigger token alerts.
// Builds an overflow-only buffer and verifies parseFanotifyEvents returns 0 events.
func TestFanotifyWatcher_Overflow_LogOnly(t *testing.T) {
	overflowBuf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_Q_OVERFLOW, -1, 0)
	events, overflow := parseFanotifyEvents(overflowBuf)
	if !overflow {
		t.Error("expected overflow to be detected")
	}
	if len(events) != 0 {
		t.Errorf("got %d events on overflow; want 0 (must not fire all tokens)", len(events))
	}
}

// TestWatchOptionsExistsAndIsExported verifies WatchOptions is usable from outside the package.
// (This test lives in the internal watch package, so it's really a compile check.)
func TestWatchOptionsExistsAndIsExported(_ *testing.T) {
	_ = WatchOptions{Tier1: true}
	_ = WatchOptions{}
}

// TestGoldenUnitTemplateNotApplicableHere is a placeholder ensuring test count.
// (Real golden template test is in cmd_install_linux_test.go)
func TestFanotifyDebounceKey(_ *testing.T) {
	k1 := fanotifyDebounceKey{tokenID: "abc", second: 100}
	k2 := fanotifyDebounceKey{tokenID: "abc", second: 100}
	k3 := fanotifyDebounceKey{tokenID: "xyz", second: 100}
	m := map[fanotifyDebounceKey]bool{}
	m[k1] = true
	if !m[k2] {
		panic("equal keys must map to same entry")
	}
	if m[k3] {
		panic("different keys must not collide")
	}
}

func TestParseFanotifyBoundsAndOverflow(t *testing.T) {
	// FAN_NOFD (0xFFFFFFFF -> -1) must be accepted cleanly.
	if fd, ok := parseFanotifyFD(0xFFFFFFFF); !ok || fd != -1 {
		t.Fatalf("parseFanotifyFD(0xFFFFFFFF) = (%d, %v); want (-1, true)", fd, ok)
	}
	// math.MaxInt32 must be accepted; math.MaxInt32+1 must be rejected.
	if fd, ok := parseFanotifyFD(math.MaxInt32); !ok || fd != math.MaxInt32 {
		t.Fatalf("parseFanotifyFD(MaxInt32) = (%d, %v); want (MaxInt32, true)", fd, ok)
	}
	if fd, ok := parseFanotifyFD(uint32(math.MaxInt32) + 1); ok {
		t.Fatalf("parseFanotifyFD(MaxInt32+1) = (%d, true); want false", fd)
	}
	if pid, ok := parseFanotifyPID(math.MaxInt32); !ok || pid != math.MaxInt32 {
		t.Fatalf("parseFanotifyPID(MaxInt32) = (%d, %v); want (MaxInt32, true)", pid, ok)
	}
	if pid, ok := parseFanotifyPID(uint32(math.MaxInt32) + 1); ok {
		t.Fatalf("parseFanotifyPID(MaxInt32+1) = (%d, true); want false", pid)
	}
	if _, ok := safeIntToInt32(-1); ok {
		t.Fatal("safeIntToInt32(-1) = true; want false")
	}
	if v, ok := safeIntToInt32(42); !ok || v != 42 {
		t.Fatalf("safeIntToInt32(42) = (%d, %v); want (42, true)", v, ok)
	}
	// When uint64 math.MaxInt32 < int on 64-bit, check upper bound.
	bigVal := math.MaxInt32 + 1
	if _, ok := safeIntToInt32(bigVal); ok {
		t.Fatalf("safeIntToInt32(%d) = true; want false", bigVal)
	}

	// Overflows in raw event buffer must reject the event and still close valid FDs.
	pipeFDs := make([]int, 2)
	if err := unix.Pipe(pipeFDs); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = unix.Close(pipeFDs[1]) }()
	readFD := pipeFDs[0]
	// Build event with valid readFD and overflowing PID (0x80000000).
	buf := make([]byte, fanotifyMetadataSize)
	binary.LittleEndian.PutUint32(buf[0:4], fanotifyMetadataSize)
	buf[4] = fanotifyMetadataVersion
	binary.LittleEndian.PutUint16(buf[6:8], uint16(fanotifyMetadataSize))
	binary.LittleEndian.PutUint64(buf[8:16], unix.FAN_OPEN)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(readFD))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(0x80000000))

	events, overflow := parseFanotifyEventsWithCloser(buf, func(fd int) { _ = unix.Close(fd) })
	if overflow || len(events) != 0 {
		t.Fatalf("got events=%d overflow=%v; want 0, false", len(events), overflow)
	}
	var st unix.Stat_t
	if err := unix.Fstat(readFD, &st); err == nil {
		_ = unix.Close(readFD)
		t.Fatal("fd should have been closed when PID overflowed")
	}
}

func TestParseFanotifyEventsWithCloser_ClosesOrphanFDs(t *testing.T) {
	overflowBuf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_Q_OVERFLOW, 42, 0)
	badPIDBuf := make([]byte, fanotifyMetadataSize)
	copy(badPIDBuf, makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, 77, 1))
	badPIDBuf[23] = 0xec // out-of-range uint32 pid > math.MaxInt32

	var closed []int
	evs, overflow := parseFanotifyEventsWithCloser(append(overflowBuf, badPIDBuf...), func(fd int) {
		closed = append(closed, fd)
	})
	if !overflow {
		t.Fatal("expected overflow = true")
	}
	if len(evs) != 0 {
		t.Fatalf("expected 0 normal events, got %d", len(evs))
	}
	if len(closed) != 2 || closed[0] != 42 || closed[1] != 77 {
		t.Fatalf("closed fds = %v; want [42 77]", closed)
	}
}

func TestFanotifyWatcher_Overflow_SendsSanitizedRateLimitedAlert(t *testing.T) {
	var hits atomic.Int32
	payloadCh := make(chan alert.AlertPayload, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p alert.AlertPayload
		if err := json.Unmarshal(body, &p); err == nil {
			payloadCh <- p
		}
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Create a dataDir containing ANSI/bidi sequences to verify sanitization
	// happens before building AlertPayload as well as at the webhook sink.
	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "dir\x1b[31m\u202etest")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	if err := alert.Save(dataDir, alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "wh1", Type: alert.ChannelWebhook, WebhookURL: ts.URL},
		},
		DefaultID: "wh1",
	}); err != nil {
		t.Fatalf("alert.Save: %v", err)
	}

	lw := &linuxWatcher{
		dataDir: dataDir,
		cfg:     DefaultWatcherConfig(),
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}

	var fanPipe [2]int
	if err := unix.Pipe2(fanPipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatalf("pipe2 fan: %v", err)
	}
	defer func() { _ = unix.Close(fanPipe[0]) }()
	defer func() { _ = unix.Close(fanPipe[1]) }()

	var stopPipe [2]int
	if err := unix.Pipe2(stopPipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatalf("pipe2 stop: %v", err)
	}
	defer func() { _ = unix.Close(stopPipe[0]) }()
	defer func() { _ = unix.Close(stopPipe[1]) }()

	ownPID, _ := safeIntToInt32(os.Getpid())
	fw := &fanotifyWatcher{
		fanFD:  fanPipe[0],
		stopR:  stopPipe[0],
		stopW:  stopPipe[1],
		done:   make(chan struct{}),
		ownPID: ownPID,
	}

	var dispatched atomic.Int32
	loopErr := make(chan struct{})
	go func() {
		fw.runFanotifyLoopWithOverflow(func(_ DeployedToken, _ int32) {
			dispatched.Add(1)
		}, lw.safeDispatchFanotifyOverflow)
		close(loopErr)
	}()

	overflowBuf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_Q_OVERFLOW, -1, 0)
	// Write two separate overflow buffers — alert rate-limiting must coalesce to 1 alert.
	if _, err := unix.Write(fanPipe[1], overflowBuf); err != nil {
		t.Fatalf("write overflow #1: %v", err)
	}
	select {
	case p := <-payloadCh:
		if !strings.Contains(p.Detail, "events lost") {
			t.Errorf("overflow alert detail = %q; want substring %q", p.Detail, "events lost")
		}
		if strings.Contains(p.Path, "\x1b") || strings.ContainsRune(p.Path, '\u202e') {
			t.Errorf("overflow alert path leaked unsanitized chars: %q", p.Path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first FAN_Q_OVERFLOW alert")
	}

	if _, err := unix.Write(fanPipe[1], overflowBuf); err != nil {
		t.Fatalf("write overflow #2: %v", err)
	}

	// Verify loop kept running after overflows by sending a self-PID event,
	// then waiting briefly before cleanly signalling stop.
	selfBuf := makeFanotifyEvent(fanotifyMetadataSize, fanotifyMetadataVersion, unix.FAN_OPEN, -1, fw.ownPID)
	if _, err := unix.Write(fanPipe[1], selfBuf); err != nil {
		t.Fatalf("write self follow-up: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	select {
	case <-loopErr:
		t.Fatal("runFanotifyLoopWithOverflow exited early on overflow; must keep running")
	default:
	}

	fw.signalStop()
	select {
	case <-loopErr:
	case <-time.After(3 * time.Second):
		t.Fatal("runFanotifyLoopWithOverflow did not exit on signalStop")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("received %d overflow alerts; want exactly 1 (rate-limited)", got)
	}
	if dispatched.Load() != 0 {
		t.Errorf("token dispatch called %d times on overflow; want 0", dispatched.Load())
	}
}

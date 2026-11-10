//go:build !privileged

package watch

// Benchmarks for the watch package.
//
// These benchmarks measure the overhead of snapshot read/write operations,
// which are on the critical path for watcher startup and reload.
//
// Run with: go test -bench=. -benchtime=5s -benchmem ./internal/watch/
// See scripts/bench.sh for the full benchmark script that generates BENCHMARKS.md.
//
// Latency-to-alert (time from file open to alert delivery) is UNKNOWN until
// scripts/bench.sh is run on target hardware with a live decoyd instance.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkWriteDeployedSnapshot measures snapshot write overhead.
func BenchmarkWriteDeployedSnapshot(b *testing.B) {
	td := b.TempDir()
	tokens := make([]DeployedToken, 20)
	for i := range tokens {
		tokens[i] = DeployedToken{
			ID:           fmt.Sprintf("bench%016x", i),
			Type:         "aws_credentials",
			DeployedPath: filepath.Join(td, fmt.Sprintf("cred-%d", i)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := WriteDeployedSnapshot(td, tokens); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReadDeployedSnapshot measures snapshot read overhead.
func BenchmarkReadDeployedSnapshot(b *testing.B) {
	td := b.TempDir()
	tokens := make([]DeployedToken, 20)
	for i := range tokens {
		tokens[i] = DeployedToken{
			ID:           fmt.Sprintf("bench%016x", i),
			Type:         "aws_credentials",
			DeployedPath: filepath.Join(td, fmt.Sprintf("cred-%d", i)),
		}
	}
	if err := WriteDeployedSnapshot(td, tokens); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ReadDeployedSnapshot(td); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInotifyOpenReadClose measures overhead of a single file open+read+close,
// which is the operation a credential-stealing process performs and that inotify detects.
// This is a lower bound on possible detection latency (the kernel event is generated
// during the open/read/close syscalls).
func BenchmarkInotifyOpenReadClose(b *testing.B) {
	td := b.TempDir()
	tokenPath := filepath.Join(td, "bench-credential")
	if err := os.WriteFile(tokenPath, []byte("bench-credential-content-not-real"), 0600); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(tokenPath) // #nosec G304 — test-generated path in TempDir
		if err != nil {
			b.Fatal(err)
		}
		buf := make([]byte, 64)
		f.Read(buf) //nolint:errcheck // benchmark: we just want the syscall overhead
		f.Close()
	}
}

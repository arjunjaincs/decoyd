#!/usr/bin/env bash
# scripts/bench.sh — Run internal/watch package microbenchmarks on Linux.
# End-to-end daemon benchmarks reported in docs/BENCHMARKS.md require running
# compiled `decoyd watch` against a loopback HTTP webhook; run this script only
# for snapshot/package microbenchmark checks.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BENCH_ARGS="-bench=. -benchtime=2s -benchmem -count=3"

echo "Host uname -r: $(uname -r)"
echo "Host go version: $(go version)"
lscpu | head -20 || true
echo ""
echo "Running Go package benchmarks in ./internal/watch/..."
cd "$REPO_ROOT" && go test $BENCH_ARGS ./internal/watch/

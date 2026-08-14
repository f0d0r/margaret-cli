#!/usr/bin/env bash
# Benchmark the margaret-tools scanner against a synthetic dataset.
#
# Measures two things:
#   - I/O bytes via strace: bytes read from the dataset directory (a proxy for
#     network traffic on a remote drive) vs. bytes written/read to temp files
#     (local disk spooling).
#   - Wall time and peak RSS via /usr/bin/time.
#
# Usage: ./bench/run.sh [dataset-dir]
#   dataset-dir defaults to /tmp/benchdata; it is regenerated if missing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/build/margaret-tools"
DATADIR="${1:-/tmp/benchdata}"
LOGDIR="$(mktemp -d)"
trap 'rm -rf "$LOGDIR"' EXIT

echo "==> building margaret-tools"
go build -o "$BIN" "$ROOT/cmd/margaret-tools"

if [ ! -d "$DATADIR/loose" ]; then
  echo "==> generating dataset"
  go run "$ROOT/cmd/bench" gen -out "$DATADIR"
fi
echo "==> dataset: $(du -sh "$DATADIR" | cut -f1)"

echo "==> I/O accounting (strace)"
strace -ff -yy -e trace=read,pread64,write,pwrite64 -o "$LOGDIR/io.log" \
  "$BIN" scan "$DATADIR" --workers 4 --failures-out "$LOGDIR/failures.json" \
  >"$LOGDIR/scan.stdout" 2>"$LOGDIR/scan.stderr"
go run "$ROOT/cmd/bench" report "$LOGDIR/io.log" "$DATADIR"
echo "    scan totals: $(grep -E 'Total|Succeeded|Failed' "$LOGDIR/scan.stdout" | tr '\n' ' ')"

echo "==> wall time + peak RSS (/usr/bin/time)"
/usr/bin/time -v "$BIN" scan "$DATADIR" --workers 4 --failures-out "$LOGDIR/failures2.json" \
  >/dev/null 2>"$LOGDIR/time.txt" || true
grep -E 'Elapsed \(wall clock\)|Maximum resident set size' "$LOGDIR/time.txt"
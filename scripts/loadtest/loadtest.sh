#!/usr/bin/env bash
# Local load test (slo.md "Load test plan"). Never run against a live host.
#   scripts/loadtest/loadtest.sh LABEL [runner flags...]
# Seeds (once) a pristine catalog in $LT_DIR/seed, copies it for the run,
# starts the binary pinned to one CPU (GOMAXPROCS=1, like the 1 vCPU host)
# with metrics on loopback, runs the scenarios on the other CPUs, then
# stops the binary with SIGTERM and reports the shutdown time.
set -euo pipefail
label=${1:-run}; shift || true
LT_DIR=${LT_DIR:-/tmp/market-loadtest}
PORT=${PORT:-18090}; MPORT=${MPORT:-19091}
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
mkdir -p "$LT_DIR"
go build -trimpath -o "$LT_DIR/market" ./cmd/market
go build -o "$LT_DIR/lt-run" ./scripts/loadtest/run
if [ ! -f "$LT_DIR/seed/data.db" ]; then
  go run ./scripts/loadtest/seed -dir "$LT_DIR/seed" -products "${PRODUCTS:-10000}" -sellers "${SELLERS:-500}"
fi
rm -rf "$LT_DIR/run" && cp -r "$LT_DIR/seed" "$LT_DIR/run"
# ANALYZE=1: fresh planner stats (as after PocketBase's nightly PRAGMA
# optimize); default keeps the stats a bulk import leaves behind
if [ "${ANALYZE:-0}" = 1 ]; then go run ./scripts/loadtest/explain -db "$LT_DIR/run/data.db" -analyze >/dev/null; fi
MARKET_FAKE_GATEWAY=1 MARKET_MAIL_PROVIDER=file MARKET_METRICS_ADDR=127.0.0.1:$MPORT GOMAXPROCS=1 \
  taskset -c 0 "$LT_DIR/market" serve --http=127.0.0.1:$PORT --dir="$LT_DIR/run" >"$LT_DIR/server.log" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
for _ in $(seq 100); do curl -fsS -o /dev/null "http://127.0.0.1:$PORT/" 2>/dev/null && break; sleep 0.2; done
echo "idle RSS after start: $(awk '/VmRSS/{print $2" "$3}' /proc/$pid/status)"
taskset -c 1-7 "$LT_DIR/lt-run" -base "http://127.0.0.1:$PORT" -metrics "http://127.0.0.1:$MPORT/metrics" \
  -data "$LT_DIR/run" -label "$label" "$@"
echo "peak RSS (VmHWM): $(awk '/VmHWM/{print $2" "$3}' /proc/$pid/status)"
t0=$(date +%s%N); kill -TERM $pid; wait $pid || true; t1=$(date +%s%N)
echo "shutdown after SIGTERM: $(( (t1-t0)/1000000 )) ms"
trap - EXIT

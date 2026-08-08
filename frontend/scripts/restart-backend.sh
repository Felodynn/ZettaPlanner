#!/usr/bin/env bash
# Restarts the development backend, for the reconnection check.
#
# A restart is the widest gap the system can produce: the replay ring is in
# memory by design, so nothing survives it and every client is told `resync`.
set -euo pipefail

BACKEND="${ZP_BACKEND_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../backend" && pwd)}"
ADDR="${ZP_ADDR:-:8090}"
ORIGIN="${ZP_DEV_ORIGIN:-http://localhost:5173}"
LOG="${ZP_BACKEND_LOG:-/tmp/zettaplanner-backend.log}"

pkill -f "bin/zettaplanner serve" 2>/dev/null || true
sleep 1

cd "$BACKEND"
ZP_ADDR="$ADDR" ZP_DEV_ORIGIN="$ORIGIN" setsid ./bin/zettaplanner serve >"$LOG" 2>&1 &

# Model validation runs at startup and fails loudly (CONTRACT §8), so the
# process is not ready the moment it is spawned.
for _ in $(seq 1 60); do
  if curl -fsS -m 2 "http://localhost${ADDR}/health" >/dev/null 2>&1; then
    echo "backend up"
    exit 0
  fi
  sleep 1
done

echo "backend did not come back; see $LOG" >&2
tail -20 "$LOG" >&2
exit 1

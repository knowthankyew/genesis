#!/usr/bin/env bash
# Orchestrates automated demo generation for Genesis
set -euo pipefail

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
REPO_ROOT="$(dirname "$DIR")"
cd "$REPO_ROOT"

HTTP_PORT="${GENESIS_HTTP_PORT:-8080}"
TCP_PORT="${GENESIS_TCP_PORT:-9000}"
SYSLOG_PORT="${GENESIS_SYSLOG_PORT:-5514}"

# Check for port conflicts before starting if lsof is available
if command -v lsof >/dev/null 2>&1; then
  for port in "$HTTP_PORT" "$TCP_PORT"; do
    if lsof -i :"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "❌ Error: Port $port is already in use by another process. Please free it before running demo-record." >&2
      exit 1
    fi
  done
fi

LOG_FILE="$(mktemp -t genesis-demo-server.XXXXXX)"
DATA_PID=""

echo "🚀 Starting Genesis engine on HTTP :$HTTP_PORT, TCP :$TCP_PORT, Syslog :$SYSLOG_PORT..."
./bin/genesis server \
  --tcp="127.0.0.1:$TCP_PORT" \
  --http="127.0.0.1:$HTTP_PORT" \
  --syslog="127.0.0.1:$SYSLOG_PORT" \
  > "$LOG_FILE" 2>&1 &
SERVER_PID=$!

cleanup() {
  echo "🛑 Stopping Genesis processes..."
  if [ -n "$DATA_PID" ]; then
    kill -SIGTERM "$DATA_PID" 2>/dev/null || true
  fi
  kill -SIGTERM "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  rm -f "$LOG_FILE"
}
trap cleanup EXIT INT TERM

# Wait for server readiness
echo "⏳ Waiting for Genesis server readiness..."
READY=0
for i in {1..30}; do
  if curl -s "http://127.0.0.1:$HTTP_PORT/stats" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 0.1
done

if [ "$READY" -ne 1 ]; then
  echo "❌ Error: Genesis server failed to start within 3 seconds. Server log:" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi
echo "✓ Genesis server is ready."

# Ingest high-volume digital exhaust stream
echo "🌊 Streaming 25,000 digital telemetry events into Genesis..."
"$DIR/demo-data-generator.sh" 25000 | ./bin/genesis pipe --endpoint="127.0.0.1:$TCP_PORT" --service="telemetry-pipeline" > /dev/null 2>&1 &
DATA_PID=$!

# Send sample syslog events over UDP without blocking or text expansion
SYSLOG_PORT="$SYSLOG_PORT" python3 - <<'EOF'
import socket
import os
import sys
import time

try:
    port = int(os.environ.get("SYSLOG_PORT", "5514"))
except ValueError:
    sys.stderr.write("Warning: Invalid SYSLOG_PORT, skipping UDP test\n")
    sys.exit(0)

sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for i in range(50):
    ts = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    msg = f'<{134 if i%2==0 else 131}>1 {ts} edge-router genesis 1234 - - UDP Syslog burst #{i}\n'.encode('utf-8')
    sock.sendto(msg, ('127.0.0.1', port))
sock.close()
EOF

# Give the stream a moment to index
sleep 1

# Launch Playwright recording
echo "🎬 Executing Playwright recorder..."
export GENESIS_HTTP_URL="http://127.0.0.1:$HTTP_PORT"
node "$DIR/record-demo.mjs"

if [ -n "$DATA_PID" ]; then
  wait "$DATA_PID" 2>/dev/null || true
  DATA_PID=""
fi

# Verification of output artifacts
GIF_PATH="docs/assets/genesis-demo.gif"
MP4_PATH="docs/assets/genesis-demo.mp4"

if [ ! -f "$MP4_PATH" ]; then
  echo "❌ Error: Expected MP4 artifact missing: $MP4_PATH" >&2
  exit 1
fi

if [ ! -f "$GIF_PATH" ]; then
  echo "❌ Error: Expected GIF artifact missing: $GIF_PATH" >&2
  exit 1
fi

# Determine file size portably
if stat -f%z "$GIF_PATH" >/dev/null 2>&1; then
  GIF_SIZE=$(stat -f%z "$GIF_PATH")
else
  GIF_SIZE=$(stat -c%s "$GIF_PATH")
fi

MIN_SIZE=500000
MAX_SIZE=10485760

if [ "$GIF_SIZE" -lt "$MIN_SIZE" ] || [ "$GIF_SIZE" -gt "$MAX_SIZE" ]; then
  echo "❌ Error: Generated GIF size ($GIF_SIZE bytes) outside acceptable bounds ($MIN_SIZE - $MAX_SIZE bytes)" >&2
  exit 1
fi

echo "✅ Artifact verification passed:"
ls -lh "$GIF_PATH" "$MP4_PATH"

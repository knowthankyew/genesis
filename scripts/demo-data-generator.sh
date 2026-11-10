#!/usr/bin/env bash
# Generates realistic high-velocity digital telemetry exhaust for Genesis demos
set -euo pipefail

COUNT="${1:-25000}"

COUNT="$COUNT" python3 - <<'EOF'
import sys
import os
import time
import json
import random

try:
    count = int(os.environ.get("COUNT", "25000"))
except ValueError:
    sys.stderr.write("Error: COUNT must be a valid integer\n")
    sys.exit(1)

now = int(time.time())

services = [
    ("mobile-app", "INFO", "Telemetry sync: battery=82%, network=5G, lat=37.7749, lon=-122.4194"),
    ("mobile-app", "INFO", "Foreground app state resume; cached tokens refreshed (4ms)"),
    ("ad-tracker", "INFO", "Beacon ping: user_uuid=8f3b-92c1, fingerprint_match=true, referrer=tiktok"),
    ("ad-tracker", "INFO", "Cross-site cookie match: partner_id=dsp_8812, bid_floor=0.045"),
    ("smart-sensor", "INFO", "Ambient sensor ping: temp=71.4F, humidity=43%, occupancy=true"),
    ("smart-camera", "INFO", "Video stream heartbeat: fps=30, bitrate=4.2Mbps, motion_events=0"),
    ("checkout-api", "INFO", "Payment authorization check: card_hash=4111..., fraud_risk_score=0.012"),
    ("auth-service", "WARN", "Rate limit threshold approached (92 req/sec) for client 192.168.1.42"),
    ("storage-node", "WARN", "Disk IOPS utilization at 78% on volume /dev/nvme0n1"),
    ("gateway", "INFO", "HTTP GET /api/v2/feed - 200 OK (11ms)"),
    ("edge-router", "INFO", "BGP route update received from AS15169; 412 prefixes imported")
]

# Write records
for i in range(count):
    # Offset timestamp slightly across the last 30 minutes up to now
    ts_offset = (count - i) % 1800
    ts_iso = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(now - ts_offset))
    
    # Insert special critical needle events at specific intervals
    if i == 500:
        line = {
            "timestamp": ts_iso,
            "level": "ERROR",
            "service": "auth-service",
            "message": "CRITICAL: Bearer token exposed in cleartext auth-header during retry timeout",
            "trace_id": "trace-9988-leak"
        }
    elif i == 1500:
        line = {
            "timestamp": ts_iso,
            "level": "FATAL",
            "service": "gateway",
            "message": "Upstream cluster redis-cache connection pool exhausted; shedding load",
            "trace_id": "trace-9989-fatal"
        }
    elif i == 3000:
        line = {
            "timestamp": ts_iso,
            "level": "ERROR",
            "service": "checkout-api",
            "message": "Database transaction deadlock on table customer_orders; rolled back",
            "trace_id": "trace-9990-deadlock"
        }
    elif i % 25 == 0:
        # Syslog RFC 5424 format variant
        pri = 134 if i % 2 == 0 else 131
        sys.stdout.write(f"<{pri}>1 {ts_iso} edge-router genesis 1234 - - Packet burst detected: {12000 + (i%500)} pps\n")
        continue
    else:
        svc, lvl, msg = random.choice(services)
        line = {
            "timestamp": ts_iso,
            "level": lvl,
            "service": svc,
            "message": f"[{i:05d}] {msg}"
        }
    
    sys.stdout.write(json.dumps(line) + "\n")
EOF

# Quickstart 2: Local Development & Docker Compose

This guide demonstrates capturing telemetry from local developer microservices using `genesis run`, `genesis pipe`, and Docker Compose's native `syslog` log driver.

---

## 1. Process Capture with `genesis run`

Run any command or microservice directly under `genesis run`. Standard output and standard error are displayed in your terminal while being multiplexed and streamed directly to Genesis:

```bash
genesis run --service auth-api -- go run ./cmd/api
```

Exit codes are transparently preserved, allowing drop-in use in Makefile and shell scripts.

---

## 2. Docker Compose Integration

Docker supports forwarding container logs to a remote syslog daemon via its built-in `syslog` log driver. Point your Compose stack directly to Genesis:

```yaml
version: "3.8"

services:
  api:
    image: my-company/api:latest
    ports:
      - "3000:3000"
    logging:
      driver: "syslog"
      options:
        syslog-address: "udp://127.0.0.1:5514"
        tag: "api-service"

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: secret
    logging:
      driver: "syslog"
      options:
        syslog-address: "udp://127.0.0.1:5514"
        tag: "postgres-db"
```

---

## 3. Verified Execution Transcript

Below is a verbatim terminal transcript recorded against Genesis on macOS Darwin `arm64`:

```text
# Step 1: Run process under genesis run
$ ./bin/genesis run --service auth-worker --endpoint 127.0.0.1:9000 -- sh -c 'echo "Worker initializing worker_id=42"; echo "Connection pool ready"; echo "[ERROR] Failed to ping Redis broker" >&2'
Worker initializing worker_id=42
Connection pool ready
[ERROR] Failed to ping Redis broker

# Step 2: Pipe JSON Lines into genesis pipe
$ printf '{"time":"2026-11-03T03:47:30Z","app":"checkout","level":"warn","msg":"Coupon expired"}\n{"time":"2026-11-03T03:47:31Z","app":"checkout","level":"info","msg":"Cart completed"}\n' | ./bin/genesis pipe --endpoint 127.0.0.1:9000 --service checkout
[genesis pipe] Successfully streamed 2 lines to 127.0.0.1:9000

# Step 3: Query recent logs
$ ./bin/genesis query --http http://127.0.0.1:8080 --since 10m
Queried 5 records in 27 µs (window: 10m)
--------------------------------------------------------------------------------
2026-11-03 03:47:25  [INFO ]  auth-worker     [-]  Connection pool ready
2026-11-03 03:47:25  [INFO ]  auth-worker     [-]  Worker initializing worker_id=42
2026-11-03 03:47:25  [ERROR]  auth-worker     [-]  [ERROR] Failed to ping Redis broker
2026-11-03 03:47:30  [WARN ]  checkout        [-]  Coupon expired
2026-11-03 03:47:31  [INFO ]  checkout        [-]  Cart completed

# Step 4: Live Tail SSE
$ ./bin/genesis tail --http http://127.0.0.1:8080
[genesis tail] Live streaming from http://127.0.0.1:8080 (Ctrl+C to stop)...
03:47:30 [WARN ] order-svc      [tr-tail-123] Payment card verification pending
```

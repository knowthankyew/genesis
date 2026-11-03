# Quickstart 3: Ephemeral CI Pipeline Aggregator

This guide demonstrates using Genesis in automated CI/CD pipelines (e.g. GitHub Actions, GitLab CI, CircleCI) as an ultra-fast, ephemeral telemetry sink during integration tests.

---

## 1. Why Genesis in CI?

1. **Zero External Egress:** Keeps proprietary test logs strictly within the runner sandbox.
2. **Deterministic Querying:** Inspect whether background services logged errors during a test suite without parsing giant stdout log dumps.
3. **Amnesiac Purge (`genesis burn`):** Reset engine state cleanly between parallel test phases or wipe memory before runner teardown.

---

## 2. GitHub Actions Workflow Example

```yaml
name: Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Build Genesis
        run: |
          go build -o /tmp/genesis ./cmd/server
          /tmp/genesis server -tcp=127.0.0.1:9000 -http=127.0.0.1:8080 &
          sleep 1

      - name: Run Test Suite with Log Capture
        run: |
          /tmp/genesis run --service integration-tests -- go test -v ./test/integration/...

      - name: Check for Errors Logged During Test
        if: always()
        run: |
          echo "=== Error Logs from Test Run ==="
          /tmp/genesis query --http http://127.0.0.1:8080 --level ERROR --since 15m

      - name: Amnesiac Purge
        if: always()
        run: |
          /tmp/genesis burn --http http://127.0.0.1:8080 -y
```

---

## 3. Verified Execution Transcript

Below is a verbatim terminal transcript recorded against Genesis on macOS Darwin `arm64`:

```text
# 1. Starting Genesis server in background
$ ./bin/genesis server -tcp=127.0.0.1:9000 -http=127.0.0.1:8080 &

# 2. Running test command under genesis run
$ ./bin/genesis run --service auth-worker --endpoint 127.0.0.1:9000 -- sh -c 'echo "Worker initializing worker_id=42"; echo "Connection pool ready"; echo "[ERROR] Failed to ping Redis broker" >&2'
Worker initializing worker_id=42
Connection pool ready
[ERROR] Failed to ping Redis broker

# 3. Asserting pipeline statistics
$ ./bin/genesis stats --http http://127.0.0.1:8080 --json
{
  "received": 3,
  "indexed": 3,
  "priority": 1,
  "dropped": {
    "malformed": 0,
    "queue_full": 0,
    "stopped": 0
  },
  "evicted": 0,
  "partitions": 1,
  "accounted_bytes": 1248,
  "heap_bytes": 8345120,
  "queue_depths": {
    "normal": 0,
    "priority": 0
  },
  "uptime_seconds": 12
}

# 4. Performing Amnesiac Purge
$ ./bin/genesis burn --http http://127.0.0.1:8080 -y
[Amnesiac Purge] Success: burned 3 entries at 2026-11-03T03:47:26Z

# 5. Verifying zero records remain in memory
$ ./bin/genesis query --http http://127.0.0.1:8080 --since 10m
Queried 0 records in 4 µs (window: 10m)
--------------------------------------------------------------------------------
```

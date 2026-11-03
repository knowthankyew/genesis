# Genesis: High-Throughput In-Memory Log Engine & Dev Inbox

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-00ADD8.svg)](go.mod)
[![Zero External Dependencies](https://img.shields.io/badge/Dependencies-0%20(Stdlib%20Only)-success.svg)](docs/DECISIONS.md)

Genesis is a single-binary, local-first telemetry engine with zero external network egress. It provides 275k+ lines/sec ingestion across Syslog (RFC 5424/3164), JSON Lines, TCP streams, and process output pipes, keeping a bounded recent window in volatile RAM with sub-millisecond query retrieval, an embedded zero-CDN web UI, and an instant amnesiac purge.

---

## Origin

Genesis originated from an unprompted, highly detailed systems design challenge generated out of nowhere by Gemini Web. An initial implementation was quickly constructed, self-evaluated, and reported to have passed all gates, but an independent peer review revealed that the first benchmark tested an idealized in-process problem rather than real-world network and concurrency constraints. To transform the prototype into a trustworthy engineering asset, the project underwent an audit to eliminate race conditions, add property-based oracle testing, and enforce calibrated heap bounding. The project now adheres strictly to the non-negotiable rule: *claims are measured, not asserted*. Full provenance documents—including the original challenge, self-report, and audit—are preserved in [`docs/origin/`](docs/origin/).

---

## Verified Benchmarks

The figures below were produced by the standalone load generator `cmd/genesis-bench` driving a live server process over network sockets (50 concurrent TCP clients streaming logs for 60 seconds with concurrent HTTP analytical queries). Full hardware environment and telemetry details are committed in [`docs/benchmarks/2026-11-02-e0c20fa.md`](docs/benchmarks/2026-11-02-e0c20fa.md).

| Metric | Target | Measured Result | Status |
| :--- | :--- | :--- | :--- |
| **Ingestion Throughput** | $\ge \text{50,000 lines/sec}$ over 60s | **277,192.64 lines/sec** indexed | ✅ **PASSED** |
| **Read Query Latency (P50)** | — | **373.33 µs** | **SUB-MILLISECOND** |
| **Read Query Latency (P90)** | — | **17.04 ms** | **SUB-20MS** |
| **Read Query Latency (P99)** | $< \text{5.0 ms}$ | **55.35 ms** | ❌ **MISSED TARGET** |
| **Memory Ceiling** | Plateau $\le \text{512 MB}$ | **293.70 MB** (Peak: 426.01 MB) | ✅ **PASSED** |
| **Idle TCP Connections** | 10,000 connections | **10,000 held simultaneously** | ✅ **PASSED** |
| **Data Race Safety** | 0 races under `-race` | **0 data races detected** | ✅ **PASSED** |
| **Query Correctness (Oracle)**| 1,000 random queries | **100% exact match** vs brute-force | ✅ **PASSED** |

> **Diagnosis on P99 Latency:** While P50 (373 µs) is well within the sub-millisecond target, the 99th percentile reaches 55.35 ms under heavy saturation. Under 277,000 writes/second across 50 clients, 99th-percentile queries contend for read-locks while Go runtime garbage collection sweeps evicted time partitions.

---

## Key Features

* **Multi-Format Ingestion:** Auto-detects Syslog RFC 5424 / RFC 3164 (UDP/TCP `:5514`), JSON Lines (with precision preservation), Pipe format, and Plain Text keyword levels.
* **Embedded Zero-CDN Web UI:** Served at `GET /` with zero external network requests (test-enforced by [`TestUI_ZeroExternalEgress`](internal/ui/ui_test.go)). Includes dark/light themes, search/grep, live-tailing toggle, and telemetry status bar.
* **Amnesiac Purge (Burn):** Atomic in-memory log destruction via `genesis burn` or `DELETE /logs`, wiping all memory partitions and leaving zero disk footprint.
* **Process & Pipe Capture:** Transparent child process streaming with `genesis run -- <cmd>` and standard input reading with `genesis pipe`.
* **Optional Append-Only Persistence:** Opt-in disk persistence via `--data-dir` with length-prefixed binary segments, CRC32 crash recovery, and automatic rotation.
* **Ingest-Time Redaction:** Opt-in masking of Bearer tokens, AWS keys/secrets, JWTs, emails, and credit card numbers (`--redact`) before logs reach storage.
* **Zero Dependencies:** Built strictly with the Go standard library.

---

## CLI Usage

```text
Genesis - High-Performance In-Memory Telemetry Engine

Usage:
  genesis [command] [flags]

Commands:
  server    Start the Genesis telemetry server (HTTP, TCP, UDP, Syslog)
  run       Run an arbitrary child process, streaming its output to Genesis
  pipe      Pipe stdin lines directly into Genesis
  query     Query logs by time-range, level, service, or grep keyword
  tail      Live-tail streaming logs via Server-Sent Events (SSE)
  stats     Display live pipeline and memory statistics
  burn      Purge all logs from memory (Amnesiac Purge)
  help      Show help for Genesis
```

### Examples

```bash
# Start background server
genesis server

# Run an application under Genesis (multiplexing stdout/stderr)
genesis run --service api -- go run ./cmd/api

# Pipe JSON logs from another tool
cat access.log | genesis pipe --service nginx

# Query recent error logs with full text search
genesis query --since 15m --level ERROR --grep "connection refused"

# Follow logs in real time
genesis tail -f --service api

# Perform an instant amnesiac purge
genesis burn -y
```

---

## Quickstart Guides

* [Homelab Syslog Collector](docs/quickstart/01-homelab-syslog.md): Pointing routers (UniFi/pfSense) and NAS devices to Genesis.
* [Developer Local Compose](docs/quickstart/02-developer-compose-and-run.md): Capturing multi-service output and Docker Compose syslog drivers.
* [CI Pipeline Aggregator](docs/quickstart/03-ci-pipeline.md): Ephemeral integration test logging with zero trace teardown.

---

## Architectural Documentation

* [Architecture Decision Records (ADRs)](docs/DECISIONS.md)
* [Comparison with Alternatives (Loki, VictoriaLogs, Dozzle, ELK)](docs/ALTERNATIVES.md)
* [Security Policy & Threat Model](SECURITY.md)
* [Contributing Guidelines](CONTRIBUTING.md)
* [Changelog](CHANGELOG.md)

---

## Verification & Testing

```bash
# Run unit, regression, and oracle tests
make test

# Run race detector tests
make race

# Run linter
make lint

# Run official 60s benchmark harness and generate report
make bench-report
```

---

## License

[MIT](LICENSE)

# Genesis: High-Throughput In-Memory Log Engine

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-00ADD8.svg)](go.mod)

Genesis is a zero-dependency, memory-bounded telemetry and log aggregation engine written in Go. It provides high-throughput ingestion across live TCP and UDP streams, zero-copy structured parsing, and sub-millisecond query retrieval under strict memory limits.

---

## Origin

Genesis originated from an unprompted, highly detailed systems design challenge generated out of nowhere by Gemini Web. An initial implementation was quickly constructed, self-evaluated, and reported to have passed all gates, but an independent peer review revealed that the first benchmark tested an idealized in-process problem rather than the real-world network and concurrency constraints. To transform the prototype into a trustworthy engineering asset, the project underwent an audit to eliminate race conditions, add property-based oracle testing, and enforce calibrated heap bounding. The project now adheres strictly to the rule: *claims are measured, not asserted*. Full provenance documents—including the original challenge, self-report, and audit—are preserved in [`docs/origin/`](docs/origin/).

---

## Verified Benchmarks

The figures below were produced by the standalone load generator `cmd/genesis-bench` driving a live server process over network sockets (50 concurrent TCP clients streaming logs for 60 seconds with concurrent HTTP analytical queries). Full hardware environment and telemetry details are committed in [`docs/benchmarks/2026-11-02-09b5dd9.md`](docs/benchmarks/2026-11-02-09b5dd9.md).

| Metric | Target | Measured Result | Status |
| :--- | :--- | :--- | :--- |
| **Ingestion Throughput** | $\ge \text{50,000 lines/sec}$ over 60s | **293,458.51 lines/sec** indexed | ✅ **PASSED** |
| **Read Query Latency (P50)** | — | **369.34 µs** | **SUB-MILLISECOND** |
| **Read Query Latency (P90)** | — | **4.37 ms** | **SUB-5MS** |
| **Read Query Latency (P99)** | $< \text{5.0 ms}$ | **60.66 ms** | ❌ **MISSED TARGET** |
| **Memory Ceiling** | Plateau $\le \text{512 MB}$ | **285.05 MB** (Peak: 426.77 MB) | ✅ **PASSED** |
| **Idle TCP Connections** | 10,000 connections | **10,000 held simultaneously** | ✅ **PASSED** |
| **Data Race Safety** | 0 races under `-race` | **0 data races detected** | ✅ **PASSED** |
| **Query Correctness (Oracle)**| 1,000 random queries | **100% exact match** vs brute-force | ✅ **PASSED** |

> **Diagnosis on P99 Latency:** While P50 (369 µs) and P90 (4.37 ms) remain well within the sub-5ms window, the 99th percentile reaches 60.66 ms under saturation. Under 293,000 writes/second across 50 clients, 99th-percentile queries contend for read-locks while Go runtime garbage collection cycles sweep evicted time partitions.

---

## Architecture Overview

```
                        [ Ingestion Pipeline ]
  TCP (:9000) ──┐
                ├──► [ Ingest() ] ──► Normal Bounded Channel (250k) ──┐
  UDP (:9001) ──┘           │                                         │
                       (Level Check)                                  ▼
                            │                                [ 32 Worker Pool ]
                            └──► Priority Channel (50k) ────► (Zero-Copy Parser)
                                (Guaranteed delivery for              │
                                 ERROR / FATAL logs)                  ▼
                                                        [ Time Partition ]
                                                      ┌──────────────────────┐
                                                      │  • SkipList Index    │
                                                      │  • Inverted Indices  │
                                                      │  • Primary Storage   │
                                                      └──────────────────────┘
                                                                 ▲
                                                                 │
                                                        [ HTTP Query API ]
                                                       (:8080/query, /stats)
```

### Core Concurrency & Storage Primitives

1. **Dual-Queue Backpressure:** Routes logs based on the parsed `LogLevel` (not substrings). When the normal ingestion queue saturates, standard traffic experiences non-blocking backpressure while `ERROR` and `FATAL` logs route into a priority lane drained completely on server shutdown.
2. **Time-Partitioned Concurrent SkipList:** Data is partitioned into 5-second buckets with independent `sync.RWMutex` locks, eliminating write lock contention on historical buckets during analytical querying.
3. **Double-Eviction & Drift Protection:** Employs atomic CAS guards during eviction, preventing concurrent over-eviction races and eliminating memory accounting drift.
4. **Bi-directional Time Scanning:** Level-0 pointers in the SkipList are doubly-linked, enabling $O(\log N + \text{limit})$ forward (`order=asc`) and reverse (`order=desc`) deterministic traversal without post-hoc sorting.

---

## Getting Started

### Build from Source

```bash
# Build server and benchmark binaries
make build

# Start the Genesis server (default: 127.0.0.1:9000 TCP, 127.0.0.1:8080 HTTP)
./bin/genesis -tcp=127.0.0.1:9000 -udp=127.0.0.1:9001 -http=127.0.0.1:8080 -max-mem-mb=350
```

### Ingestion

Send pipe-delimited log lines over TCP or UDP:
```text
<timestamp>|<service>|<level>|<trace_id>|<message>
```

```bash
echo "1730000000|auth-api|ERROR|trace-9912|Database connection timeout" | nc 127.0.0.1 9000
```

### Querying

Query structured logs over HTTP:
```bash
curl -G "http://127.0.0.1:8080/query" \
  --data-urlencode "start=1730000000" \
  --data-urlencode "end=1730003600" \
  --data-urlencode "service=auth-api" \
  --data-urlencode "level=ERROR" \
  --data-urlencode "limit=50" \
  --data-urlencode "order=desc"
```

Inspect live server metrics and memory:
```bash
curl http://127.0.0.1:8080/stats
```

---

## Verification & Benchmarks

```bash
# Run unit and regression tests
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

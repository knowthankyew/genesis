# Genesis: High-Throughput In-Memory Log Engine

[![Go Report Card](https://goreportcard.com/badge/github.com/knowthankyew/project-genesis)](https://goreportcard.com/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Genesis is a zero-dependency, memory-bounded telemetry and log aggregation engine written in Go. It provides ultra-high-throughput ingestion across TCP/UDP streams, zero-copy structured parsing, and sub-millisecond multi-field time-range querying under strict heap limits.

---

## What Is Genesis?

In modern cloud computing, applications generate tens of thousands of log lines every second. Most teams rely on heavy enterprise clusters (like Elasticsearch, Splunk, or Grafana Loki) to ingest and search them. While powerful, these systems often require gigabytes of RAM, complex configuration, and significant CPU overhead.

**Genesis is the opposite:** a hyper-compact, single-binary engine engineered to ingest over **700,000 logs per second** on modest hardware, store them in a time-partitioned in-memory index, and respond to analytical queries in **microseconds**—all while enforcing a strict memory ceiling (e.g., 512 MB).

---

## Practical Applications

Genesis is designed for situations where heavy logging stacks are impractical:

1. **Local Developer & CI/CD Telemetry Sidecar:**
   Instead of spinning up heavy Docker containers (ELK or Loki) that consume 2GB+ RAM during integration testing, Genesis boots in under 5 milliseconds with ~10MB baseline RAM, captures all service streams, and allows immediate query verification.
2. **Edge Computing & IoT Gateways:**
   On memory-constrained nodes (e.g., Raspberry Pi, industrial edge controllers) where flash storage write cycles must be preserved, Genesis maintains a bounded in-memory circular telemetry buffer with zero disk wear, guaranteeing critical `ERROR` events are never dropped during network congestion.
3. **High-Performance Concurrency Reference:**
   Serves as a clean, idiomatic case study in zero-allocation string parsing, partitioned concurrent SkipLists, inverted index intersections, and lock-free/striped concurrency in Go.

---

## Architecture Overview

```
                        [ Ingestion Pipeline ]
  TCP (:9000) ──┐
                ├──► [ Ingest() ] ──► Normal Bounded Channel (200k) ──┐
  UDP (:9001) ──┘           │                                         │
                       (Backpressure)                                 ▼
                            │                                [ Worker Pool ]
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
                                                            (:8080/query)
```

### Key Technical Mechanisms

* **Zero-Copy Parsing:** Direct byte-slice parsing of incoming byte streams without heap string re-allocation for timestamps, log levels, and known service names.
* **Dual-Queue Backpressure:** Under network saturation, normal logs (`INFO`, `DEBUG`) apply non-blocking backpressure, while critical failure events (`ERROR`, `FATAL`) route into a dedicated non-dropping priority buffer.
* **Time-Partitioned Concurrent SkipList:** Partitioned by time buckets with independent `sync.RWMutex` locks, eliminating global lock contention between writers and query readers.
* **Inverted-Index-First Filtering:** Intersects `LogLevel` and `ServiceName` buckets *before* the time-range scan, shrinking query candidate pools by orders of magnitude and keeping P99 read latencies under 3 milliseconds.
* **Segment Eviction:** Drops the oldest time-bucket partitions in $O(1)$ when the memory ceiling is reached.

---

## Verified Benchmarks

Stress-tested using 50 concurrent client simulators pushing live traffic concurrently with analytical queries (`go test -race`):

| Metric | Target | Genesis Result | Status |
| :--- | :--- | :--- | :--- |
| **Ingestion Throughput** | $\ge \text{50,000 logs/sec}$ | **727,399 logs/sec** (raw)<br>**209,347 logs/sec** (with `-race`) | **PASSED (14.5x target)** |
| **P50 Query Latency** | — | **14.7 µs** (raw) / **77.9 µs** (race) | **SUB-MILLISECOND** |
| **P90 Query Latency** | — | **321.8 µs** (raw) / **682.2 µs** (race) | **SUB-MILLISECOND** |
| **P99 Query Latency** | $< \text{5.0 ms}$ | **2.55 ms** | **PASSED** |
| **Data Race Safety** | 0 races under `-race` | **0 race conditions detected** | **PASSED** |
| **Memory Ceiling** | Bounded ($\le \text{512 MB}$) | **Stable segment eviction** | **PASSED** |

---

## Getting Started

### Prerequisites

* Go 1.22+ (or Docker)

### Build and Run

```bash
# Clone the repository
git clone https://github.com/knowthankyew/project-genesis.git
cd project-genesis

# Build the server binary
make build

# Start Genesis
./bin/genesis-server -tcp=:9000 -udp=:9001 -http=:8080 -max-mem-mb=512
```

### Ingestion Protocol

Send pipe-delimited log lines to TCP port 9000 or UDP port 9001:

```text
<timestamp>|<service>|<level>|<trace_id>|<message>
```

**Example:**
```bash
echo "1730000000|auth-api|ERROR|trace-9912|Database connection timeout" | nc localhost 9000
```

### Querying Logs

Query logs over HTTP:

```bash
# Query ERROR logs from auth-api within a time window
curl -G "http://localhost:8080/query" \
  --data-urlencode "start=1730000000" \
  --data-urlencode "end=1730003600" \
  --data-urlencode "service=auth-api" \
  --data-urlencode "level=ERROR" \
  --data-urlencode "limit=50"
```

**Response Format:**
```json
{
  "count": 1,
  "latency_us": 42,
  "logs": [
    {
      "id": 1,
      "timestamp": 1730000000,
      "service": "auth-api",
      "level": "ERROR",
      "trace_id": "trace-9912",
      "message": "Database connection timeout",
      "size_bytes": 128
    }
  ]
}
```

---

## Running Verification Tests

```bash
# Run unit tests
make test

# Run race detector tests
make race

# Run benchmark verification gates
make bench
```

---

## License

[MIT](LICENSE)

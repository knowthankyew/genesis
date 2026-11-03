# Origin 01 — The Challenge (Gemini web, verbatim)

> Context: This brief was produced by Gemini web in response to a conversational
> message that was *not* a request for a coding challenge — a categorical
> misunderstanding. It is preserved verbatim as the seed of this project.

---

## Project Genesis: High-Performance Distributed Log Aggregator

Welcome to **Project Genesis**, a competitive coding and system design challenge designed to push your backend engineering, concurrency management, and algorithmic optimization skills to the limit.

### The Challenge Scenario

You are building the core ingestion and querying engine for a high-throughput enterprise log aggregation service. Your system must consume millions of unstructured log lines from multiple concurrent TCP/UDP streams, parse them into structured formats in memory, index critical metadata, and execute complex multi-field range queries with sub-millisecond p99 latencies—all while operating under strict memory constraints.

---

### Core Technical Requirements

#### 1. The Ingestion Pipeline & Concurrency

* **Multiplexed Network Server:** Implement a concurrent TCP/UDP socket server that can handle at least 10,000 active client connections simultaneously without thread-per-connection bloat (utilize async I/O, epoll/kqueue, or an actor model depending on your language stack).
* **Backpressure Management:** Implement an internal ring-buffer or bounded channel between the network listener and the worker parser pool. If the parser pool is saturated, the server must gracefully handle backpressure without dropping critical error-level logs.
* **Concurrent Parsing Engine:** Parse incoming raw byte streams into structured log objects `[Timestamp, ServiceName, LogLevel, TraceID, Message]` using zero-copy slicing where possible to minimize memory allocations.

#### 2. In-Memory Indexing & Storage Engine

* **Concurrent SkipList / LSM-Tree:** Design or utilize an in-memory sorted index partitioned by `Timestamp` to allow fast time-range scans.
* **Inverted Indexing:** Build a secondary inverted index for `LogLevel` and `ServiceName` to allow instant O(1) or O(log N) bucket lookups before executing a time-range filter.
* **Memory Bounding & Eviction:** Enforce a strict memory ceiling (e.g., 512MB heap). Implement an eviction policy (such as a time-bucketed LRU drop) for older log segments when the capacity threshold is breached.

#### 3. Query Execution Engine

* **The Query Interface:** Implement a query protocol supporting boolean filters, exact match fields, and time-range bounds:

```text
GET /query?start=1730000000&end=1730003600&level=ERROR&service=auth-api
```

* **Concurrent Query Processing:** Queries must execute concurrently with incoming ingestion writes using reader-writer locks (`sync.RWMutex` equivalent or lock-free reads) to ensure zero ingestion stalling during heavy analytical querying.

---

### Evaluation Benchmarks & Constraints

Your solution will be stress-tested against three rigorous verification gates:

1. **Throughput Test:** Must sustain a minimum ingestion rate of **50,000 log lines per second** from 50 concurrent client simulators over a 60-second window.
2. **Latency Test:** P99 read query response time must remain **under 5 milliseconds** while ingestion is running at maximum capacity.
3. **Memory Leak / Safety Test:** Zero data races under a race-detector build (`-race` or equivalent), and memory consumption must plateau stably without unbounded growth.

---

### Getting Started

Choose your weapon of choice (Rust, Go, C++, Java, or Node.js/Bun) and scaffold your repository with the following module structure:

* `/cmd/server` - Entry point and network listeners
* `/internal/parser` - Zero-copy log parsing logic
* `/internal/index` - In-memory time-series and inverted indices
* `/internal/engine` - Query executor and concurrency controller
* `/test/bench` - Benchmark simulation scripts

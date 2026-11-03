<!-- diagram-meta
last-validated: 2026-11-02
validated-commit: 2572d08
covers: [cmd/, internal/]
-->

# Genesis Architecture Specification (`knowthankyew/genesis`)

**Genesis** is a single-binary, zero-dependency, in-memory log and telemetry engine written in pure Go 1.22 standard library (`go.mod` contains zero external dependencies).

---

## 1. End-to-End Ingestion, Indexing & Query Pipeline

```mermaid
flowchart TD
    subgraph IngestListeners["Localhost-by-Default Listeners (127.0.0.1)"]
        Entry["🚀 CLI Entrypoint<br/><code>cmd/server/main.go</code>"]
        Server["🌐 TCP / UDP / Syslog / HTTP Server<br/><code>internal/cli/server.go</code>"]
        CLI["💻 Process Wrapper &amp; Pipe CLI<br/><code>internal/cli/run.go</code> &bull; <code>internal/cli/pipe.go</code>"]
    end

    subgraph SecurityLayer["Ingest Scrubbing &amp; Access Control"]
        Redact["🛡️ PII &amp; Credential Redactor<br/><code>internal/redact/redact.go</code>"]
        Auth["🔐 Constant-Time Bearer Auth<br/><code>internal/security/security.go</code>"]
    end

    subgraph CoreEngine["Dual-Channel QoS Engine &amp; Auto-Parser"]
        Engine["⚡ Dual-Channel Priority Engine<br/><code>internal/engine/engine.go</code>"]
        Parser["🔍 Multi-Format Auto-Detector<br/><code>internal/parser/multiformat.go</code> &bull; <code>internal/parser/parser.go</code>"]
    end

    subgraph StorageIndex["Time-Partitioned Index &amp; Optional Persistence"]
        Index["🗂️ 5s Time-Partitioned StoreIndex<br/><code>internal/index/index.go</code>"]
        SkipList["⛓️ Doubly-Linked Concurrent SkipList<br/><code>internal/index/skiplist.go</code>"]
        Disk["💾 Optional CRC32 Segment Store<br/><code>internal/storage/storage.go</code>"]
    end

    subgraph ClientSurface["Zero-Egress Web UI &amp; Query Surface"]
        WebUI["🖥️ Embedded Zero-CDN Web UI<br/><code>internal/ui/ui.go</code> &bull; <code>internal/ui/assets/index.html</code>"]
        Bench["📊 Live Socket Load Harness<br/><code>cmd/genesis-bench/main.go</code>"]
    end

    Entry --> Server & CLI
    CLI -->|"TCP (:9000)"| Server
    Bench -->|"50 TCP Clients / 10k Conns"| Server
    Server --> Redact --> Engine
    Engine -->|"Optional Append"| Disk
    Disk -.->|"Startup IngestReplay"| Engine
    Engine -->|"PriorityCh (ERROR/FATAL) &amp; NormalCh"| Parser
    Parser --> Index
    Index --> SkipList
    Server --> Auth --> WebUI
    WebUI -->|"GET /query, /stats, /tail &bull; DELETE /logs"| Engine
```

---

## 2. Core Subsystem Invariants

1. **Dual-Channel QoS Priority Routing (`internal/engine/engine.go`):**
   - Incoming lines are inspected for `ERROR` or `FATAL` severity and routed to `priorityCh` ahead of `normalCh`.
   - Worker goroutines always drain `priorityCh` before processing normal traffic, guaranteeing zero priority drops under saturation (`277,192.64 lines/sec` indexed over 60s).
2. **Time-Partitioned Doubly-Linked SkipList (`internal/index/index.go`, `internal/index/skiplist.go`):**
   - Logs are bucketed into 5-second partitions with per-partition inverted maps (`byLevel`, `byService`) and a doubly-linked concurrent SkipList (`RangeScanAsc` / `RangeScanDesc` in $O(\log N + K)$ time).
   - When accounted bytes reach `--max-mem-mb`, the oldest inactive partition is evicted in $O(1)$ time without per-node GC scanning.
3. **Amnesiac Purge (`Burn`):**
   - `DELETE /logs` (`HandleBurn`) atomically replaces the partition map, resets byte counters, unlinks all `.seg` files if `--data-dir` is active, and records a single audit log line.
4. **Zero External Egress & Localhost-by-Default:**
   - All listeners bind to `127.0.0.1` unless `--allow-all-interfaces` is explicitly passed.
   - `internal/ui/assets/index.html` contains zero external URLs (`http://` or `https://`), enforced by `TestUI_ZeroExternalEgress` and `scripts/audit-claims.py`.

---

## 3. Partition Lifecycle, Eviction & Amnesiac Burn Sequence

```mermaid
sequenceDiagram
    participant Client as TCP / Syslog / Pipe Client
    participant Engine as Engine (internal/engine/engine.go)
    participant Index as StoreIndex (internal/index/index.go)
    participant Disk as DiskStore (internal/storage/storage.go)
    participant User as Web UI / CLI (internal/cli/burn.go)

    Client->>Engine: Ingest(rawLine)
    Engine->>Disk: Append(redactedLine) [if --data-dir active]
    Engine->>Index: Insert(entry) [PriorityCh drained before NormalCh]
    Index->>Index: Evict oldest 5s partition in O(1) if CurrentBytes > MaxMemoryBytes
    User->>Engine: DELETE /logs (Amnesiac Burn)
    Engine->>Index: Burn() [Atomically clear partitions & reset byte counters]
    Engine->>Disk: Burn() [Unlink all .seg files]
    Engine-->>User: 200 OK {"status":"burned","purged":N}
```


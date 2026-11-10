<!-- landscape-meta
last-reviewed: 2026-11-10
review-cadence-days: 180
reviewed-versions: [journalctl@systemd-256, dozzle@v8.11, vector@v0.43, fluent-bit@v3.2, loki@v3.4, victorialogs@v1.10, quickwit@v0.8, opensearch@v2.18]
-->

# When to Use Genesis (and When NOT To)

Engineering requires picking the right tool for the job. Genesis is built for a very specific problem: **local-first, privacy-guaranteed ephemeral log inboxing with sub-millisecond querying and zero external network transmission.**

If your problem is different, you should probably use something else. Here is an honest, candid comparison against common logging stacks.

---

## Comparison Matrix

| Solution | Storage Model | Memory Footprint | Network Footprint | Best Used For | Where It Falls Down |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Genesis** | Volatile RAM (Opt-in disk) | Strict Ceiling (350MB) | Zero Egress | Local microservices, dev inbox, CI test sinks, homelab routers | Long-term multi-month analytics, distributed multi-node clustering |
| **`journalctl` / `grep`** | Disk (`/var/log/journal`) | Minimal | Zero Egress | Single Linux machine system inspection | Multi-process correlation, live UI, multi-format parsing, Windows/macOS |
| **Dozzle** | In-Memory tail | Very Small | Zero Egress | Simple Docker container log viewing | Non-Docker processes, syslog, structured query indexing, alerting |
| **Vector / Fluent Bit** | Pipeline Buffer | Low to Medium | Network Heavy | High-speed log collection, transformation, and routing to remote sinks | Lacks local query indexing, no embedded UI, requires external destination |
| **Grafana Loki** | Chunk Storage (S3 / GCS / disk) | Medium to High | Network Required | Large Kubernetes clusters, Grafana dashboards | Local development overhead, requires Loki + Promtail/Grafana agents |
| **VictoriaLogs** | Disk (Compressed block storage) | Low to Medium | Local or Remote | High-volume single-node long-term retention | Ephemeral zero-disk workflows, lacks embedded zero-CDN browser UI |
| **Quickwit** | Split Index (Cloud/Local Disk) | Low to Medium | Local or Remote | Sub-second search over cloud object storage (S3/GCS) | Real-time volatile RAM streaming, lacks live SSE tailing and instant amnesiac burn |
| **Elasticsearch / OpenSearch** | Inverted Index on Disk | Heavy (2GB–32GB+ RAM) | Network Heavy | Enterprise full-text search, compliance, security SIEM | Resource overhead, Java runtime footprint, complex cluster management |

---

## Detailed Tool Breakdown

### 1. `journalctl` & `grep`
* **Where it fits:** Inspecting system-level services on Linux hosts (systemd). Nothing beats `journalctl -u nginx -f` on a headless server.
* **Where Genesis fits instead:** When developing on macOS or Windows, when correlating output across independent non-systemd processes, or when you need a single searchable view across multiple Docker containers and UDP syslog feeds.

### 2. Dozzle
* **Where it fits:** Viewing live container stdout/stderr through a browser with zero setup.
* **Where Genesis fits instead:** Dozzle is primarily designed for Docker container environments. Genesis accepts logs from arbitrary child processes (`genesis run`), standard input pipes (`genesis pipe`), network syslog routers, and JSON lines over TCP/UDP sockets.

### 3. Vector & Fluent Bit
* **Where they fit:** Universal log forwarders and routers. When you need to collect millions of events from Kubernetes nodes or edge devices, filter/transform them, and route them to Kafka, S3, or Datadog, Vector and Fluent Bit are exceptional choices.
* **Where Genesis fits instead:** Vector and Fluent Bit are transport pipelines, not in-memory search destinations. They require an external database or indexing engine to query logs. Genesis provides ingestion, real-time sharded indexing, sub-millisecond queries, and an embedded Web UI in a single self-contained binary.

### 4. Grafana Loki
* **Where it fits:** Enterprise Kubernetes fleets where log streams need to be tagged with Prometheus labels and queried via LogQL in Grafana dashboards.
* **Where Genesis fits instead:** Running Loki locally for dev/test requires running Loki, Promtail, Grafana, and MinIO. Genesis is a single static Go binary with zero external dependencies, ready to ingest in under a millisecond.

### 5. VictoriaLogs
* **Where it fits:** Production logging on single instances where disk compression and SQL-like querying over months of data are needed.
* **Where Genesis fits instead:** Ephemeral development workflows, transient CI test runs, and privacy-sensitive local auditing where data must be held strictly in volatile RAM and instantly purged (`genesis burn`).

### 6. Quickwit
* **Where it fits:** Cost-effective search and analytics over massive datasets stored on cloud object storage (AWS S3, Google Cloud Storage, MinIO).
* **Where Genesis fits instead:** Quickwit is designed for immutable block indexing into object storage. Genesis is designed for local volatile RAM with continuous ring-buffer eviction, zero disk traces, and atomic memory destruction.

### 7. Elasticsearch / OpenSearch
* **Where they fit:** Enterprise-wide centralized SIEM, security compliance, complex schema mapping, and petabyte-scale search across distributed clusters.
* **Where Genesis fits instead:** Elasticsearch requires a JVM, multiple gigabytes of baseline memory before ingesting a single line, and complex index lifecycle management. Genesis runs comfortably in under 350 MB on a laptop.

---

## Living Document & Freshness Policy

This document is governed by an automated **180-day freshness cadence** tracked in the `landscape-meta` header and audited by `scripts/audit-claims.py`.

Our commitment is intellectual honesty:
* If any alternative listed here has evolved to address a limitation noted above, please open an issue or PR with the updated version details.
* If a new local-first or ephemeral logging engine emerges that developers evaluate alongside Genesis, we actively welcome its addition to this comparison.
* We re-evaluate all listed projects semi-annually against their latest stable releases, recording an audit note of any material changes to cited trade-offs.
* **Radar & Emerging Tools:** We monitor emerging single-binary and embedded-database experiments (such as SQLite-based loggers, LiteLog, Glintlog, or DuckDB/Parquet engines) for evaluation in future review cycles.

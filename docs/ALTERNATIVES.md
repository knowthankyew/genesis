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
| **Grafana Loki** | Chunk Storage (S3 / GCS / disk) | Medium to High | Network Required | Large Kubernetes clusters, Grafana dashboards | Local development overhead, requires Loki + Promtail/Grafana agents |
| **VictoriaLogs** | Disk (Compressed block storage) | Low to Medium | Local or Remote | High-volume single-node long-term retention | Lightweight ephemeral runs, lacks embedded zero-CDN browser UI |
| **Elasticsearch / OpenSearch** | Inverted Index on Disk | Heavy (2GB–32GB+ RAM) | Network Heavy | Enterprise full-text search, compliance, security SIEM | Massive resource overhead, Java runtime bloat, setup complexity |

---

## Detailed Tool Breakdown

### 1. `journalctl` & `grep`
* **Where it fits:** Inspecting system-level services on Linux hosts (systemd). Nothing beats `journalctl -u nginx -f` on a headless server.
* **Where Genesis fits instead:** When developing on macOS or Windows, when correlating output across independent non-systemd processes, or when you need a single searchable view across multiple Docker containers and UDP syslog feeds.

### 2. Dozzle
* **Where it fits:** Viewing live container stdout/stderr through a browser with zero setup.
* **Where Genesis fits instead:** Dozzle only works with Docker. Genesis accepts logs from arbitrary child processes (`genesis run`), standard input pipes (`genesis pipe`), network syslog routers, and JSON lines over TCP sockets.

### 3. Grafana Loki
* **Where it fits:** Enterprise Kubernetes fleets where log streams need to be tagged with Prometheus labels and queried via LogQL in Grafana dashboards.
* **Where Genesis fits instead:** Running Loki locally for dev/test requires running Loki, Promtail, Grafana, and MinIO. Genesis is a single 15MB static Go binary with zero external dependencies, ready to ingest in 1 millisecond.

### 4. VictoriaLogs
* **Where it fits:** Multi-terabyte production logging on single instances where disk compression and SQL-like querying over months of data are needed.
* **Where Genesis fits instead:** Ephemeral development workflows, transient CI test runs, and privacy-sensitive local auditing where data must be held strictly in volatile RAM and instantly purged (`genesis burn`).

### 5. Elasticsearch / OpenSearch
* **Where it fits:** Enterprise-wide centralized SIEM, security compliance, complex schema mapping, and petabyte-scale search.
* **Where Genesis fits instead:** Elasticsearch requires a JVM, multiple gigabytes of baseline memory before ingesting a single line, and complex index lifecycle management. Genesis runs comfortably in under 350 MB on a laptop.

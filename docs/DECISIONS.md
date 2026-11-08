# Architecture Decision Records (ADRs)

This document records the foundational architectural decisions and trade-offs made in Project Genesis, adhering to the principle: *Standard library only for the core, claims measured, and privacy-first invariants.*

---

## ADR 1: Standard Library Only — JSON & Environment Variables over YAML/TOML

* **Context:** Genesis requires a file-based configuration mechanism to supplement CLI flags and environment variables. External libraries such as `gopkg.in/yaml.v3` or `github.com/pelletier/go-toml` are commonly used in the Go ecosystem.
* **Decision:** We strictly enforce **zero external dependencies** for the core engine. Configuration files will be read as standard JSON (`genesis.json`). All configuration values can also be supplied via `GENESIS_*` environment variables or CLI flags.
* **Consequences:** 
  - Prevents supply-chain attacks and transitive CVE risks.
  - Eliminates dependency maintenance and binary bloat.
  - Zero compilation overhead and instant builds.

---

## ADR 2: Optional Disk Persistence — Length-Prefixed Append-Only Segments with CRC32

* **Context:** Genesis is an in-memory-first telemetry inbox. However, users occasionally need short-term crash resilience across restarts.
* **Decision:** Disk persistence is strictly **opt-in** via `--data-dir` (default: disabled / empty). When enabled:
  - Writes length-prefixed binary frames: `[4-byte payload length][4-byte IEEE CRC32][payload bytes]`.
  - Rotates append-only segment files (`.seg`) at <!-- claim:max_segment_mb -->16 MB<!-- /claim --> boundaries.
  - On restart, replays newest valid segments into memory, cleanly discarding any incomplete/torn writes at the end of a crashed segment.
  - On `genesis burn` (or `DELETE /logs`), all `.seg` files are unlinked and wiped immediately, maintaining the Amnesiac Purge invariant.
* **Consequences:** Simple, robust crash recovery with no heavy database engine (SQLite, BoltDB, or RocksDB).

---

## ADR 3: Ingest-Time Redaction — Fail-Safe Regex Sanitization

* **Context:** Development and staging logs frequently leak credentials, bearer tokens, and customer PII into telemetry systems.
* **Decision:** We provide opt-in ingest sanitization (`--redact` or `GENESIS_REDACT=true`). Sanitization runs at the parser boundary *before* lines enter priority queues, SkipLists, or disk segments.
* **Consequences:** Ensures sensitive secrets are never stored in memory partitions, never streamed over SSE live tails, and never written to persistence segments.

---

## ADR 4: Security Model — Bearer Tokens and Localhost Defaults

* **Context:** Genesis is designed primarily for local development and private homelabs. However, teams may expose Genesis on internal VPNs or shared servers.
* **Decision:**
  - Default bind is strictly `127.0.0.1`. Binding to `0.0.0.0` requires explicit `-allow-all-interfaces`.
  - Optional Bearer token authentication via `Authorization: Bearer <token>` or `?token=<token>` (specifically for browser `EventSource` live-tails).
  - Optional TLS encryption via `--tls-cert` and `--tls-key`.

---

## ADR 5: Golden-Ratio Jittered Cooperative TCP Backpressure

* **Context:** Under a 50-stream unthrottled TCP flood, bursts exceed single-node indexing capacity once the bounded `normalCh` queue fills. Dropping excess lines alone leaves producers spinning at wire speed, while fixed-duration backoff sleeps cause concurrent connections to synchronize into periodic wakeup herds ("timer lockstep") that spike tail query latencies. Seeding per-connection RNGs from wall-clock timestamps risks identical seeds across sockets accepted within the same clock tick, whereas `crypto/rand` syscalls or shared mutex PRNGs add unnecessary hot-path overhead.
* **Decision:** In `internal/cli/server.go` (`handleTCPConnection`), each accepted TCP connection initializes a local 64-bit state via a lock-free atomic golden-ratio Weyl increment (`tcpJitterSeed.Add(0x9e3779b97f4a7c15) | 1`, where `0x9e3779b97f4a7c15` = $\lfloor 2^{64}/\varphi \rfloor$; the same Weyl step seeds per-shard `SkipList` height PRNGs in `internal/index/skiplist.go`). Because the golden ratio $\varphi = \frac{1+\sqrt{5}}{2}$ has the continued fraction $[1; 1, 1, \dots]$, it is Hurwitz-optimal—the real number hardest to approximate rationally—giving successive multiples modulo $2^{64}$ low-discrepancy spacing that resists periodic aliasing. On every second `ErrBackpressure` drop, each connection steps its state with 64-bit Marsaglia xorshift (`13, 7, 17`) to sleep `800–1,823 µs` (`800 + (jitter & 1023)` µs).
* **Consequences:**
  - Zero mutex contention and zero OS entropy syscalls on socket accept or backoff.
  - Concurrent streams accepted in the same microsecond receive maximally dispersed initial states and deterministically desynchronized per-stream sleep sequences.
  - Slowing socket reads fills kernel TCP receive windows smoothly, engaging cooperative sender flow control without synchronized timer herds.

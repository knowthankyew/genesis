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

# Changelog

All notable changes to Project Genesis are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.4.0] - 2026-11-08

### Changed
- **Sharded Micro-Segment Index (`internal/index`):** Split time partitions into 8 shards that seal 4,096-entry immutable micro-segments backed by flat `[]parser.LogEntry` slices, sorted inverted posting lists (`byService`, `byLevel`, `bySvcLevel`), shard-first deduplicated sealing scans, and reusable bounded binary-heap top-K collection, bringing 60-second live-socket end-to-end read query P99 latency (including full HTTP response body drain) down from `55.35 ms` to `4.29 ms` (`< 5.0 ms` target passed) at `1,046,322.42 lines/sec` TCP ingestion.
- **Zero-Pointer (`noscan`) SkipList Arena (`internal/index`):** Replaced heap-allocated pointer skip-list nodes with an index-addressed `[]skipNode` slice containing zero pointers, eliminating per-node GC mark scanning and write barriers across evicted segments.
- **Pooled Ingestion & Query Buffers (`internal/engine`, `internal/parser`):** Added `sync.Pool` buffer reuse for raw ingestion lines and HTTP query JSON serialization, zero-allocation service interning, and single-backing-string slicing for `TraceID` and `Message`.
- **Jittered Cooperative TCP Backpressure (`internal/cli`):** Added per-connection golden-ratio seeded microsecond backoff on repeated `ErrBackpressure` drops so saturated TCP clients engage kernel flow control smoothly without timer herds.

---

## [0.3.0] - 2026-11-02

### Added
- **Unified Configuration:** Support for `genesis.json`, `GENESIS_*` environment variables, and CLI flag hierarchy.
- **Opt-In Append-Only Disk Persistence:** Length-prefixed segment storage (`--data-dir`) with CRC32 IEEE checksums and crash recovery.
- **Ingest Redaction:** Built-in sanitization for Bearer tokens, AWS keys/secrets, JWTs, emails, and credit cards (`--redact`), plus user regexes.
- **Security & Auth:** HTTP Bearer token middleware (`--auth-token` / `GENESIS_AUTH_TOKEN`) with constant-time verification, optional TLS support (`--tls-cert`/`--tls-key`), and root `SECURITY.md`.
- **Daemon & Service Units:** macOS `launchd` plist and Linux `systemd` unit files in `deploy/`, plus a `docker-compose.example.yml` setup.
- **Packaging:** Local GoReleaser snapshot configuration (`.goreleaser.yaml`) and Homebrew formula template (`Formula/genesis.rb`).
- **Engineering Docs:** Architectural decisions in `docs/DECISIONS.md` and logging stack comparisons in `docs/ALTERNATIVES.md`.

---

## [0.2.0] - 2026-11-02

### Added
- **Multi-Format Ingestion:** Auto-detects RFC 5424 Syslog, RFC 3164 BSD Syslog, JSON Lines, and Plain Text keyword levels alongside legacy pipe format.
- **Embedded Hermetic Web UI:** Zero-CDN single-file HTML/CSS/JS served via `//go:embed` with dark/light themes, live stats, and SSE tailing.
- **Amnesiac Purge (Burn):** Atomic in-memory log destruction (`DELETE /logs` / `genesis burn`) leaving zero disk footprint and an audit trail.
- **Unified CLI:** Subcommands `server`, `run` (process capture), `pipe` (stdin), `query` (human time windows), `tail` (SSE stream), `stats`, and `burn`.
- **Quickstart Guides:** Verified homelab router syslog, developer compose, and CI ephemeral aggregator documentation in `docs/quickstart/`.

---

## [0.1.0] - 2026-11-02

### Changed
- **Honesty Pass:** Fixed 10 critical systems bugs identified in peer review (eviction/insert race, single partition overflow, non-substring priority routing, graceful queue drain, 64KB UDP buffers, input limits).
- **Oracle Property Testing:** Implemented randomized dual-run property tests asserting 100% exact tie-breaking parity between SkipLists and brute-force sorted slices.
- **Live Socket Benchmarking:** Replaced artificial in-process microbenchmarks with `cmd/genesis-bench` over real TCP/HTTP sockets.

---

## [0.0.0-origin] - 2026-11-02

### Added
- Initial unverified origin snapshot imported verbatim from `project-genesis` @ `d97ef33`.

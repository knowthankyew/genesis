# NOTICE & PRIVACY GOVERNANCE DISCLOSURE — GENESIS (`knowthankyew/genesis`)

**Genesis** is a high-performance, single-binary, in-memory log and telemetry engine written in 100% pure Go standard library (`go.mod` has zero external dependencies) as part of the [`knowthankyew`](https://github.com/knowthankyew) open-source portfolio.

---

## 1. Core Architectural & Privacy Invariants

| Invariant | Default Posture | Verification & Enforcement Mechanism |
| :--- | :--- | :--- |
| **1. Volatile Memory by Default & Amnesiac Purge (`Burn`)** | Logs live exclusively in bounded RAM (`--max-mem-mb 400`, 480 MB runtime ceiling). `DELETE /logs` or `genesis burn` purges all partitions and segments in $<5\text{ ms}$ and records a single timestamped audit line. | Verified by `TestStoreIndex_Burn`, `TestEngine_Burn_Endpoint`, and `TestStorage_Burn`. |
| **2. Zero External Dependencies (`Go Stdlib Only`)** | `go.mod` contains zero `require` directives. No third-party SDKs, telemetry exporters, or supply-chain dependencies are linked into the binary. | Enforced fail-closed by `scripts/audit-claims.py` and `go vet ./...`. |
| **3. Zero Web UI Network Egress** | Embedded Web UI (`internal/ui/assets/index.html`) is compiled into the binary via `//go:embed` with zero external CDN fonts, scripts, stylesheets, or outbound URLs (`http://` / `https://`). | Enforced fail-closed by `TestUI_ZeroExternalEgress` (`internal/ui/ui_test.go`) and `scripts/audit-claims.py`. |
| **4. Localhost-by-Default Network Binding** | TCP (`127.0.0.1:9000`), UDP (`127.0.0.1:9001`), Syslog (`127.0.0.1:5514`), and HTTP (`127.0.0.1:8080`) bind strictly to loopback unless `--allow-all-interfaces` is explicitly passed. | Enforced by `internal/config/config.go`, `internal/cli/server.go`, and `TestIsBindingAllInterfaces`. |
| **5. Empirical Benchmark Provenance ("Claims Are Measured, Not Asserted")** | Every throughput, latency, and memory number in `README.md` is produced by the committed socket load generator (`cmd/genesis-bench`) and recorded with hardware, OS, Go version, and git SHA in `docs/benchmarks/`. | Enforced fail-closed by `scripts/audit-claims.py` cross-checking `README.md` against `docs/benchmarks/2026-11-02-e0c20fa.md`. |

---

## 2. Dual-Mode Operation: Default Volatile vs. Opt-In Persistence & Security

1. **Default Consumer / Developer Mode (Volatile Air-Gap):**
   - Runs 100% in volatile RAM (`--data-dir ""`).
   - Binds exclusively to `127.0.0.1`.
   - When the process exits or `genesis burn` is executed, zero log data remains on disk.
2. **Opt-In Operator Overlays:**
   - **Append-Only Disk Persistence (`--data-dir <path>`):** Writes length-prefixed (`uint32` + `CRC32` IEEE checksum + payload) `.seg` files with age and byte retention pruning (`Prune(maxAge, maxBytes)`). `DELETE /logs` (`Burn`) unlinks all `.seg` files on disk in addition to wiping RAM.
   - **Ingest-Time PII & Secret Redaction (`--redact`):** Scrubs Bearer tokens, AWS Access Keys (`AKIA...`), JWTs, email addresses, credit card numbers, and custom regexes (`redact_patterns`) prior to memory indexing or disk persistence.
   - **Bearer Token Authentication (`--auth-token`) & TLS (`--tls-cert` / `--tls-key`):** Enforces constant-time (`crypto/subtle.ConstantTimeCompare`) Bearer authentication across HTTP endpoints (`/query`, `/stats`, `/tail`, `/burn`) and Web UI requests.

---

## 3. Licensing & Third-Party Attribution

- **License:** Permissive **MIT License** (`SPDX-License-Identifier: MIT`). See [LICENSE](LICENSE).
- **Third-Party Runtime Dependencies:** `0` (Pure Go 1.22 Standard Library).
- **CycloneDX SBOM:** Generated deterministically via `scripts/generate-sbom.py` (`bom.json`) and attested in GitHub Actions CI.

# Genesis Security Policy & Threat Model

Project Genesis is designed from the ground up around a core privacy and security invariant: **local-first volatile telemetry with zero external network egress**.

This document outlines the threat model, trust boundaries, network listeners, and security controls for Genesis operators.

---

## 1. Threat Model & Trust Boundaries

```
+-----------------------------------------------------------------------------------+
|                              LOCAL HOST BOUNDARY                                  |
|                                                                                   |
|  [Child Processes]      [Docker Containers]     [Local Tools / CLI]              |
|         |                        |                        |                       |
|         v (stdout/pipe)          v (syslog driver)        v (HTTP /query)         |
|  +-----------------------------------------------------------------------------+  |
|  |                             GENESIS SERVER                                  |  |
|  |                                                                             |  |
|  |  +--------------------+    +--------------------+    +-------------------+  |  |
|  |  | Ingest Redaction   | -> | Volatile RAM Index | -> | Amnesiac Purge    |  |  |
|  |  | (PII/Key Stripper) |    | (Zero Disk Egress) |    | (Burn Tombstone)  |  |  |
|  |  +--------------------+    +--------------------+    +-------------------+  |  |
|  |                                      |                                      |  |
|  |                                      v                                      |  |
|  |                             [Embedded Web UI]                               |  |
|  |                         (Zero External Requests)                            |  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
                                         |
                                         x  ZERO EXTERNAL EGRESS
                                         v
                              [Public Cloud / Internet]
```

### Who Can Send Logs?
* **Local Ingestion Interfaces:** By default, Genesis binds listeners strictly to `127.0.0.1` (`:9000` TCP, `:9001` UDP, `:5514` Syslog). Any process with access to loopback networking on the host machine can emit logs into Genesis.
* **Remote Ingestion (Homelab / LAN):** When explicitly configured to bind on non-loopback interfaces (using `-allow-all-interfaces` or `-syslog 0.0.0.0:5514`), devices within the local network (routers, hypervisors, NAS boxes) can stream unauthenticated UDP/TCP logs.
* **Threat Mitigation:** Never expose raw ingestion ports directly to the public internet. Use firewall rules, private subnets, or VPN tunnels (WireGuard / Tailscale) to restrict network access to trusted devices.
* **Bounded Ingestion & Resource Guardrails:** Incoming TCP/UDP lines default to a <!-- claim:default_max_line_kb -->64 KB<!-- /claim --> cap before truncation (`DefaultMaxLineLength`, configurable via `--max-line-len`), and the parser's string interning pool (`internService`) is capped at <!-- claim:max_service_pool_size -->1,024<!-- /claim --> cached service strings (`MaxServicePoolSize`).

### Who Can Read Logs?
* **Web UI & REST Query API:** Available over HTTP on `127.0.0.1:8080`.
* **Authentication:** When `GENESIS_AUTH_TOKEN` (or `--auth-token`) is set, all HTTP endpoints (`/`, `/query`, `/stats`, `/tail`, `/logs`) enforce constant-time Bearer token verification. Requests lacking a valid token are rejected with `401 Unauthorized`.
* **Transport Encryption:** For shared host or multi-node LAN deployments, optional TLS can be enabled via `--tls-cert` and `--tls-key`.

---

## 2. Ingest Redaction & Data Sanitization

When `--redact` (or `GENESIS_REDACT=true`) is enabled, all raw lines pass through an in-memory sanitizer before indexing or storage:
* **Bearer Tokens & Auth Headers:** Masked to `[REDACTED_BEARER]`.
* **AWS Keys & Secrets:** Masked to `[REDACTED_AWS_KEY]` and `[REDACTED_AWS_SECRET]`.
* **JSON Web Tokens (JWT):** Masked to `[REDACTED_JWT]`.
* **Email Addresses:** Masked to `[REDACTED_EMAIL]`.
* **Credit Card Numbers:** Masked to `[REDACTED_CARD]`.
* **Custom Patterns:** Operators can define supplemental regular expressions via `GENESIS_CUSTOM_REDACT`.

---

## 3. Amnesiac Purge (Burn) Guarantees

Executing `genesis burn` (or `DELETE /logs`):
1. Acquires global write locks across all time partitions.
2. Dereferences all SkipList nodes, inverted maps, and cached entry records for immediate Go garbage collection.
3. Resets memory tracking counters to zero.
4. If persistence was enabled, closes active file descriptors and unlinks all `.seg` disk segment files immediately.
5. Emits a single immutable audit line:
   ```text
   [AUDIT] burned N entries at <TIMESTAMP>
   ```

---

## 4. Reporting a Security Vulnerability

If you discover a security vulnerability or invariant violation in Project Genesis, please report it privately via GitHub Security Advisories or email `security@knowthankyew.org`. We aim to acknowledge and triage reports within 24 hours.

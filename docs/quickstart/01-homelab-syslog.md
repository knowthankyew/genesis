# Quickstart 1: Homelab Syslog Collector

This guide demonstrates configuring Genesis as a lightweight, zero-dependency syslog collector for homelab environments (e.g. Ubiquiti/pfSense routers, Synology/TrueNAS storage servers, and Proxmox hypervisors).

---

## 1. Architecture & Port Mapping

Genesis provides standard RFC 5424 and RFC 3164 UDP syslog ingestion alongside a zero-CDN embedded web UI:

| Service | Port | Protocol | Purpose |
| :--- | :--- | :--- | :--- |
| **Syslog Listener** | `:5514` | UDP | Ingest RFC 5424 / RFC 3164 Syslog |
| **TCP Stream** | `:9000` | TCP | High-throughput structured pipe |
| **Web UI & REST API** | `:8080` | HTTP | Embedded Web UI, SSE live tail, Query API |

---

## 2. Configuration Recipes

### Router / Firewall (pfSense / OPNsense / Ubiquiti UniFi)
In your router's administration interface:
1. Navigate to **Status** > **System Logs** > **Settings** (or **Remote Logging** in UniFi).
2. Check **Send log messages to remote syslog server**.
3. Set **Remote Syslog Server**: `<genesis-ip>:5514`.
4. Select log contents (Firewall events, System events).

### Linux Host (`/etc/rsyslog.d/50-genesis.conf`)
Forward all host logs to Genesis over UDP:
```text
*.* @127.0.0.1:5514;RSYSLOG_SyslogProtocol23Format
```
Restart `rsyslog`:
```bash
sudo systemctl restart rsyslog
```

---

## 3. Verified Execution Transcript

Below is a verbatim terminal transcript recorded against Genesis on macOS Darwin `arm64`:

```text
$ ./bin/genesis server -tcp=127.0.0.1:9000 -http=127.0.0.1:8080 -udp=127.0.0.1:9001 -syslog=127.0.0.1:5514
2026/11/02 21:47:12 ==========================================================
2026/11/02 21:47:12   GENESIS: High-Performance In-Memory Log Engine
2026/11/02 21:47:12 ==========================================================
2026/11/02 21:47:12 [HTTP] Web UI & API listening on http://127.0.0.1:8080
2026/11/02 21:47:12 [UDP] Ingestion Server listening on 127.0.0.1:9001 (64KB buffer)
2026/11/02 21:47:12 [TCP] Ingestion Server listening on 127.0.0.1:9000
2026/11/02 21:47:12 [Syslog UDP] Ingestion Server listening on 127.0.0.1:5514 (64KB buffer)

# Simulating incoming RFC 5424 & RFC 3164 syslog packets from homelab nodes:
$ echo "<165>1 2026-11-03T03:47:13Z router.home firewall 1234 tr-fw01 - DROP wan_in eth0 SRC=198.51.100.23 DST=192.168.1.10 PROTO=TCP SPT=44122 DPT=22" | nc -u -w1 127.0.0.1 5514
$ echo "<34>Nov  3 03:47:13 synology-nas storage-pool: Degraded RAID array detected on volume1" | nc -u -w1 127.0.0.1 5514
$ echo "<134>Nov  3 03:47:13 proxmox-01 pve-ha-crm: service vm:101 started successfully" | nc -u -w1 127.0.0.1 5514

# Querying with Genesis CLI:
$ ./bin/genesis query --http http://127.0.0.1:8080 --since 15m
Queried 3 records in 24 µs (window: 15m)
--------------------------------------------------------------------------------
2026-11-03 03:47:13  [INFO ]  firewall        [tr-fw01]  DROP wan_in eth0 SRC=198.51.100.23 DST=192.168.1.10 PROTO=TCP SPT=44122 DPT=22
2026-11-03 03:47:13  [FATAL]  storage-pool    [-]  Degraded RAID array detected on volume1
2026-11-03 03:47:13  [INFO ]  pve-ha-crm      [-]  service vm:101 started successfully

# Filtering by severity:
$ ./bin/genesis query --http http://127.0.0.1:8080 --level FATAL
Queried 1 records in 13 µs (window: 1h)
--------------------------------------------------------------------------------
2026-11-03 03:47:13  [FATAL]  storage-pool    [-]  Degraded RAID array detected on volume1

# Full text grep:
$ ./bin/genesis query --http http://127.0.0.1:8080 --grep "Degraded"
Queried 1 records in 16 µs (window: 1h)
--------------------------------------------------------------------------------
2026-11-03 03:47:13  [FATAL]  storage-pool    [-]  Degraded RAID array detected on volume1
```

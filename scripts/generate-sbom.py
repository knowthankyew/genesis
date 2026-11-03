#!/usr/bin/env python3
"""
generate-sbom.py
Generates a deterministic CycloneDX v1.5 JSON Software Bill of Materials (SBOM) for Genesis.
Documents application metadata, internal Go stdlib-only packages, and knowthankyew privacy properties.
"""

import json
import os
import platform
import sys
import uuid
from datetime import datetime, timezone


def generate_sbom(output_path: str = "bom.json", arch: str | None = None, version: str = "0.3.1") -> None:
    if not arch:
        machine = platform.machine().lower()
        arch = "arm64" if machine in ("arm64", "aarch64") else "amd64"

    epoch = os.environ.get("SOURCE_DATE_EPOCH")
    if epoch:
        try:
            ts = datetime.fromtimestamp(int(epoch), timezone.utc)
        except (ValueError, OverflowError):
            sys.exit(f"ERROR: Invalid SOURCE_DATE_EPOCH='{epoch}'. Expected integer timestamp.")
    else:
        ts = datetime.now(timezone.utc)
    now_iso = ts.strftime("%Y-%m-%dT%H:%M:%SZ")
    seed_epoch = epoch if epoch else "local"
    serial_uuid = f"urn:uuid:{uuid.uuid5(uuid.NAMESPACE_URL, f'knowthankyew/genesis/{version}/{arch}/{seed_epoch}')}"
    app_ref = f"pkg:golang/github.com/knowthankyew/genesis@{version}?arch={arch}"

    internal_packages = [
        ("cli", "Unified CLI subcommands (server, run, pipe, query, tail, stats, burn)"),
        ("config", "JSON, environment variable (GENESIS_*), and CLI flag configuration loader"),
        ("engine", "Dual-channel QoS priority ingestion engine, SSE live tail broadcaster, and HTTP handlers"),
        ("index", "Time-partitioned concurrent store index and doubly-linked SkipList with O(1) partition eviction"),
        ("parser", "Zero-allocation pipe parser and multi-format auto-detector (Syslog RFC 5424/3164, JSON Lines, Plain Text)"),
        ("redact", "Ingest-time PII and credential redactor (Bearer, AWS keys, JWT, email, credit card, custom regex)"),
        ("security", "Constant-time Bearer token HTTP authentication middleware"),
        ("storage", "Optional crash-safe append-only CRC32 segment persistence with age and size pruning"),
        ("ui", "Embedded single-file zero-CDN Web UI served via Go embed.FS"),
    ]

    components = []
    depends_on = []
    for pkg_name, desc in internal_packages:
        ref = f"pkg:golang/github.com/knowthankyew/genesis/internal/{pkg_name}@{version}"
        depends_on.append(ref)
        components.append(
            {
                "type": "library",
                "bom-ref": ref,
                "name": f"internal/{pkg_name}",
                "version": version,
                "scope": "required",
                "description": desc,
                "licenses": [{"license": {"id": "MIT"}}],
            }
        )

    sbom = {
        "$schema": "http://cyclonedx.org/schema/bom-1.5.schema.json",
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "serialNumber": serial_uuid,
        "version": 1,
        "metadata": {
            "timestamp": now_iso,
            "tools": {
                "components": [
                    {
                        "type": "application",
                        "publisher": "knowthankyew",
                        "name": "generate-sbom",
                        "version": "1.0.0",
                    }
                ]
            },
            "component": {
                "type": "application",
                "bom-ref": app_ref,
                "name": "genesis",
                "version": version,
                "description": "High-performance, zero-dependency in-memory log and telemetry engine in pure Go stdlib with O(1) partition eviction, dual-channel QoS priority routing, and instant amnesiac burn.",
                "licenses": [{"license": {"id": "MIT"}}],
                "properties": [
                    {"name": "knowthankyew:privacy:network_egress", "value": "deny"},
                    {"name": "knowthankyew:privacy:telemetry_mode", "value": "memory_only"},
                    {"name": "knowthankyew:privacy:burn_enabled", "value": "true"},
                    {"name": "knowthankyew:privacy:third_party_dependencies", "value": "0"},
                    {"name": "knowthankyew:security:localhost_default_bind", "value": "true"},
                ],
            },
        },
        "components": components,
        "dependencies": [
            {
                "ref": app_ref,
                "dependsOn": depends_on,
            }
        ],
    }

    out_abs = os.path.abspath(output_path)
    os.makedirs(os.path.dirname(out_abs), exist_ok=True)
    with open(out_abs, "w", encoding="utf-8") as f:
        json.dump(sbom, f, indent=2)
        f.write("\n")
    print(f"✓ CycloneDX SBOM written to {output_path}")


if __name__ == "__main__":
    out = sys.argv[1] if len(sys.argv) > 1 else "bom.json"
    arch_arg = sys.argv[2] if len(sys.argv) > 2 else None
    ver_arg = sys.argv[3] if len(sys.argv) > 3 else "0.3.1"
    generate_sbom(out, arch_arg, ver_arg)

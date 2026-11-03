#!/usr/bin/env python3
"""
audit-claims.py - Automated Architectural, Empirical Benchmark & Privacy Claims Linter for Genesis.

Enforces portfolio-wide knowthankyew quality gates:
1. Zero External Go Dependencies (go.mod stdlib-only check)
2. Zero Web UI Network Egress (internal/ui/assets/index.html static URL & primitive scan)
3. Empirical Benchmark Claim Parity (README.md numbers verified against docs/benchmarks/2026-11-02-e0c20fa.md)
4. Security & Privacy Code Invariants (127.0.0.1 default binds, DELETE-only /burn, no wildcard CORS on /tail)
5. Canonical MIT License, NOTICE.md, and CycloneDX 1.5 SBOM (bom.json) verification
6. Prohibited Marketing Vocabulary Guardrails across README.md, NOTICE.md, SECURITY.md, and docs/*.md
"""

import hashlib
import json
from pathlib import Path
import re
import sys

CANONICAL_MIT_SHA256 = "49f4b2686067fc56489ee83e4280c72a38b5c09d275bf352f5247d90572c8b9b"

PROHIBITED_WORDS = [
    re.compile(r"\bbullet-?proof\b", re.IGNORECASE),
    re.compile(r"\brock\s+solid\b", re.IGNORECASE),
    re.compile(r"\bconclusive\b", re.IGNORECASE),
    re.compile(r"\bdefinitive\b", re.IGNORECASE),
    re.compile(r"\binfinitely scalable\b", re.IGNORECASE),
]


def fail(errors: list[str], msg: str) -> None:
    errors.append(msg)


def read_text_checked(root: Path, rel_path: str, errors: list[str]) -> str | None:
    target = root / rel_path
    if not target.is_file():
        fail(errors, f"Missing required file: {rel_path}")
        return None
    try:
        return target.read_text(encoding="utf-8")
    except OSError as exc:
        fail(errors, f"Could not read {rel_path}: {exc}")
        return None


def check_zero_go_dependencies(root: Path, errors: list[str]) -> None:
    gomod = read_text_checked(root, "go.mod", errors)
    if gomod is None:
        return
    if "module github.com/knowthankyew/genesis" not in gomod:
        fail(errors, "go.mod does not declare 'module github.com/knowthankyew/genesis'")
    if re.search(r"^\s*require\b", gomod, flags=re.MULTILINE):
        fail(errors, "go.mod contains external 'require' directives (violates stdlib-only zero-dependency invariant)")
    if (root / "go.sum").is_file():
        gosum = read_text_checked(root, "go.sum", errors)
        if gosum and gosum.strip():
            fail(errors, "go.sum is non-empty (violates stdlib-only zero-dependency invariant)")


def check_ui_zero_egress(root: Path, errors: list[str]) -> None:
    html = read_text_checked(root, "internal/ui/assets/index.html", errors)
    if html is None:
        return

    forbidden_ui_patterns = [
        (r"https?://", "external http:// or https:// URL"),
        (r"\bnew\s+WebSocket\s*\(", "WebSocket constructor"),
        (r"\bnavigator\.sendBeacon\s*\(", "navigator.sendBeacon"),
        (r"\bnew\s+Image\s*\(", "new Image() beacon"),
    ]
    for pattern, label in forbidden_ui_patterns:
        if re.search(pattern, html, flags=re.IGNORECASE):
            fail(errors, f"Web UI zero-egress violation in internal/ui/assets/index.html: found {label}")


def check_empirical_benchmark_claims(root: Path, errors: list[str]) -> None:
    readme = read_text_checked(root, "README.md", errors)
    bench = read_text_checked(root, "docs/benchmarks/2026-11-02-e0c20fa.md", errors)
    if readme is None or bench is None:
        return

    required_pairs = [
        ("277192.64 lines/sec", "277,192.64 lines/sec", "sustained indexed throughput"),
        ("293.70 MB", "293.70 MB", "heap plateau under load"),
        ("426.01 MB", "426.01 MB", "heap peak under load"),
        ("373.33", "373.33 µs", "query latency p50"),
        ("17.04", "17.04 ms", "query latency p90"),
        ("55.35", "55.35 ms", "query latency p99 (honest saturation report)"),
        ("10000 held", "10,000 held simultaneously", "concurrent idle TCP connections"),
    ]
    for bench_str, readme_str, desc in required_pairs:
        if bench_str not in bench:
            fail(errors, f"Benchmark report docs/benchmarks/2026-11-02-e0c20fa.md missing expected {desc} '{bench_str}'")
        if readme_str not in readme:
            fail(errors, f"README.md drifted from benchmark report for {desc}: missing '{readme_str}'")

    if "MISSED TARGET" not in readme:
        fail(errors, "README.md must transparently disclose the P99 saturation target miss ('MISSED TARGET')")


def check_security_and_privacy_invariants(root: Path, errors: list[str]) -> None:
    cfg_go = read_text_checked(root, "internal/config/config.go", errors)
    if cfg_go is not None:
        for default_bind in ("127.0.0.1:9000", "127.0.0.1:9001", "127.0.0.1:5514", "127.0.0.1:8080"):
            if default_bind not in cfg_go:
                fail(errors, f"internal/config/config.go missing localhost default bind '{default_bind}'")

    eng_go = read_text_checked(root, "internal/engine/engine.go", errors)
    if eng_go is not None:
        if "Access-Control-Allow-Origin" in eng_go:
            fail(errors, "internal/engine/engine.go contains 'Access-Control-Allow-Origin' header (cross-origin SSE leak risk)")
        if "r.Method != http.MethodDelete" not in eng_go:
            fail(errors, "internal/engine/engine.go HandleBurn does not enforce strictly http.MethodDelete (CSRF risk)")
        if "SetWriteDeadline(time.Time{})" not in eng_go:
            fail(errors, "internal/engine/engine.go HandleTail does not clear connection write deadline for long-lived SSE")


def check_license_notice_and_sbom(root: Path, errors: list[str]) -> None:
    lic_path = root / "LICENSE"
    if not lic_path.is_file():
        fail(errors, "Missing root LICENSE file")
    else:
        sha = hashlib.sha256(lic_path.read_bytes()).hexdigest()
        if sha != CANONICAL_MIT_SHA256:
            fail(errors, f"LICENSE SHA-256 mismatch: expected {CANONICAL_MIT_SHA256}, got {sha}")

    notice = read_text_checked(root, "NOTICE.md", errors)
    if notice is not None:
        for required_phrase in ("Volatile Memory", "Amnesiac Purge", "Zero External Dependencies", "MIT License"):
            if required_phrase not in notice:
                fail(errors, f"NOTICE.md missing required governance disclosure phrase '{required_phrase}'")

    bom_text = read_text_checked(root, "bom.json", errors)
    if bom_text is not None:
        try:
            bom = json.loads(bom_text)
        except json.JSONDecodeError as exc:
            fail(errors, f"bom.json is not valid JSON: {exc}")
            return
        if bom.get("bomFormat") != "CycloneDX" or bom.get("specVersion") != "1.5":
            fail(errors, "bom.json must be CycloneDX specVersion 1.5")
        comp = bom.get("metadata", {}).get("component", {})
        if comp.get("name") != "genesis":
            fail(errors, f"bom.json metadata.component.name expected 'genesis', got '{comp.get('name')}'")
        props = {p.get("name"): p.get("value") for p in comp.get("properties", [])}
        expected_props = {
            "knowthankyew:privacy:network_egress": "deny",
            "knowthankyew:privacy:telemetry_mode": "memory_only",
            "knowthankyew:privacy:burn_enabled": "true",
            "knowthankyew:privacy:third_party_dependencies": "0",
        }
        for k, v in expected_props.items():
            if props.get(k) != v:
                fail(errors, f"bom.json property '{k}' expected '{v}', got '{props.get(k)}'")


def check_vocabulary_guardrails(root: Path, errors: list[str]) -> None:
    docs_to_scan = [
        root / "README.md",
        root / "NOTICE.md",
        root / "SECURITY.md",
        root / "CHANGELOG.md",
        root / "CONTRIBUTING.md",
    ]
    docs_dir = root / "docs"
    if docs_dir.is_dir():
        for md in sorted(docs_dir.rglob("*.md")):
            # Exclude historical origin record which preserves the unaltered initial snapshot
            if "origin" in md.parts:
                continue
            docs_to_scan.append(md)

    for doc_path in docs_to_scan:
        if not doc_path.is_file():
            continue
        rel = str(doc_path.relative_to(root))
        text = doc_path.read_text(encoding="utf-8")
        for idx, line in enumerate(text.splitlines(), 1):
            lower = line.lower()
            if "prohibit" in lower or "forbidden" in lower or "guardrail" in lower:
                continue
            for pattern in PROHIBITED_WORDS:
                if pattern.search(line):
                    fail(
                        errors,
                        f"Vocabulary violation in {rel}:{idx}: prohibited term '{pattern.pattern}' in '{line.strip()}'",
                    )


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    errors: list[str] = []

    check_zero_go_dependencies(root, errors)
    check_ui_zero_egress(root, errors)
    check_empirical_benchmark_claims(root, errors)
    check_security_and_privacy_invariants(root, errors)
    check_license_notice_and_sbom(root, errors)
    check_vocabulary_guardrails(root, errors)

    print("=== Genesis Claims, Benchmark Provenance & Privacy Invariant Audit (v0.3.1) ===")
    if errors:
        print(f"\n✗ FAILED: {len(errors)} invariant violation(s) detected:", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    print("  ✓ Zero external Go dependencies verified (go.mod stdlib-only)")
    print("  ✓ Embedded Web UI verified clean of external network egress")
    print("  ✓ README.md benchmark metrics verified against docs/benchmarks/2026-11-02-e0c20fa.md")
    print("  ✓ Localhost-by-default binds, DELETE-only /burn, and SSE deadline invariants verified")
    print("  ✓ Canonical MIT LICENSE, NOTICE.md, and CycloneDX 1.5 SBOM (bom.json) verified")
    print("✓ All Genesis claims and portfolio invariants passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

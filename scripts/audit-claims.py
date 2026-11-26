#!/usr/bin/env python3
"""
audit-claims.py - Unified Architectural & Empirical Claims Linter for Genesis (`knowthankyew/genesis`).

Provides portfolio-standard CLI discoverability (scripts/audit-claims.py) matching `ml`:
1. Empirical Claims Linter unit tests (tests/test_claims_linter.py)
2. Document Claims & Empirical Constraint Check across all registered docs (scripts/sync_docs.py --check)
3. Prohibited & Guarded Vocabulary Audit across README.md, NOTICE.md, SECURITY.md, and docs/*.md
4. Architecture Diagram Freshness & Path Existence Audit (diagram-meta covers & code paths)
5. Canonical MIT License, SPDX, NOTICE.md, and CycloneDX 1.5 SBOM (bom.json) Governance Gate
6. Genesis Go Stdlib Zero-Dependency, gofmt Cleanliness, Web UI Zero-Egress & Security Invariants
"""

import hashlib
import json
import os
import re
import subprocess
import sys
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT / "scripts"))
import sync_docs  # noqa: E402

DIAGRAM_META_RE = re.compile(r"<!--\s*diagram-meta\b(.*?)-->", re.DOTALL)
DIAGRAM_CODE_PATH_RE = re.compile(r"<code>([^<>\s]+)</code>")
DIAGRAM_PATH_PREFIXES = ("cmd/", "internal/", "scripts/", "docs/", ".github/", "tests/")
REQUIRED_DIAGRAM_DOC = "docs/ARCHITECTURE.md"

LANDSCAPE_META_RE = re.compile(r"<!--\s*landscape-meta\b(.*?)-->", re.DOTALL)
REQUIRED_LANDSCAPE_DOC = "docs/ALTERNATIVES.md"

# Genesis server core packages must never initiate outbound TCP/UDP/HTTP connections
# (CLI client commands in internal/cli/ and benchmark harness in cmd/genesis-bench/ are separate).
SERVER_CORE_PACKAGES = (
    "internal/config",
    "internal/engine",
    "internal/index",
    "internal/parser",
    "internal/redact",
    "internal/security",
    "internal/storage",
    "internal/ui",
)

RAW_GO_NETWORK_RE = re.compile(
    r"\bnet\.Dial(?:Timeout|TCP|UDP|IP|Unix)?\b"
    r"|(?<!\*)\bnet\.Dialer\b"
    r"|(?<!\*)\bhttp\.(?:Client|Transport)\b"
    r"|\bhttp\.Default(?:Client|Transport)\b"
    r"|\bhttp\.(?:Get|Head|Post|PostForm)\s*\("
)

CANONICAL_MIT_SHA256 = "49f4b2686067fc56489ee83e4280c72a38b5c09d275bf352f5247d90572c8b9b"
SPDX_PREFIX = "SPDX" + "-License-Identifier:"
SPDX_RE = re.compile(re.escape(SPDX_PREFIX) + r"\s*([^\r\n]+)")


def clean_spdx_identifier(raw: str) -> str:
    cleaned = re.sub(r"(-->|\*\/|\*|#|\/|\"|'|`)[\s\S]*$", "", raw).strip()
    return cleaned.rstrip(";`).,").strip()


def _git(*args: str) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(["git", *args], cwd=str(REPO_ROOT), capture_output=True, text=True)
    except FileNotFoundError as ex:
        return subprocess.CompletedProcess(
            args=["git", *args],
            returncode=127,
            stdout="",
            stderr=f"git executable not found: {ex}",
        )


def get_docs_to_scan() -> list[Path]:
    docs = [
        REPO_ROOT / "README.md",
        REPO_ROOT / "NOTICE.md",
        REPO_ROOT / "SECURITY.md",
        REPO_ROOT / "CHANGELOG.md",
        REPO_ROOT / "CONTRIBUTING.md",
    ]
    docs_dir = REPO_ROOT / "docs"
    if docs_dir.is_dir():
        for md in sorted(docs_dir.rglob("*.md")):
            if "origin" in md.parts:
                continue
            docs.append(md)
    return [d for d in dict.fromkeys(docs) if d.is_file()]


def audit_vocabulary(errors: list[str]) -> None:
    registry_path = REPO_ROOT / "docs" / "claims_registry.json"
    reg_ok, reg_errs = sync_docs.validate_claims_registry(registry_path, repo_root=REPO_ROOT)
    if not reg_ok:
        for e in reg_errs:
            errors.append(f"Registry hygiene violation: {e}")

    registered_docs = {
        "README.md",
        "NOTICE.md",
        "SECURITY.md",
        "docs/ARCHITECTURE.md",
        "docs/DECISIONS.md",
        "docs/ALTERNATIVES.md",
    }
    for doc in get_docs_to_scan():
        content = doc.read_text(encoding="utf-8")
        rel_name = str(doc.relative_to(REPO_ROOT)).replace("\\", "/")

        if rel_name in registered_docs:
            ok, word_errs = sync_docs.lint_prose_and_words(
                content, rel_name, registry_path=registry_path, repo_root=REPO_ROOT
            )
            if not ok:
                for e in word_errs:
                    errors.append(f"Vocabulary/Path violation: {e}")
        else:
            for idx, line in enumerate(content.splitlines(), 1):
                if sync_docs._has_personal_host_path_text(line):
                    errors.append(f"Vocabulary/Path violation in {rel_name}:{idx}: personal host path leak found.")
                for pat in sync_docs.PROHIBITED_WORDS:
                    if pat.search(line):
                        errors.append(
                            f"Vocabulary violation in {rel_name}:{idx}: "
                            f"Prohibited term matching '{pat.pattern}' found: '{line.strip()}'"
                        )

        m_ok, m_errs = sync_docs.lint_claim_markers_and_regions(
            content, rel_name, registry_path=registry_path
        )
        if not m_ok:
            for e in m_errs:
                errors.append(f"Marker/Region violation: {e}")


def parse_diagram_meta(text: str) -> dict | None:
    m = DIAGRAM_META_RE.search(text)
    if not m:
        return None
    meta: dict = {}
    for line in m.group(1).splitlines():
        key, sep, val = line.partition(":")
        if sep:
            meta[key.strip()] = val.strip()
    covers = meta.get("covers", "")
    if covers.startswith("[") and covers.endswith("]"):
        meta["covers"] = [c.strip() for c in covers[1:-1].split(",") if c.strip()]
    else:
        meta["covers"] = []
    return meta


def _audit_diagram_doc(doc: Path, errors: list[str]) -> None:
    try:
        rel = doc.relative_to(REPO_ROOT).as_posix()
    except ValueError:
        rel = doc.name
    label = f"Architecture diagram ({rel})"
    text = doc.read_text(encoding="utf-8")

    meta = parse_diagram_meta(text)
    if meta is None:
        errors.append(f"{label}: missing <!-- diagram-meta ... --> header")
        return

    raw_date = meta.get("last-validated", "")
    try:
        max_allowed_date = datetime.now(timezone.utc).date() + timedelta(days=1)
        if date.fromisoformat(raw_date) > max_allowed_date:
            errors.append(f"{label}: last-validated {raw_date} is in the future")
    except ValueError:
        errors.append(f"{label}: last-validated '{raw_date}' is not an ISO date (YYYY-MM-DD)")

    commit = meta.get("validated-commit", "")
    covers: list[str] = meta["covers"]
    if not commit:
        errors.append(f"{label}: validated-commit is missing")
    elif not re.fullmatch(r"[0-9a-fA-F]{7,40}", commit):
        errors.append(f"{label}: validated-commit '{commit}' must be a valid hex git commit hash")
    if not covers:
        errors.append(f"{label}: covers is missing or not a [a, b, c] list")

    if commit and re.fullmatch(r"[0-9a-fA-F]{7,40}", commit) and covers:
        resolved = _git("rev-parse", "--verify", "--quiet", f"{commit}^{{commit}}")
        if resolved.returncode != 0:
            errors.append(
                f"{label}: validated-commit '{commit}' not found in this clone "
                f"(shallow checkout? use fetch-depth: 0)"
            )
        elif _git("merge-base", "--is-ancestor", commit, "HEAD").returncode != 0:
            errors.append(f"{label}: validated-commit '{commit}' is not an ancestor of HEAD")
        else:
            pathspecs = [f":(glob){c}" for c in covers] + [
                f":(exclude){rel}",
                ":(exclude)scripts/audit-claims.py",
                ":(exclude)scripts/sync_docs.py",
                ":(exclude)tests/test_claims_linter.py",
            ]
            diff = _git("diff", "--name-only", commit, "--", *pathspecs)
            if diff.returncode != 0:
                errors.append(f"{label}: git diff failed: {diff.stderr.strip()}")
            else:
                changed = [f for f in diff.stdout.splitlines() if f.strip()]
                if changed:
                    last = _git("log", "-1", "--format=%h", "HEAD", "--", *pathspecs).stdout.strip()
                    shown = ", ".join(changed[:10]) + (" ..." if len(changed) > 10 else "")
                    errors.append(
                        f"{label}: STALE. {len(changed)} covered file(s) changed since "
                        f"{commit}: {shown}. Re-validate the diagram, then set "
                        f"validated-commit to {last} and update last-validated."
                    )

    for raw in dict.fromkeys(DIAGRAM_CODE_PATH_RE.findall(text)):
        p = raw.split("#", 1)[0]
        p = re.sub(r":\d+(-\d+)?$", "", p)
        if not p.startswith(DIAGRAM_PATH_PREFIXES) or any(ch in p for ch in "*{}<>$"):
            continue
        if not (REPO_ROOT / p).exists():
            errors.append(f"{label}: referenced path does not exist: {p}")


def audit_architecture_diagrams(errors: list[str]) -> None:
    required = REPO_ROOT / REQUIRED_DIAGRAM_DOC
    targets: list[Path] = []
    if required.is_file():
        targets.append(required)
    else:
        errors.append(f"Architecture diagram: required doc {REQUIRED_DIAGRAM_DOC} not found")
    for doc in get_docs_to_scan():
        if doc != required and "<!-- diagram-meta" in doc.read_text(encoding="utf-8"):
            targets.append(doc)
    for doc in targets:
        _audit_diagram_doc(doc, errors)


def parse_landscape_meta(text: str) -> dict | None:
    m = LANDSCAPE_META_RE.search(text)
    if not m:
        return None
    meta: dict = {}
    for line in m.group(1).splitlines():
        key, sep, val = line.partition(":")
        if sep:
            meta[key.strip()] = val.strip()
    versions = meta.get("reviewed-versions", "")
    if versions.startswith("[") and versions.endswith("]"):
        meta["reviewed-versions"] = [v.strip() for v in versions[1:-1].split(",") if v.strip()]
    else:
        meta["reviewed-versions"] = []
    return meta


def audit_landscape_freshness(errors: list[str], repo_root: Path = REPO_ROOT) -> None:
    doc_path = repo_root / REQUIRED_LANDSCAPE_DOC
    if not doc_path.is_file():
        errors.append(f"Landscape gate: required document {REQUIRED_LANDSCAPE_DOC} not found")
        return
    text = doc_path.read_text(encoding="utf-8")
    meta = parse_landscape_meta(text)
    if not meta:
        errors.append(f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): missing <!-- landscape-meta ... --> header")
        return

    raw_date = meta.get("last-reviewed", "")
    try:
        rev_date = date.fromisoformat(raw_date)
        max_allowed_date = datetime.now(timezone.utc).date() + timedelta(days=1)
        if rev_date > max_allowed_date:
            errors.append(f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): last-reviewed {raw_date} is in the future")
    except ValueError:
        errors.append(f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): last-reviewed '{raw_date}' is not an ISO date (YYYY-MM-DD)")
        return

    raw_cadence = meta.get("review-cadence-days", "180")
    try:
        cadence_days = int(raw_cadence)
    except ValueError:
        errors.append(
            f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): review-cadence-days '{raw_cadence}' is not a valid integer"
        )
        return
    if cadence_days < 1 or cadence_days > 180:
        errors.append(
            f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): review-cadence-days must be between 1 and 180, got {cadence_days}"
        )
        return

    today = datetime.now(timezone.utc).date()
    elapsed = (today - rev_date).days
    if elapsed > cadence_days:
        errors.append(
            f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): STALE. Last reviewed {raw_date} "
            f"({elapsed} days ago, exceeds cadence of {cadence_days} days). "
            f"Review external alternatives, verify claims against latest releases, and update last-reviewed."
        )

    versions = meta.get("reviewed-versions", [])
    if len(versions) < 3:
        errors.append(
            f"Landscape gate ({REQUIRED_LANDSCAPE_DOC}): reviewed-versions list must contain at least 3 pinned competitor versions"
        )


def audit_license_and_sbom(errors: list[str]) -> None:
    license_file = REPO_ROOT / "LICENSE"
    if not license_file.is_file():
        errors.append("License gate: root LICENSE file is missing")
    else:
        license_sha = hashlib.sha256(license_file.read_bytes()).hexdigest()
        if license_sha != CANONICAL_MIT_SHA256:
            errors.append(
                f"License gate: root LICENSE SHA-256 mismatch. Expected {CANONICAL_MIT_SHA256}, got {license_sha}."
            )

    ls_files = _git("ls-files", "-z")
    if ls_files.returncode == 0:
        for rel_path in ls_files.stdout.split("\0"):
            if not rel_path or rel_path == "scripts/audit-claims.py":
                continue
            p = REPO_ROOT / rel_path
            if not p.is_file():
                continue
            try:
                content = p.read_text(encoding="utf-8")
            except (UnicodeDecodeError, OSError):
                continue
            for spdx_raw in SPDX_RE.findall(content):
                spdx = clean_spdx_identifier(spdx_raw)
                if spdx != "MIT":
                    errors.append(
                        f"License gate: non-MIT SPDX identifier '{spdx}' found in {rel_path}. Only 'MIT' is permitted."
                    )

    notice_path = REPO_ROOT / "NOTICE.md"
    if not notice_path.is_file():
        errors.append("Governance gate: NOTICE.md is missing")
    else:
        notice = notice_path.read_text(encoding="utf-8")
        for phrase in ("Volatile Memory", "Amnesiac Purge", "Zero External Dependencies", "MIT License"):
            if phrase not in notice:
                errors.append(f"NOTICE.md missing required disclosure phrase '{phrase}'")

    bom_path = REPO_ROOT / "bom.json"
    if not bom_path.is_file():
        errors.append("Governance gate: bom.json is missing")
    else:
        raw_bom = bom_path.read_text(encoding="utf-8")
        if sync_docs._has_personal_host_path_text(raw_bom):
            errors.append("bom.json contains leaked personal host path")
        try:
            bom = sync_docs._loads_strict_json(raw_bom)
            if not isinstance(bom, dict):
                raise ValueError(f"bom.json root must be a JSON object, got {type(bom).__name__}")
            if bom.get("bomFormat") != "CycloneDX" or bom.get("specVersion") != "1.5":
                errors.append("bom.json must be CycloneDX specVersion 1.5")
            comp = bom.get("metadata", {}).get("component", {})
            if comp.get("name") != "genesis":
                errors.append(f"bom.json component name expected 'genesis', got {comp.get('name')!r}")
            props = {p.get("name"): p.get("value") for p in comp.get("properties", [])}
            expected_props = {
                "knowthankyew:privacy:network_egress": "deny",
                "knowthankyew:privacy:telemetry_mode": "memory_only",
                "knowthankyew:privacy:burn_enabled": "true",
                "knowthankyew:privacy:third_party_dependencies": "0",
                "knowthankyew:security:localhost_default_bind": "true",
            }
            for k, v in expected_props.items():
                if props.get(k) != v:
                    errors.append(f"bom.json property '{k}' expected '{v}', got {props.get(k)!r}")

            internal_dir = REPO_ROOT / "internal"
            actual_pkgs = {
                f"internal/{d.name}"
                for d in internal_dir.iterdir()
                if d.is_dir() and any(d.glob("*.go"))
            }
            sbom_pkgs = {c.get("name") for c in bom.get("components", []) if c.get("name")}
            missing_pkgs = sorted(actual_pkgs - sbom_pkgs)
            extra_pkgs = sorted(sbom_pkgs - actual_pkgs)
            if missing_pkgs or extra_pkgs:
                errors.append(
                    f"bom.json internal package parity drift: missing={missing_pkgs}, extra={extra_pkgs}"
                )
        except Exception as exc:
            errors.append(f"bom.json invalid JSON: {exc}")


def strip_go_comments_and_strings(src: str) -> str:
    """Replace Go comments and string/rune literals with spaces while preserving newlines for line numbers."""
    out: list[str] = []
    i = 0
    n = len(src)
    state = "code"  # code | line_comment | block_comment | dquote | rawstr | rune
    while i < n:
        ch = src[i]
        nxt = src[i + 1] if i + 1 < n else ""
        if state == "code":
            if ch == "/" and nxt == "/":
                state = "line_comment"
                out.append("  ")
                i += 2
                continue
            if ch == "/" and nxt == "*":
                state = "block_comment"
                out.append("  ")
                i += 2
                continue
            if ch == '"':
                state = "dquote"
                out.append(" ")
                i += 1
                continue
            if ch == "`":
                state = "rawstr"
                out.append(" ")
                i += 1
                continue
            if ch == "'":
                state = "rune"
                out.append(" ")
                i += 1
                continue
            out.append(ch)
            i += 1
        elif state == "line_comment":
            if ch == "\n":
                state = "code"
                out.append("\n")
            else:
                out.append(" ")
            i += 1
        elif state == "block_comment":
            if ch == "*" and nxt == "/":
                state = "code"
                out.append("  ")
                i += 2
            elif ch == "\n":
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1
        elif state == "dquote":
            if ch == "\\" and i + 1 < n:
                out.append("\n" if nxt == "\n" else "  ")
                i += 2
            elif ch == '"':
                state = "code"
                out.append(" ")
                i += 1
            else:
                out.append("\n" if ch == "\n" else " ")
                i += 1
        elif state == "rawstr":
            if ch == "`":
                state = "code"
                out.append(" ")
            else:
                out.append("\n" if ch == "\n" else " ")
            i += 1
        elif state == "rune":
            if ch == "\\" and i + 1 < n:
                out.append("\n" if nxt == "\n" else "  ")
                i += 2
            elif ch == "'":
                state = "code"
                out.append(" ")
                i += 1
            else:
                out.append("\n" if ch == "\n" else " ")
                i += 1
    return "".join(out)


def audit_go_server_network_egress_boundary(errors: list[str], repo_root: Path = REPO_ROOT) -> None:
    """
    Enforce that zero non-test Go files inside Genesis's server-core packages initiate
    outbound TCP/UDP/HTTP connections (preserving strict zero-egress server isolation).
    """
    for pkg_rel in SERVER_CORE_PACKAGES:
        pkg_dir = repo_root / pkg_rel
        if not pkg_dir.is_dir():
            continue
        for go_file in sorted(pkg_dir.rglob("*.go")):
            if go_file.name.endswith("_test.go"):
                continue
            rel = go_file.relative_to(repo_root).as_posix()
            cleaned = strip_go_comments_and_strings(go_file.read_text(encoding="utf-8"))
            for line_no, line in enumerate(cleaned.splitlines(), 1):
                for m in RAW_GO_NETWORK_RE.finditer(line):
                    errors.append(
                        f"Go server zero-egress boundary violation in {rel}:{line_no}: outbound primitive "
                        f"'{m.group(0).strip()}' forbidden in server-core package {pkg_rel}"
                    )


def audit_genesis_invariants(errors: list[str]) -> None:
    # 1. Zero external Go dependencies
    gomod_path = REPO_ROOT / "go.mod"
    if not gomod_path.is_file():
        errors.append("go.mod is missing")
    else:
        gomod = gomod_path.read_text(encoding="utf-8")
        if "module github.com/knowthankyew/genesis" not in gomod:
            errors.append("go.mod does not declare 'module github.com/knowthankyew/genesis'")
        if re.search(r"^\s*require\b", gomod, flags=re.MULTILINE):
            errors.append("go.mod contains external 'require' directives (violates stdlib-only invariant)")
    gosum_path = REPO_ROOT / "go.sum"
    if gosum_path.is_file() and gosum_path.read_text(encoding="utf-8").strip():
        errors.append("go.sum is non-empty (violates stdlib-only invariant)")

    # 2. Web UI zero network egress
    ui_path = REPO_ROOT / "internal" / "ui" / "assets" / "index.html"
    if not ui_path.is_file():
        errors.append("internal/ui/assets/index.html is missing")
    else:
        html = ui_path.read_text(encoding="utf-8")
        forbidden_ui_patterns = [
            (r"https?://", "external http:// or https:// URL"),
            (r"""(?:src|href|action|poster)\s*=\s*["']\s*//""", "protocol-relative URL attribute"),
            (r"""url\(\s*["']?\s*//""", "protocol-relative CSS url()"),
            (r"@import\b", "CSS @import"),
            (r"\bnew\s+WebSocket\s*\(", "WebSocket constructor"),
            (r"\bnavigator\.sendBeacon\s*\(", "navigator.sendBeacon"),
            (r"\bnew\s+Image\s*\(", "new Image() beacon"),
        ]
        for pat, label in forbidden_ui_patterns:
            if re.search(pat, html, flags=re.IGNORECASE):
                errors.append(f"Web UI zero-egress violation in internal/ui/assets/index.html: found {label}")

    # 3. Static Go server-core zero-egress boundary (zero outbound dialers/clients in server packages)
    audit_go_server_network_egress_boundary(errors, repo_root=REPO_ROOT)

    # 4. Security & privacy runtime invariants verified via Go test suite (assert each expected test actually passed)
    expected_invariant_tests = [
        ("github.com/knowthankyew/genesis/internal/engine", "TestSecurityInvariants_LoopbackAndBurnAndCORS"),
        ("github.com/knowthankyew/genesis/internal/ui", "TestUI_ZeroExternalEgress"),
    ]
    test_names = sorted({t for _, t in expected_invariant_tests})
    ok_inv, inv_out = run_check(
        [
            "go",
            "test",
            "-json",
            "./internal/engine",
            "./internal/ui",
            "-run",
            "^(" + "|".join(test_names) + ")$",
        ],
        "Go Security & Zero-Egress Invariant Tests",
        errors,
    )
    if ok_inv:
        passed_pairs: set[tuple[str, str]] = set()
        for line in inv_out.splitlines():
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            if (
                ev.get("Action") == "pass"
                and isinstance(ev.get("Package"), str)
                and isinstance(ev.get("Test"), str)
            ):
                passed_pairs.add((ev["Package"], ev["Test"]))
        missing_tests = [f"{pkg}::{t}" for pkg, t in expected_invariant_tests if (pkg, t) not in passed_pairs]
        if missing_tests:
            errors.append(
                f"Invariant Go test audit missed expected tests (renamed or skipped?): {', '.join(missing_tests)}"
            )


def run_check(cmd: list[str], label: str, errors: list[str]) -> tuple[bool, str]:
    try:
        proc = subprocess.run(cmd, cwd=str(REPO_ROOT), capture_output=True, text=True)
    except FileNotFoundError as exc:
        msg = f"{label} failed: executable not found ({exc})"
        errors.append(msg)
        return False, msg
    combined = f"{proc.stdout}\n{proc.stderr}"
    if proc.returncode != 0:
        errors.append(f"{label} failed (exit {proc.returncode}):\n{combined}")
        return False, combined
    return True, combined


def main() -> int:
    print("\n=== Genesis Empirical Claims, Provenance & Privacy Invariant Audit ===")
    errors: list[str] = []

    # 1. Run empirical claims linter unit test suite
    test_cmd = [sys.executable, "-m", "unittest", "tests/test_claims_linter.py"]
    ok_tests, test_out = run_check(test_cmd, "Empirical Claims Test Suite (test_claims_linter.py)", errors)
    if ok_tests:
        m = re.search(r"Ran\s+(\d+)\s+tests?", test_out)
        count_str = f"{m.group(1)} test cases" if m else "all test cases"
        print(f"  ✓ Empirical claims & Go constant bound tests passed ({count_str})")

    # 2. Run sync_docs claims, empirical blocks, and benchmark math check
    sync_cmd = [sys.executable, "scripts/sync_docs.py", "--check"]
    ok_sync, _ = run_check(sync_cmd, "Document Claims Sync Check (sync_docs.py --check)", errors)
    if ok_sync:
        print("  ✓ Benchmark math recomputation, empirical table blocks & multi-doc claims verified (sync_docs.py)")

    # 3. Audit prohibited and guarded vocabulary across docs
    before_vocab = len(errors)
    audit_vocabulary(errors)
    if len(errors) == before_vocab:
        print("  ✓ README.md, NOTICE.md, SECURITY.md, and docs/*.md verified clean of prohibited & unguarded vocabulary")

    # 4. Architecture diagram freshness & path existence
    before_diagram = len(errors)
    audit_architecture_diagrams(errors)
    if len(errors) == before_diagram:
        print("  ✓ Architecture diagram metadata is fresh and every referenced path exists")

    # 5. Canonical MIT license, SPDX & CycloneDX SBOM governance gate
    before_license = len(errors)
    audit_license_and_sbom(errors)
    if len(errors) == before_license:
        print("  ✓ 100% MIT canonical license integrity, SPDX identifiers, NOTICE.md & CycloneDX SBOM verified")

    # 6. Genesis Go stdlib zero-dep, Web UI zero-egress & security invariants
    before_inv = len(errors)
    audit_genesis_invariants(errors)
    if len(errors) == before_inv:
        print("  ✓ Zero external Go dependencies, Web UI zero-egress, and loopback/DELETE-burn/SSE-deadline/no-CORS invariants verified")

    # 7. Competitive landscape & ALTERNATIVES.md freshness gate
    before_landscape = len(errors)
    audit_landscape_freshness(errors)
    if len(errors) == before_landscape:
        print("  ✓ Competitive landscape (docs/ALTERNATIVES.md) metadata & 180-day freshness verified")

    if errors:
        print(f"\n[FAIL] {len(errors)} invariant violation(s) detected:", file=sys.stderr)
        for err in errors:
            print(f"  ✗ {err}", file=sys.stderr)
        return 1

    print("\nAll Genesis empirical claims, registry schemas, and governance gates verified successfully.\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())

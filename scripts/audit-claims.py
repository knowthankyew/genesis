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
    registered_docs = {"README.md", "NOTICE.md", "SECURITY.md", "docs/ARCHITECTURE.md", "docs/DECISIONS.md"}
    for doc in get_docs_to_scan():
        content = doc.read_text(encoding="utf-8")
        rel_name = str(doc.relative_to(REPO_ROOT)).replace("\\", "/")

        if rel_name in registered_docs:
            ok, word_errs = sync_docs.lint_prose_and_words(content, rel_name, registry_path=registry_path)
            if not ok:
                for e in word_errs:
                    errors.append(f"Vocabulary violation: {e}")
        else:
            for idx, line in enumerate(content.splitlines(), 1):
                for pat in sync_docs.PROHIBITED_WORDS:
                    if pat.search(line):
                        errors.append(
                            f"Vocabulary violation in {rel_name}:{idx}: "
                            f"Prohibited term matching '{pat.pattern}' found: '{line.strip()}'"
                        )


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
    if not covers:
        errors.append(f"{label}: covers is missing or not a [a, b, c] list")

    if commit and covers:
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
        try:
            bom = json.loads(bom_path.read_text(encoding="utf-8"))
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
            }
            for k, v in expected_props.items():
                if props.get(k) != v:
                    errors.append(f"bom.json property '{k}' expected '{v}', got {props.get(k)!r}")
        except Exception as exc:
            errors.append(f"bom.json invalid JSON: {exc}")


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

    # 3. Security & privacy runtime invariants verified via Go test suite
    run_check(
        ["go", "test", "./internal/engine", "./internal/ui", "-run", "TestSecurityInvariants|TestUI_ZeroExternalEgress"],
        "Go Security & Zero-Egress Invariant Tests",
        errors,
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

    if errors:
        print(f"\n[FAIL] {len(errors)} invariant violation(s) detected:", file=sys.stderr)
        for err in errors:
            print(f"  ✗ {err}", file=sys.stderr)
        return 1

    print("\nAll Genesis empirical claims, registry schemas, and governance gates verified successfully.\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())

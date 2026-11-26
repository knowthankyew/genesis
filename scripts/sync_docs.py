#!/usr/bin/env python3
"""
sync_docs.py: Claims Registry Documentation Synchronizer & Linter v2 for Genesis.

Enforces:
1. Documentation claim markers `<!-- claim:<id> -->...<!-- /claim -->` strictly match
   empirical artifacts and Go source constants registered in docs/claims_registry.json.
2. Inside regions marked as empirical (`<!-- BEGIN_EMPIRICAL -->` ... `<!-- END_EMPIRICAL -->`),
   any unmanaged digit in metric cells outside a claim marker fails the check, and every
   table row status cell must be governed by a registered status claim marker.
3. Word & personal-path lint: adjectives such as 'bulletproof', 'rock solid', 'conclusive',
   'definitive', and 'infinitely scalable' are prohibited (no bypass phrases). Guarded terms
   ('verified', 'authentic', 'proves', 'zero hallucination', '100%') are allowed only when tied
   to a registered claim with verified file/test evidence. Personal host paths (/Users/, /home/,
   Windows C:\\Users\\) are forbidden in docs and JSON artifacts.
4. Independent mathematical verification of docs/benchmarks/empirical_benchmarks.json against
   the referenced docs/benchmarks/<date>-<sha>[-rN].md report (strict JSON without duplicate keys
   or non-finite floats, clean git_commit ancestry, throughput recomputation, latency ordering,
   status threshold derivation, dropped sum, heap bounds, RSS check, and CommonMark-aware
   published GFM table & telemetry parity).
5. Cross-document marker integrity and registry referential hygiene (no orphaned/duplicate
   used_in entries, repo-confined paths, and verified claim_term evidence on disk).
6. Safe extraction and rendering of Go source constants (MaxSegmentBytes, DefaultMaxLineLength,
   MaxServicePoolSize, DefaultMaxMemoryBytes, MaxQueryLimit, GoRuntimeMemoryLimitMB).

Modes:
  --check (DEFAULT): Validates documentation and artifacts; exits 0 on match, 1 on failure.
  --write (EXPLICIT): Renders resolved claim values into documentation markers.
"""

import argparse
import ast
import json
import math
import operator
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_REGISTRY_PATH = REPO_ROOT / "docs" / "claims_registry.json"
DEFAULT_README_PATH = REPO_ROOT / "README.md"

EMPIRICAL_BLOCK_DELIMITERS = [
    ("<!-- BEGIN_EMPIRICAL -->", "<!-- END_EMPIRICAL -->"),
]

_PERSONAL_HOST_PATH_RE = re.compile(r"(?:/Users/|/home/|[A-Za-z]:(?:\\|/)[Uu]sers(?:\\|/))")

PROHIBITED_WORDS = [
    re.compile(r"\bbullet-?proof\b", re.IGNORECASE),
    re.compile(r"\brock\s+solid\b", re.IGNORECASE),
    re.compile(r"\bconclusive\b", re.IGNORECASE),
    re.compile(r"\bdefinitive\b", re.IGNORECASE),
    re.compile(r"\binfinitely\s+scalable\b", re.IGNORECASE),
]

GUARDED_WORDS = [
    re.compile(r"\bverified\b", re.IGNORECASE),
    re.compile(r"\bauthentic\b", re.IGNORECASE),
    re.compile(r"\bproves\b", re.IGNORECASE),
    re.compile(r"\bzero\s+hallucination\b", re.IGNORECASE),
    re.compile(r"100%"),
]

_BINARY_OPS = {
    ast.Add: operator.add,
    ast.Sub: operator.sub,
    ast.Mult: operator.mul,
    ast.Div: operator.truediv,
    ast.FloorDiv: operator.floordiv,
    ast.LShift: operator.lshift,
    ast.RShift: operator.rshift,
}

MAX_SAFE_INT64 = (1 << 63) - 1


def _reject_nonfinite_json_constant(const: str) -> None:
    raise ValueError(f"Non-standard non-finite JSON constant not permitted: {const!r}")


def _parse_finite_json_float(raw: str) -> float:
    val = float(raw)
    if not math.isfinite(val):
        raise ValueError(f"Non-finite JSON floating-point value not permitted: {raw!r}")
    return val


def _reject_duplicate_json_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    out: dict[str, object] = {}
    for k, v in pairs:
        if k in out:
            raise ValueError(f"Duplicate JSON object member not permitted: {k!r}")
        out[k] = v
    return out


def _loads_strict_json(raw: str):
    return json.loads(
        raw,
        parse_float=_parse_finite_json_float,
        parse_constant=_reject_nonfinite_json_constant,
        object_pairs_hook=_reject_duplicate_json_keys,
    )


def _round_go(val: float, digits: int) -> float:
    """Match Go's math.Round(v * 10^digits) / 10^digits (IEEE 754 half away from zero)."""
    scale = 10 ** digits
    scaled = val * scale
    frac, whole = math.modf(abs(scaled))
    rounded = whole + (1.0 if frac >= 0.5 else 0.0)
    return math.copysign(rounded / scale, scaled)


def _has_personal_host_path_text(text: str) -> bool:
    """Detect leaked macOS (/Users/), Linux (/home/), or Windows (C:\\Users\\) personal paths."""
    return _PERSONAL_HOST_PATH_RE.search(text) is not None


def _contains_personal_host_path(obj) -> bool:
    """Recursively check decoded JSON strings for personal host prefixes (catching JSON-escaped slashes)."""
    if isinstance(obj, str):
        return _has_personal_host_path_text(obj)
    if isinstance(obj, dict):
        return any(_contains_personal_host_path(k) or _contains_personal_host_path(v) for k, v in obj.items())
    if isinstance(obj, list):
        return any(_contains_personal_host_path(v) for v in obj)
    return False


def _verify_git_commit_ancestry(commit: object, repo_root: Path, label: str, errors: list[str]) -> bool:
    """Verify git_commit metadata is a clean 7-40 hex hash (never 'unknown' or '-dirty') and an ancestor of HEAD."""
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-fA-F]{7,40}", commit):
        errors.append(
            f"{label}: git_commit {commit!r} must be a clean 7-40 hex git commit hash (dirty/unknown builds not permitted)"
        )
        return False
    if (repo_root / ".git").exists():
        try:
            rev = subprocess.run(
                ["git", "rev-parse", "--verify", "--quiet", f"{commit}^{{commit}}"],
                cwd=str(repo_root),
                capture_output=True,
                text=True,
            )
            if rev.returncode != 0:
                errors.append(f"{label}: git_commit '{commit}' not found in repository git history")
                return False
            anc = subprocess.run(
                ["git", "merge-base", "--is-ancestor", commit, "HEAD"],
                cwd=str(repo_root),
                capture_output=True,
                text=True,
            )
            if anc.returncode != 0:
                errors.append(f"{label}: git_commit '{commit}' is not an ancestor of HEAD")
                return False
        except FileNotFoundError as exc:
            errors.append(f"{label}: git executable not found while verifying commit ancestry ({exc})")
            return False
    return True


def _is_int(v) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def _is_finite_num(v) -> bool:
    if not isinstance(v, (int, float)) or isinstance(v, bool):
        return False
    try:
        return math.isfinite(float(v))
    except (OverflowError, ValueError, TypeError):
        return False


def _req_dict(parent: dict, key: str, errors: list[str], prefix: str) -> dict:
    val = parent.get(key)
    if not isinstance(val, dict):
        errors.append(f"{prefix}.{key} must be a JSON object, got {type(val).__name__}")
        return {}
    return val


def _req_int(
    section: dict,
    key: str,
    errors: list[str],
    prefix: str,
    min_val: int | None = None,
    max_val: int = MAX_SAFE_INT64,
) -> int | None:
    val = section.get(key)
    if not _is_int(val):
        errors.append(f"{prefix}.{key} must be an integer, got {val!r}")
        return None
    if min_val is not None and val < min_val:
        errors.append(f"{prefix}.{key} must be >= {min_val}, got {val}")
        return None
    if val > max_val:
        errors.append(f"{prefix}.{key} exceeds maximum safe int64 bound ({max_val}), got {val}")
        return None
    return val


def _req_num(section: dict, key: str, errors: list[str], prefix: str) -> float | None:
    val = section.get(key)
    if not _is_finite_num(val):
        errors.append(f"{prefix}.{key} must be a finite number, got {val!r}")
        return None
    return float(val)


def _eval_numeric_expr(expr_str: str, const_name: str) -> int | float:
    """Safely evaluate a constant numeric Go expression (e.g. '16 * 1024 * 1024') via AST."""
    tree = ast.parse(expr_str.strip(), mode="eval")

    def _eval(node: ast.AST) -> int | float:
        if isinstance(node, ast.Expression):
            return _eval(node.body)
        if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)) and not isinstance(node.value, bool):
            return node.value
        if isinstance(node, ast.BinOp) and type(node.op) in _BINARY_OPS:
            return _BINARY_OPS[type(node.op)](_eval(node.left), _eval(node.right))
        raise ValueError(f"Unsupported expression in Go constant {const_name}: {expr_str!r}")

    return _eval(tree)


def extract_go_constant(file_path: Path, const_name: str) -> int | float | str:
    """Extract a package-level Go constant from a .go source file without executing code."""
    if not file_path.is_file():
        raise FileNotFoundError(f"Go source file not found: {file_path}")
    text = file_path.read_text(encoding="utf-8")
    pattern = re.compile(
        rf"^\s*(?:const\s+)?{re.escape(const_name)}(?:\s+[A-Za-z0-9_]+)?\s*=\s*([^\r\n]+)",
        re.MULTILINE,
    )
    m = pattern.search(text)
    if not m:
        raise ValueError(f"Go constant {const_name!r} not found in {file_path}")
    raw_expr = m.group(1).strip()
    if raw_expr.startswith('"'):
        str_match = re.match(r'^"((?:[^"\\]|\\.)*)"(?:\s*//.*)?$', raw_expr)
        if not str_match:
            raise ValueError(f"Unsupported string expression in Go constant {const_name}: {raw_expr!r}")
        return str_match.group(1)
    raw_expr = re.sub(r"//.*$", "", raw_expr).strip()
    return _eval_numeric_expr(raw_expr, const_name)


NAMED_FORMAT_RULES = {
    "bytes_to_mb": lambda v: f"{int(v) // (1024 * 1024)} MB",
    "bytes_to_kb": lambda v: f"{int(v) // 1024} KB",
}


def apply_format_rule(claim_id: str, fmt: str, value) -> str:
    """Render a resolved value using named format rules or str.format."""
    if fmt in NAMED_FORMAT_RULES:
        try:
            return NAMED_FORMAT_RULES[fmt](value)
        except Exception as exc:
            raise ValueError(f"Claim '{claim_id}': named format_rule {fmt!r} failed on {value!r}: {exc}") from exc
    if isinstance(value, (list, tuple, dict)):
        raise ValueError(f"Claim '{claim_id}': format_rule {fmt!r} cannot render {type(value).__name__}")
    try:
        return fmt.format(value=value)
    except Exception as exc:
        raise ValueError(f"Claim '{claim_id}': invalid format_rule {fmt!r}: {exc}") from exc


def resolve_claim_value(
    claim_id: str,
    claim_cfg: dict,
    repo_root: Path = REPO_ROOT,
    validated_json_cache: dict[Path, dict] | None = None,
) -> str:
    """Resolve and format a registered claim value from its empirical JSON or Go source artifact."""
    artifact_rel = claim_cfg.get("source_artifact")
    if not isinstance(artifact_rel, str) or not artifact_rel.strip():
        raise ValueError(f"Claim '{claim_id}' missing 'source_artifact'")
    if Path(artifact_rel).is_absolute():
        raise ValueError(f"Claim '{claim_id}' source_artifact must be repository-relative: {artifact_rel!r}")
    artifact_path = repo_root / artifact_rel
    try:
        resolved_artifact = artifact_path.resolve()
        resolved_root = repo_root.resolve()
    except (ValueError, OSError) as exc:
        raise ValueError(f"Claim '{claim_id}' has invalid source_artifact {artifact_rel!r}: {exc}") from exc
    try:
        resolved_artifact.relative_to(resolved_root)
    except ValueError as exc:
        raise ValueError(f"Claim '{claim_id}' source_artifact escapes repository root: {artifact_rel!r}") from exc

    fmt = claim_cfg.get("format_rule", "{value}")

    if "go_const" in claim_cfg:
        if not artifact_path.is_file():
            raise FileNotFoundError(f"Source artifact not found for claim '{claim_id}': {artifact_path}")
        val = extract_go_constant(artifact_path, claim_cfg["go_const"])
        return apply_format_rule(claim_id, fmt, val)

    if "json_path" in claim_cfg:
        if validated_json_cache is not None and resolved_artifact in validated_json_cache:
            data = validated_json_cache[resolved_artifact]
        else:
            if not artifact_path.is_file():
                raise FileNotFoundError(f"Source artifact not found for claim '{claim_id}': {artifact_path}")
            data = _loads_strict_json(artifact_path.read_text(encoding="utf-8"))
        cur = data
        for part in claim_cfg["json_path"].split("."):
            if isinstance(cur, dict) and part in cur:
                cur = cur[part]
            elif isinstance(cur, list) and part.isdigit() and int(part) < len(cur):
                cur = cur[int(part)]
            else:
                raise KeyError(f"Path '{claim_cfg['json_path']}' part '{part}' not found in {artifact_path.name}")
        return apply_format_rule(claim_id, fmt, cur)

    raise ValueError(f"Claim '{claim_id}' has neither 'go_const' nor 'json_path'")


_HTML_TYPE1_OPEN_RE = re.compile(r"^ {0,3}<(pre|script|style|textarea)(?:[\s>]|$)", re.IGNORECASE)
_HTML_TYPE3_OPEN_RE = re.compile(r"^ {0,3}<\?")
_HTML_TYPE5_OPEN_RE = re.compile(r"^ {0,3}<!\[CDATA\[")
_HTML_TYPE6_OPEN_RE = re.compile(
    r"^ {0,3}</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|"
    r"dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|"
    r"head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|"
    r"p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:[\s/>]|$)",
    re.IGNORECASE,
)


def _parse_gfm_row(
    raw_line: str,
    require_outer_pipes: bool = False,
    min_cells: int = 2,
) -> tuple[str, list[str]] | None:
    """Parse a GFM table row outside 4-space/tab indented code blocks (optional outer pipes)."""
    if raw_line.startswith("    ") or raw_line.startswith("\t"):
        return None
    line = raw_line.strip()
    if not line or "|" not in line:
        return None
    if require_outer_pipes and not (line.startswith("|") and line.endswith("|") and len(line) >= 3):
        return None
    inner = line
    if inner.startswith("|"):
        inner = inner[1:]
    if inner.endswith("|") and not inner.endswith(r"\|"):
        inner = inner[:-1]
    cells = [c.strip() for c in re.split(r"(?<!\\)\|", inner)]
    if len(cells) < min_cells:
        return None
    return line, cells


def _is_gfm_delimiter_row(cells: list[str]) -> bool:
    return len(cells) >= 2 and all(re.fullmatch(r":?-{1,}:?", c) is not None for c in cells)


def _strip_markdown_non_rendered(md_text: str) -> list[str]:
    """
    Strip CommonMark fenced code blocks (backticks and tildes >= 3), 4-space/tab indented code
    blocks (before scanning for HTML comments), raw HTML blocks (Types 1, 3, 5, and 6), and
    HTML comments (including unclosed '<!--' through EOF), preserving 1-based line positions.
    """
    cleaned_lines: list[str] = []
    fence_char: str | None = None
    fence_len = 0
    in_comment = False
    html_type1_end: re.Pattern[str] | None = None
    in_html_type3 = False
    in_html_type5 = False
    in_html_type6 = False
    can_start_indented_code = True

    for raw_line in md_text.splitlines():
        if fence_char is not None:
            close_m = re.match(r"^ {0,3}(" + re.escape(fence_char) + r"{3,})[ \t]*$", raw_line)
            if close_m and len(close_m.group(1)) >= fence_len:
                fence_char = None
                fence_len = 0
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if html_type1_end is not None:
            if html_type1_end.search(raw_line):
                html_type1_end = None
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if in_html_type3:
            if "?>" in raw_line:
                in_html_type3 = False
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if in_html_type5:
            if "]]>" in raw_line:
                in_html_type5 = False
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if in_html_type6:
            if raw_line.strip() == "":
                in_html_type6 = False
                can_start_indented_code = True
            cleaned_lines.append("")
            continue

        line = raw_line
        if in_comment:
            end_idx = line.find("-->")
            if end_idx == -1:
                cleaned_lines.append("")
                continue
            in_comment = False
            line = line[end_idx + 3 :]
            if line.strip() == "":
                cleaned_lines.append("")
                can_start_indented_code = True
                continue

        if line.strip() == "":
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if can_start_indented_code and (line.startswith("    ") or line.startswith("\t")):
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        open_m = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", line)
        if open_m:
            marker, info = open_m.group(1), open_m.group(2)
            if marker[0] == "~" or "`" not in info:
                fence_char = marker[0]
                fence_len = len(marker)
                cleaned_lines.append("")
                can_start_indented_code = True
                continue

        m1 = _HTML_TYPE1_OPEN_RE.match(line)
        if m1:
            end_re = re.compile(rf"</{m1.group(1)}>", re.IGNORECASE)
            if not end_re.search(line):
                html_type1_end = end_re
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if _HTML_TYPE3_OPEN_RE.match(line):
            if "?>" not in line:
                in_html_type3 = True
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if _HTML_TYPE5_OPEN_RE.match(line):
            if "]]>" not in line:
                in_html_type5 = True
            cleaned_lines.append("")
            can_start_indented_code = True
            continue

        if _HTML_TYPE6_OPEN_RE.match(line):
            in_html_type6 = True
            cleaned_lines.append("")
            can_start_indented_code = False
            continue

        out_parts: list[str] = []
        pos = 0
        while True:
            start_idx = line.find("<!--", pos)
            if start_idx == -1:
                out_parts.append(line[pos:])
                break
            out_parts.append(line[pos:start_idx])
            end_idx = line.find("-->", start_idx + 4)
            if end_idx == -1:
                in_comment = True
                break
            pos = end_idx + 3
        cleaned = "".join(out_parts)
        cleaned_lines.append(cleaned)
        can_start_indented_code = cleaned.strip() == ""

    return cleaned_lines


def _extract_published_markdown_table_rows(
    md_text: str,
) -> tuple[dict[str, list[str]], set[str], list[str], str]:
    """
    Collect rendered GFM table body rows (keyed by col 0 label -> rendered cells),
    bold row labels, duplicate bold labels, and rendered non-commented Markdown text.
    """
    cleaned_lines = _strip_markdown_non_rendered(md_text)
    rows_by_label: dict[str, list[str]] = {}
    seen_labels: set[str] = set()
    dup_labels: list[str] = []
    i = 0
    total_lines = len(cleaned_lines)
    while i < total_lines:
        hdr = _parse_gfm_row(cleaned_lines[i], require_outer_pipes=False)
        if hdr is not None and i + 1 < total_lines:
            delim = _parse_gfm_row(cleaned_lines[i + 1], require_outer_pipes=False)
            if delim is not None and len(delim[1]) == len(hdr[1]) and _is_gfm_delimiter_row(delim[1]):
                header_cols = len(hdr[1])
                i += 2
                while i < total_lines:
                    body = _parse_gfm_row(cleaned_lines[i], require_outer_pipes=False, min_cells=1)
                    if body is None:
                        break
                    _, parts = body
                    rendered_parts = parts[:header_cols] if len(parts) > header_cols else parts
                    if rendered_parts[0].startswith("**") and rendered_parts[0].endswith("**"):
                        lbl = rendered_parts[0]
                        if lbl in seen_labels:
                            dup_labels.append(lbl)
                        else:
                            seen_labels.add(lbl)
                            rows_by_label[lbl] = rendered_parts
                    i += 1
                continue
        i += 1
    return rows_by_label, seen_labels, dup_labels, "\n".join(cleaned_lines)


def independent_recompute_metrics(
    benchmark_json_path: Path,
    repo_root: Path = REPO_ROOT,
    validated_json_cache: dict[Path, dict] | None = None,
) -> tuple[bool, list[str]]:
    """
    Independently verify mathematical consistency of docs/benchmarks/empirical_benchmarks.json,
    strict schema & finite-number bounds, clean git_commit ancestry, status gate derivations
    from recomputed metrics, and CommonMark-aware published-table parity against the committed
    markdown benchmark report.
    """
    errors: list[str] = []
    if not benchmark_json_path.is_file():
        return False, [f"Missing empirical benchmark JSON: {benchmark_json_path}"]

    try:
        raw_json = benchmark_json_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        return False, [f"Failed to read {benchmark_json_path}: {exc}"]
    if _has_personal_host_path_text(raw_json):
        errors.append("Empirical benchmark JSON contains leaked personal host path")

    try:
        data = _loads_strict_json(raw_json)
    except Exception as exc:
        return False, [f"Failed to parse {benchmark_json_path}: {exc}"]

    if not isinstance(data, dict):
        return False, [f"Empirical benchmark JSON root must be an object, got {type(data).__name__}"]
    if _contains_personal_host_path(data):
        errors.append("Empirical benchmark JSON contains leaked personal host path in decoded strings")

    meta = _req_dict(data, "metadata", errors, "benchmark")
    gates = _req_dict(data, "acceptance_gates", errors, "benchmark")
    ingest = _req_dict(data, "ingestion_pipeline", errors, "benchmark")
    lat = _req_dict(data, "query_latencies", errors, "benchmark")
    mem = _req_dict(data, "memory_profile", errors, "benchmark")
    ver = _req_dict(data, "verification_runs", errors, "benchmark")
    if errors:
        return False, errors

    # 0. Verify clean git_commit provenance
    git_commit = meta.get("git_commit")
    commit_valid = _verify_git_commit_ancestry(git_commit, repo_root, "Empirical benchmark metadata", errors)

    # 1. Recompute throughput from lines_indexed / duration_seconds
    tps_target = _req_num(gates, "throughput_target_lps", errors, "acceptance_gates")
    lines_sent = _req_int(ingest, "lines_sent", errors, "ingestion_pipeline", min_val=1)
    lines_indexed = _req_int(ingest, "lines_indexed", errors, "ingestion_pipeline", min_val=1)
    duration_sec = _req_num(ingest, "duration_seconds", errors, "ingestion_pipeline")
    stored_tps = _req_num(ingest, "throughput_lps", errors, "ingestion_pipeline")

    calc_tps: float | None = None
    if lines_indexed is not None and duration_sec is not None and stored_tps is not None:
        if duration_sec <= 0:
            errors.append("ingestion_pipeline.duration_seconds must be positive")
        else:
            calc_tps = _round_go(lines_indexed / duration_sec, 2)
            if f"{stored_tps:.2f}" != f"{calc_tps:.2f}":
                errors.append(
                    f"Throughput recomputation mismatch: stored {stored_tps:.2f}, "
                    f"calculated {calc_tps:.2f} ({lines_indexed} / {duration_sec})"
                )
            if tps_target is not None:
                exp_tps_status = "PASSED" if calc_tps >= tps_target else "MISSED TARGET"
                if ingest.get("throughput_gate_status") != exp_tps_status:
                    errors.append(
                        f"throughput_gate_status mismatch: expected {exp_tps_status!r} "
                        f"for recomputed throughput {calc_tps:.2f} vs target {tps_target:.2f}"
                    )

    # 2. Verify dropped breakdown sum
    dropped_total = _req_int(ingest, "dropped_total", errors, "ingestion_pipeline", min_val=0)
    dropped_q = _req_int(ingest, "dropped_queue_full", errors, "ingestion_pipeline", min_val=0)
    dropped_m = _req_int(ingest, "dropped_malformed", errors, "ingestion_pipeline", min_val=0)
    dropped_s = _req_int(ingest, "dropped_stopped", errors, "ingestion_pipeline", min_val=0)
    if None not in (dropped_total, dropped_q, dropped_m, dropped_s):
        dropped_sum = dropped_q + dropped_m + dropped_s
        if dropped_total != dropped_sum:
            errors.append(f"Dropped count mismatch: dropped_total={dropped_total}, sum of categories={dropped_sum}")

    # 3. Verify latency percentile monotonicity and status labels
    p50_us = _req_num(lat, "p50_us", errors, "query_latencies")
    p90_ms = _req_num(lat, "p90_ms", errors, "query_latencies")
    p99_ms = _req_num(lat, "p99_ms", errors, "query_latencies")
    max_ms = _req_num(lat, "max_ms", errors, "query_latencies")
    p99_target = _req_num(gates, "p99_latency_target_ms", errors, "acceptance_gates")

    if None not in (p50_us, p90_ms, p99_ms, max_ms):
        p50_ms = p50_us / 1000.0
        if not (0 < p50_ms <= p90_ms <= p99_ms <= max_ms):
            errors.append(
                f"Query latency percentile monotonicity violated: "
                f"p50={p50_ms:.4f}ms, p90={p90_ms:.4f}ms, p99={p99_ms:.4f}ms, max={max_ms:.4f}ms"
            )

        exp_p50_status = "SUB-MILLISECOND" if p50_us < 1000.0 else "ABOVE-1MS"
        if lat.get("p50_gate_status") != exp_p50_status:
            errors.append(f"p50_gate_status mismatch: expected {exp_p50_status!r} for p50_us={p50_us}")

        exp_p90_status = "SUB-TWENTY-MS" if p90_ms < 20.0 else "ABOVE-20MS"
        if lat.get("p90_gate_status") != exp_p90_status:
            errors.append(f"p90_gate_status mismatch: expected {exp_p90_status!r} for p90_ms={p90_ms}")

        if p99_target is not None:
            exp_p99_status = "MISSED TARGET" if p99_ms >= p99_target else "PASSED"
            if lat.get("p99_gate_status") != exp_p99_status:
                errors.append(
                    f"p99_gate_status mismatch: expected {exp_p99_status!r} for p99={p99_ms}ms vs target={p99_target}ms"
                )

    # 4. Verify memory bounds (accounted <= plateau <= peak <= ceiling) and RSS
    accounted_mb = _req_num(mem, "accounted_mb", errors, "memory_profile")
    plateau_mb = _req_num(mem, "heap_alloc_plateau_mb", errors, "memory_profile")
    peak_mb = _req_num(mem, "heap_alloc_peak_mb", errors, "memory_profile")
    ceiling_mb = _req_num(gates, "memory_ceiling_mb", errors, "acceptance_gates")
    rss_kb = _req_int(mem, "rss_10k_connections_kb", errors, "memory_profile", min_val=1)
    conns_held = _req_int(mem, "idle_connections_held", errors, "memory_profile", min_val=0)
    conns_target = _req_int(gates, "idle_tcp_connections_target", errors, "acceptance_gates", min_val=1)

    if None not in (accounted_mb, plateau_mb, peak_mb, ceiling_mb):
        if not (0 < accounted_mb <= plateau_mb <= peak_mb <= ceiling_mb):
            errors.append(
                f"Memory bound invariant violated: accounted={accounted_mb}MB, "
                f"plateau={plateau_mb}MB, peak={peak_mb}MB, ceiling={ceiling_mb}MB"
            )
        exp_heap_status = "PASSED" if plateau_mb <= ceiling_mb and peak_mb <= ceiling_mb else "MISSED TARGET"
        if mem.get("heap_gate_status") != exp_heap_status:
            errors.append(f"heap_gate_status mismatch: expected {exp_heap_status!r}")

    if conns_held is not None and conns_target is not None:
        exp_conns_status = "PASSED" if conns_held >= conns_target else "MISSED TARGET"
        if mem.get("idle_connections_gate_status") != exp_conns_status:
            errors.append(f"idle_connections_gate_status mismatch: expected {exp_conns_status!r}")

    # 5. Verify verification_runs section
    race_target = _req_int(gates, "race_detector_errors_target", errors, "acceptance_gates", min_val=0)
    race_measured = _req_int(ver, "race_detector_errors_measured", errors, "verification_runs", min_val=0)
    if race_target is not None and race_measured is not None:
        exp_race_status = "PASSED" if race_measured == race_target else "FAILED"
        if ver.get("race_gate_status") != exp_race_status:
            errors.append(f"race_gate_status mismatch: expected {exp_race_status!r}")

    oracle_target = _req_int(gates, "oracle_randomized_queries_target", errors, "acceptance_gates", min_val=1)
    oracle_exec = _req_int(ver, "oracle_queries_executed", errors, "verification_runs", min_val=0)
    oracle_pct = _req_int(ver, "oracle_exact_match_pct_measured", errors, "verification_runs", min_val=0, max_val=100)
    if None not in (oracle_target, oracle_exec, oracle_pct):
        exp_oracle_status = "PASSED" if oracle_exec >= oracle_target and oracle_pct == 100 else "FAILED"
        if ver.get("oracle_gate_status") != exp_oracle_status:
            errors.append(f"oracle_gate_status mismatch: expected {exp_oracle_status!r}")

    # 6. Cross-verify against repo-confined Markdown benchmark report (published GFM table + rendered text)
    report_rel = meta.get("report_markdown", "")
    report_path: Path | None = None
    if not isinstance(report_rel, str) or not report_rel.strip():
        errors.append(f"Referenced markdown benchmark report path is missing or invalid: {report_rel!r}")
    elif Path(report_rel).is_absolute():
        errors.append(f"Referenced markdown benchmark report must be repository-relative, got absolute path: {report_rel!r}")
    else:
        try:
            cand = (repo_root / report_rel).resolve()
            resolved_root = repo_root.resolve()
        except (ValueError, OSError) as exc:
            errors.append(f"Referenced markdown benchmark report path is invalid: {report_rel!r} ({exc})")
        else:
            try:
                cand.relative_to(resolved_root)
            except ValueError:
                errors.append(f"Referenced markdown benchmark report escapes repository root: {report_rel!r}")
            else:
                if not cand.is_file():
                    errors.append(f"Referenced markdown benchmark report not found: {report_rel!r}")
                else:
                    report_path = cand
                    if commit_valid and isinstance(git_commit, str):
                        if not re.fullmatch(rf"\d{{4}}-\d{{2}}-\d{{2}}-{re.escape(git_commit)}(?:-r[2-9]\d*)?\.md", cand.name):
                            errors.append(
                                f"Benchmark report_markdown filename {cand.name!r} must match "
                                f"'YYYY-MM-DD-{git_commit}[-rN].md' for metadata.git_commit={git_commit!r}"
                            )

    if report_path is not None and not errors:
        try:
            md_text = report_path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            return False, [f"Failed to read markdown benchmark report {report_rel}: {exc}"]
        if _has_personal_host_path_text(md_text):
            errors.append(f"Markdown benchmark report {report_rel} contains leaked personal host path")

        def _go_dur_us(us: float) -> str:
            return f"{us:.3f}".rstrip("0").rstrip(".") + "µs"

        def _go_dur_ms(ms: float) -> str:
            if ms < 1.0:
                return _go_dur_us(ms * 1000.0)
            return f"{ms:.6f}".rstrip("0").rstrip(".") + "ms"

        rows_by_label, _, dup_labels, rendered_md = _extract_published_markdown_table_rows(md_text)
        if dup_labels:
            errors.append(f"Markdown benchmark report {report_rel} contains duplicate metric row labels: {dup_labels}")

        expected_table_cells = [
            ("**Throughput (Lines Indexed)**", f"{calc_tps:.2f} lines/sec"),
            ("**Read Query Latency (P99)**", _go_dur_ms(p99_ms)),
            ("**Memory Plateau**", f"{plateau_mb:.2f} MB"),
            ("**Memory Plateau**", f"{peak_mb:.2f} MB"),
            ("**Idle TCP Connections**", f"{conns_held} held"),
        ]
        for row_label, expected_sub in expected_table_cells:
            cells = rows_by_label.get(row_label)
            if cells is None or len(cells) < 3 or expected_sub not in cells[2]:
                errors.append(
                    f"Benchmark JSON/Markdown published-table parity error: expected {expected_sub!r} "
                    f"in published GFM row {row_label!r} of {report_rel}"
                )

        expected_rendered_tokens = [
            _go_dur_us(p50_us),
            _go_dur_ms(p90_ms),
            _go_dur_ms(p99_ms),
            f"{rss_kb} KB",
            str(lines_indexed),
            str(lines_sent),
            str(dropped_total),
        ]
        for tok in expected_rendered_tokens:
            if tok not in rendered_md:
                errors.append(f"Benchmark JSON/Markdown parity error: rendered token {tok!r} missing from {report_rel}")

    if not errors and validated_json_cache is not None:
        validated_json_cache[benchmark_json_path.resolve()] = data
    return len(errors) == 0, errors


def _verify_claim_term_evidence(term_id: str, term_cfg: dict, repo_root: Path) -> list[str]:
    """Verify that a claim_term entry declares non-empty evidence files/symbols that exist in repo_root."""
    errs: list[str] = []
    evidence_list = term_cfg.get("evidence", [])
    if not isinstance(evidence_list, list) or not evidence_list:
        return [f"claim_term '{term_id}' is missing required non-empty 'evidence' list in claims_registry.json"]

    resolved_root = repo_root.resolve()
    for item in evidence_list:
        file_part, sep, symbol_part = str(item).partition(":")
        if not file_part or Path(file_part).is_absolute():
            errs.append(f"claim_term '{term_id}' evidence path must be repository-relative: {file_part!r}")
            continue
        try:
            target = (repo_root / file_part).resolve()
            target.relative_to(resolved_root)
        except (ValueError, OSError):
            errs.append(f"claim_term '{term_id}' evidence path escapes repository root or is invalid: {file_part!r}")
            continue
        if not target.is_file():
            errs.append(f"claim_term '{term_id}' evidence file does not exist: {file_part}")
            continue
        if sep and symbol_part:
            text = target.read_text(encoding="utf-8")
            if symbol_part not in text:
                errs.append(f"claim_term '{term_id}' evidence symbol {symbol_part!r} not found in {file_part}")
    return errs


def lint_prose_and_words(
    content: str,
    doc_name: str = "document",
    registry_path: Path | None = None,
    repo_root: Path = REPO_ROOT,
) -> tuple[bool, list[str]]:
    """
    Word & personal-path lint:
    - Prohibit 'bulletproof', 'rock solid', 'conclusive', 'definitive', 'infinitely scalable'.
    - Guarded words ('verified', 'authentic', 'proves', 'zero hallucination', '100%')
      allowed only when tied to a registered claim marker <!-- claim:... --> or <!-- claim_term:... -->
      whose evidence exists on disk.
    - Prohibit leaked personal host paths (/Users/, /home/, C:\\Users\\).
    """
    errors: list[str] = []

    for idx, line in enumerate(content.splitlines(), 1):
        if _has_personal_host_path_text(line):
            errors.append(f"Personal path leak in {doc_name}:{idx}: found personal host path.")
        for pat in PROHIBITED_WORDS:
            m = pat.search(line)
            if m:
                errors.append(
                    f"Word lint failure in {doc_name}:{idx}: prohibited adjective '{m.group(0)}' found."
                )

    if registry_path is None:
        registry_path = DEFAULT_REGISTRY_PATH
    claim_terms = {}
    if registry_path and registry_path.is_file():
        try:
            claim_terms = _loads_strict_json(registry_path.read_text(encoding="utf-8")).get("claim_terms", {})
        except Exception as exc:
            errors.append(f"Failed to load claim_terms from {registry_path.name}: {exc}")

    verified_term_ids: set[str] = set()
    claim_term_pat = re.compile(r"<!--\s*claim_term:([a-zA-Z0-9_\-]+)\s*-->(.*?)<!--\s*/claim_term\s*-->", re.DOTALL)
    for m in claim_term_pat.finditer(content):
        term_id = m.group(1)
        wrapped_text = m.group(2).strip()
        if term_id not in claim_terms:
            errors.append(f"Word lint failure in {doc_name}: unknown claim_term ID '{term_id}'.")
        else:
            expected_term = claim_terms[term_id].get("term", "")
            if wrapped_text.lower() != expected_term.lower():
                errors.append(
                    f"Word lint failure in {doc_name}: claim_term '{term_id}' wrapped text {wrapped_text!r} "
                    f"does not match registered term {expected_term!r}."
                )
            if term_id not in verified_term_ids:
                verified_term_ids.add(term_id)
                errors.extend(_verify_claim_term_evidence(term_id, claim_terms[term_id], repo_root))

    masked = re.sub(r"<!--\s*claim:[^>]+-->.*?<!--\s*/claim\s*-->", " ", content, flags=re.DOTALL)
    masked = re.sub(r"<!--\s*claim_term:[^>]+-->.*?<!--\s*/claim_term\s*-->", " ", masked, flags=re.DOTALL)

    for pat in GUARDED_WORDS:
        for match in pat.finditer(masked):
            line_no = masked[: match.start()].count("\n") + 1
            errors.append(
                f"Word lint failure in {doc_name} line {line_no}: guarded term '{match.group(0)}' "
                f"used in bare text without being tied to a registered claim marker."
            )

    return len(errors) == 0, errors


def _strip_fenced_code_blocks_preserve_lines(content: str) -> str:
    """Blank out CommonMark backtick/tilde fenced code blocks while preserving 1-based line numbers."""
    out: list[str] = []
    fence_char: str | None = None
    fence_len = 0
    for raw_line in content.splitlines():
        if fence_char is not None:
            close_m = re.match(r"^ {0,3}(" + re.escape(fence_char) + r"{3,})[ \t]*$", raw_line)
            if close_m and len(close_m.group(1)) >= fence_len:
                fence_char = None
                fence_len = 0
            out.append("")
            continue
        open_m = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", raw_line)
        if open_m and (open_m.group(1)[0] == "~" or "`" not in open_m.group(2)):
            fence_char = open_m.group(1)[0]
            fence_len = len(open_m.group(1))
            out.append("")
            continue
        out.append(raw_line)
    return "\n".join(out)


def lint_claim_markers_and_regions(
    content: str,
    doc_name: str = "document",
    registry_path: Path | None = None,
) -> tuple[bool, list[str]]:
    """
    Validate structural integrity of `<!-- claim:... -->`, `<!-- claim_term:... -->`, and
    `<!-- BEGIN_EMPIRICAL -->` markers across ANY scanned Markdown file.
    """
    errors: list[str] = []
    if registry_path is None:
        registry_path = DEFAULT_REGISTRY_PATH

    known_claims: set[str] = set()
    known_terms: set[str] = set()
    if registry_path and registry_path.is_file():
        try:
            reg = _loads_strict_json(registry_path.read_text(encoding="utf-8"))
            known_claims = set(reg.get("claims", {}).keys())
            known_terms = set(reg.get("claim_terms", {}).keys())
        except Exception as exc:
            errors.append(f"Failed to parse {registry_path.name}: {exc}")
            return False, errors

    stripped = _strip_fenced_code_blocks_preserve_lines(content)

    for begin_marker, end_marker in EMPIRICAL_BLOCK_DELIMITERS:
        b_count = stripped.count(begin_marker)
        e_count = stripped.count(end_marker)
        if b_count != e_count:
            errors.append(
                f"Marker lint failure in {doc_name}: unbalanced empirical delimiters "
                f"({b_count} '{begin_marker}' vs {e_count} '{end_marker}')"
            )

    pos = 0
    open_tag: tuple[str, str, int] | None = None
    tag_start_re = re.compile(r"<!--\s*(/?claim(?:_term)?)\b")
    full_open_re = re.compile(r"^<!--\s*(claim|claim_term):([a-zA-Z0-9_\-]+)\s*-->$")
    full_close_re = re.compile(r"^<!--\s*/(claim|claim_term)\s*-->$")

    while True:
        m = tag_start_re.search(stripped, pos)
        if not m:
            break
        start = m.start()
        line_no = stripped[:start].count("\n") + 1
        end = stripped.find("-->", start + 4)
        if end == -1:
            errors.append(f"Marker lint failure in {doc_name}:{line_no}: unclosed HTML claim marker comment")
            break
        raw_comment = stripped[start : end + 3]
        pos = end + 3

        mo = full_open_re.match(raw_comment)
        mc = full_close_re.match(raw_comment)
        if mo:
            kind, cid = mo.group(1), mo.group(2)
            if open_tag is not None:
                errors.append(
                    f"Marker lint failure in {doc_name}:{line_no}: nested or unclosed "
                    f"'{open_tag[0]}:{open_tag[1]}' (opened at line {open_tag[2]}) before '{kind}:{cid}'"
                )
            open_tag = (kind, cid, line_no)
            if kind == "claim" and known_claims and cid not in known_claims:
                errors.append(f"Marker lint failure in {doc_name}:{line_no}: unknown claim ID '{cid}'")
            elif kind == "claim_term" and known_terms and cid not in known_terms:
                errors.append(f"Marker lint failure in {doc_name}:{line_no}: unknown claim_term ID '{cid}'")
        elif mc:
            close_kind = mc.group(1)
            if open_tag is None:
                errors.append(
                    f"Marker lint failure in {doc_name}:{line_no}: orphan closing '<!-- /{close_kind} -->' without opening marker"
                )
            elif open_tag[0] != close_kind:
                errors.append(
                    f"Marker lint failure in {doc_name}:{line_no}: mismatched closing '<!-- /{close_kind} -->' "
                    f"for '{open_tag[0]}:{open_tag[1]}' opened at line {open_tag[2]}"
                )
                open_tag = None
            else:
                open_tag = None
        else:
            errors.append(f"Marker lint failure in {doc_name}:{line_no}: malformed claim marker {raw_comment!r}")

    if open_tag is not None:
        errors.append(
            f"Marker lint failure in {doc_name}:{open_tag[2]}: unclosed '<!-- {open_tag[0]}:{open_tag[1]} -->' (missing '<!-- /{open_tag[0]} -->')"
        )

    emp_ok, emp_errs = check_empirical_regions(stripped, doc_name)
    if not emp_ok:
        errors.extend(emp_errs)

    return len(errors) == 0, errors


def validate_claims_registry(
    registry_path: Path,
    repo_root: Path = REPO_ROOT,
) -> tuple[bool, list[str]]:
    """
    Validate structural & referential integrity of docs/claims_registry.json:
    - Strict JSON (no duplicate keys or non-finite numbers) and zero personal paths
    - Repo-confined existing source_artifacts (with strict JSON & path-leak check for JSON files)
    - Non-empty, deduplicated `used_in` lists pointing to existing repo-confined docs containing each marker
    - All `claim_terms` evidence files and symbols exist on disk
    """
    errors: list[str] = []
    if not registry_path.is_file():
        return False, [f"Registry not found: {registry_path}"]
    try:
        raw_reg = registry_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        return False, [f"Failed to read {registry_path}: {exc}"]
    if _has_personal_host_path_text(raw_reg):
        errors.append(f"Registry {registry_path.name} contains leaked personal host path")
    try:
        reg = _loads_strict_json(raw_reg)
    except Exception as exc:
        return False, [f"Failed to parse {registry_path.name}: {exc}"]
    if not isinstance(reg, dict):
        return False, [f"Registry {registry_path.name} root must be a JSON object"]
    if _contains_personal_host_path(reg):
        errors.append(f"Registry {registry_path.name} contains leaked personal host path in decoded strings")

    claims = reg.get("claims")
    if not isinstance(claims, dict) or not claims:
        return False, [f"Registry {registry_path.name} must define a non-empty 'claims' object"]

    resolved_root = repo_root.resolve()
    doc_cache: dict[Path, str] = {}
    json_checked: set[Path] = set()

    for cid, cfg in claims.items():
        if not isinstance(cfg, dict):
            errors.append(f"Claim '{cid}' config must be a JSON object")
            continue
        art_rel = cfg.get("source_artifact")
        if not isinstance(art_rel, str) or not art_rel.strip() or Path(art_rel).is_absolute():
            errors.append(f"Claim '{cid}' has missing or non-relative source_artifact: {art_rel!r}")
        else:
            try:
                art_path = (repo_root / art_rel).resolve()
            except (ValueError, OSError) as exc:
                errors.append(f"Claim '{cid}' has invalid source_artifact {art_rel!r}: {exc}")
                art_path = None
            if art_path is not None:
                try:
                    art_path.relative_to(resolved_root)
                except ValueError:
                    errors.append(f"Claim '{cid}' source_artifact escapes repository root: {art_rel!r}")
                else:
                    if not art_path.is_file():
                        errors.append(f"Claim '{cid}' required source_artifact does not exist: {art_rel}")
                    elif "json_path" in cfg and art_path not in json_checked:
                        json_checked.add(art_path)
                        try:
                            raw_art = art_path.read_text(encoding="utf-8")
                            if _has_personal_host_path_text(raw_art):
                                errors.append(f"Claim '{cid}' source_artifact {art_rel} contains leaked personal host path")
                            parsed_art = _loads_strict_json(raw_art)
                            if _contains_personal_host_path(parsed_art):
                                errors.append(f"Claim '{cid}' source_artifact {art_rel} contains leaked personal host path in decoded strings")
                        except Exception as exc:
                            errors.append(f"Claim '{cid}' source_artifact {art_rel} failed strict JSON parse: {exc}")

        used_in = cfg.get("used_in")
        if not isinstance(used_in, list) or not used_in:
            errors.append(f"Claim '{cid}' must declare a non-empty 'used_in' document list (orphaned claim)")
            continue
        seen_docs: set[str] = set()
        marker_re = re.compile(rf"<!--\s*claim:{re.escape(cid)}\s*-->.*?<!--\s*/claim\s*-->", re.DOTALL)
        for rel_doc in used_in:
            if not isinstance(rel_doc, str) or not rel_doc.strip() or Path(rel_doc).is_absolute():
                errors.append(f"Claim '{cid}' has invalid 'used_in' entry: {rel_doc!r}")
                continue
            norm_rel = rel_doc.replace("\\", "/")
            if norm_rel in seen_docs:
                errors.append(f"Claim '{cid}' has duplicate 'used_in' entry: {norm_rel}")
                continue
            seen_docs.add(norm_rel)
            try:
                doc_p = (repo_root / norm_rel).resolve()
            except (ValueError, OSError) as exc:
                errors.append(f"Claim '{cid}' has invalid 'used_in' path {norm_rel!r}: {exc}")
                continue
            try:
                doc_p.relative_to(resolved_root)
            except ValueError:
                errors.append(f"Claim '{cid}' 'used_in' path escapes repository root: {norm_rel}")
                continue
            if not doc_p.is_file():
                errors.append(f"Claim '{cid}' 'used_in' document does not exist: {norm_rel}")
                continue
            if doc_p not in doc_cache:
                doc_cache[doc_p] = doc_p.read_text(encoding="utf-8")
            if not marker_re.search(doc_cache[doc_p]):
                errors.append(f"Claim '{cid}' listed in 'used_in' for {norm_rel}, but marker is missing")

    claim_terms = reg.get("claim_terms", {})
    if isinstance(claim_terms, dict):
        for tid, tcfg in claim_terms.items():
            if isinstance(tcfg, dict):
                errors.extend(_verify_claim_term_evidence(tid, tcfg, repo_root))

    return len(errors) == 0, errors


def check_empirical_regions(content: str, doc_name: str = "document") -> tuple[bool, list[str]]:
    """
    Verify that inside regions marked as empirical:
    1. Any empirical metric digit outside a claim marker in Target/Result/Status cells fails.
    2. Every data row's Status cell (last column) contains a registered `<!-- claim:... -->` marker.
    """
    errors: list[str] = []
    claim_marker_re = re.compile(r"<!--\s*claim:[a-zA-Z0-9_\-]+\s*-->.*?<!--\s*/claim\s*-->", re.DOTALL)

    for begin_marker, end_marker in EMPIRICAL_BLOCK_DELIMITERS:
        pattern = re.escape(begin_marker) + r"(.*?)" + re.escape(end_marker)
        for match in re.finditer(pattern, content, re.DOTALL):
            block_text = match.group(1)
            line_offset = content[: match.start()].count("\n") + 1
            block_lines = block_text.splitlines()
            skip_indices: set[int] = set()
            for idx_line in range(len(block_lines) - 1):
                hdr_row = _parse_gfm_row(block_lines[idx_line], require_outer_pipes=False)
                delim_row = _parse_gfm_row(block_lines[idx_line + 1], require_outer_pipes=False)
                if (
                    hdr_row is not None
                    and delim_row is not None
                    and len(hdr_row[1]) == len(delim_row[1])
                    and _is_gfm_delimiter_row(delim_row[1])
                ):
                    skip_indices.add(idx_line)
                    skip_indices.add(idx_line + 1)

            filtered_lines = []
            for row_idx, line in enumerate(block_lines, 1):
                line_str = line.strip()
                if not line_str:
                    continue
                if (row_idx - 1) in skip_indices:
                    continue
                parsed_row = _parse_gfm_row(line, require_outer_pipes=False, min_cells=2)
                if parsed_row is not None:
                    _, parts = parsed_row
                    if _is_gfm_delimiter_row(parts):
                        continue
                    if len(parts) >= 3:
                        status_cell = parts[-1]
                        if not claim_marker_re.search(status_cell):
                            errors.append(
                                f"Empirical block lint in {doc_name} (line {line_offset + row_idx - 1}): "
                                f"status cell {status_cell!r} must be governed by a <!-- claim:... --> marker"
                            )
                        metric_cells = " | ".join(parts[1:])
                        filtered_lines.append(metric_cells)
                        continue
                filtered_lines.append(line_str)
            block_to_check = "\n".join(filtered_lines)

            stripped = re.sub(r"<!--\s*claim:[^>]+-->.*?<!--\s*/claim\s*-->", " ", block_to_check, flags=re.DOTALL)
            stripped = re.sub(r"<!--.*?-->", " ", stripped, flags=re.DOTALL)

            digits = re.findall(r"\d+", stripped)
            if digits:
                errors.append(
                    f"Empirical block lint in {doc_name} (around line {line_offset}): "
                    f"found unmanaged digits outside claim marker in empirical block: {digits[:5]}"
                )

    return len(errors) == 0, errors


def sync_document(
    doc_path: Path,
    registry_path: Path,
    write: bool = False,
    repo_root: Path = REPO_ROOT,
    validated_json_cache: dict[Path, dict] | None = None,
) -> tuple[bool, list[str]]:
    """Verify or update claim markers in doc_path against registry_path."""
    messages: list[str] = []
    errors: list[str] = []

    if not registry_path.is_file():
        return False, [f"Registry not found: {registry_path}"]
    if not doc_path.is_file():
        return False, [f"Target doc not found: {doc_path}"]

    raw_reg = registry_path.read_text(encoding="utf-8")
    if _has_personal_host_path_text(raw_reg):
        errors.append(f"Registry {registry_path.name} contains leaked personal host path")
    registry = _loads_strict_json(raw_reg)
    claims = registry.get("claims", {})
    resolved_values: dict[str, str] = {}

    for claim_id, claim_cfg in claims.items():
        try:
            resolved_values[claim_id] = resolve_claim_value(
                claim_id,
                claim_cfg,
                repo_root=repo_root,
                validated_json_cache=validated_json_cache,
            )
        except Exception as exc:
            errors.append(f"Failed to resolve claim '{claim_id}': {exc}")

    content = doc_path.read_text(encoding="utf-8")
    claim_pat = re.compile(r"<!--\s*claim:([a-zA-Z0-9_\-]+)\s*-->(.*?)<!--\s*/claim\s*-->", re.DOTALL)

    found_claims: set[str] = set()
    for m in claim_pat.finditer(content):
        cid = m.group(1)
        current_val = m.group(2)
        found_claims.add(cid)

        if cid not in resolved_values:
            errors.append(f"Unknown claim ID '{cid}' used in {doc_path.name}")
            continue

        expected_val = resolved_values[cid]
        if current_val != expected_val:
            if write:
                messages.append(f"Updated claim '{cid}' from {current_val!r} to {expected_val!r}")
            else:
                errors.append(
                    f"Claim drift for '{cid}' in {doc_path.name}: "
                    f"expected {expected_val!r}, found {current_val!r}"
                )

    try:
        doc_rel = str(doc_path.resolve().relative_to(repo_root.resolve())).replace("\\", "/")
    except ValueError:
        doc_rel = None

    if doc_rel:
        for claim_id, claim_cfg in claims.items():
            used_in = [u.replace("\\", "/") for u in claim_cfg.get("used_in", [])]
            if doc_rel in used_in and claim_id not in found_claims:
                errors.append(
                    f"Missing required claim marker '<!-- claim:{claim_id} -->' in {doc_rel} "
                    f"(registered in claims_registry.json 'used_in')"
                )

    if write:
        def repl(match):
            cid = match.group(1)
            if cid in resolved_values:
                return f"<!-- claim:{cid} -->{resolved_values[cid]}<!-- /claim -->"
            return match.group(0)

        updated_content = claim_pat.sub(repl, content)
        if updated_content != content:
            doc_path.write_text(updated_content, encoding="utf-8")
            messages.append(f"Wrote updated claims to {doc_path.name}")

    latest_text = doc_path.read_text(encoding="utf-8")
    marker_ok, marker_errors = lint_claim_markers_and_regions(
        latest_text, doc_path.name, registry_path=registry_path
    )
    if not marker_ok:
        errors.extend(marker_errors)

    word_ok, word_errors = lint_prose_and_words(
        latest_text, doc_path.name, registry_path=registry_path, repo_root=repo_root
    )
    if not word_ok:
        errors.extend(word_errors)

    if errors:
        return False, errors
    return True, messages


def main() -> int:
    parser = argparse.ArgumentParser(description="Synchronize and lint Genesis documentation claims.")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true", help="Check documentation claims without modifying files")
    mode.add_argument("--write", action="store_true", help="Overwrite documentation claim markers with ground truth")
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY_PATH, help="Path to claims_registry.json")
    parser.add_argument("--doc", type=Path, default=None, help="Specific markdown document to check/sync")

    args = parser.parse_args()
    do_write = args.write

    print(f"=== Genesis Claims Synchronizer & Linter v2 (mode: {'WRITE' if do_write else 'CHECK'}) ===")
    overall_success = True

    if args.doc is None:
        reg_ok, reg_errs = validate_claims_registry(args.registry, repo_root=REPO_ROOT)
        if not reg_ok:
            overall_success = False
            for err in reg_errs:
                print(f"[FAIL] {err}", file=sys.stderr)
        else:
            print("[PASS] Claims registry schema, non-orphan used_in mappings & claim_term evidence verified.")

    validated_json_cache: dict[Path, dict] = {}
    bench_json = REPO_ROOT / "docs" / "benchmarks" / "empirical_benchmarks.json"
    metrics_ok, metrics_errs = independent_recompute_metrics(
        bench_json,
        repo_root=REPO_ROOT,
        validated_json_cache=validated_json_cache,
    )
    if not metrics_ok:
        overall_success = False
        for err in metrics_errs:
            print(f"[FAIL] {err}", file=sys.stderr)
    else:
        print("[PASS] Independent benchmark math recomputation & Markdown parity verified.")

    if do_write and not overall_success:
        print("\nAborting --write because registry or benchmark artifact recomputation failed.", file=sys.stderr)
        return 1

    if args.doc is not None:
        target_docs = [args.doc]
    else:
        target_docs = [DEFAULT_README_PATH]
        if args.registry.is_file():
            try:
                reg_data = _loads_strict_json(args.registry.read_text(encoding="utf-8"))
                for c_cfg in reg_data.get("claims", {}).values():
                    for rel_p in c_cfg.get("used_in", []):
                        candidate = REPO_ROOT / rel_p
                        if candidate not in target_docs:
                            target_docs.append(candidate)
            except Exception as exc:
                overall_success = False
                print(f"[FAIL] Failed to parse {args.registry}: {exc}", file=sys.stderr)

    for doc_target in target_docs:
        doc_ok, doc_msgs = sync_document(
            doc_target,
            args.registry,
            write=do_write,
            repo_root=REPO_ROOT,
            validated_json_cache=validated_json_cache,
        )
        if not doc_ok:
            overall_success = False
            for err in doc_msgs:
                print(f"[FAIL] {err}", file=sys.stderr)
        else:
            for msg in doc_msgs:
                print(f"[INFO] {msg}")
            print(f"[PASS] Document claims and empirical constraints verified for {doc_target.name}.")

    if overall_success:
        print("\nAll Genesis claims and empirical guardrails verified successfully.")
        return 0
    print("\nClaims synchronization / lint failed.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())

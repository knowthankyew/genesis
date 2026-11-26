"""
Genesis Empirical Claims Linter Test Suite.

Asserts that all quantitative claims, Go source constants, benchmark metrics,
latency percentiles, memory bounds, and guarded vocabulary across JSON/Markdown
artifacts and documentation match empirical runs with zero hardcoded drift
and zero personal user paths.
"""

import json
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DOCS_DIR = REPO_ROOT / "docs"
sys.path.insert(0, str(REPO_ROOT / "scripts"))
import sync_docs  # noqa: E402


class TestClaimsLinter(unittest.TestCase):
    def test_go_constants_and_safe_bounds(self):
        """Verify Go engine/parser/storage constants match expected architectural bounds."""
        max_line = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "parser" / "parser.go", "DefaultMaxLineLength"
        )
        self.assertEqual(max_line, 65536)

        pool_size = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "parser" / "parser.go", "MaxServicePoolSize"
        )
        self.assertEqual(pool_size, 1024)

        entry_overhead = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "parser" / "parser.go", "EntryBaseOverheadBytes"
        )
        self.assertEqual(entry_overhead, 360)

        max_seg = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "storage" / "storage.go", "MaxSegmentBytes"
        )
        self.assertEqual(max_seg, 16 * 1024 * 1024)

        max_query_limit = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "index" / "index.go", "MaxQueryLimit"
        )
        self.assertEqual(max_query_limit, 10000)

        mem_limit_mb = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "cli" / "server.go", "GoRuntimeMemoryLimitMB"
        )
        self.assertEqual(mem_limit_mb, 480)

        shard_count = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "index" / "index.go", "NumPartitionShards"
        )
        self.assertEqual(shard_count, 8)

        chunk_entries = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "index" / "index.go", "MaxChunkEntries"
        )
        self.assertEqual(chunk_entries, 4096)

        evict_trigger = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "index" / "index.go", "EvictionTriggerPercent"
        )
        evict_target = sync_docs.extract_go_constant(
            REPO_ROOT / "internal" / "index" / "index.go", "EvictionTargetPercent"
        )
        self.assertGreater(evict_trigger, 50)
        self.assertLess(evict_target, evict_trigger)

    def test_empirical_benchmarks_json_and_recomputation(self):
        """Verify empirical_benchmarks.json math, latency ordering, and zero /Users/ paths."""
        bench_path = DOCS_DIR / "benchmarks" / "empirical_benchmarks.json"
        self.assertTrue(bench_path.is_file(), f"Missing {bench_path}")

        raw = bench_path.read_text(encoding="utf-8")
        self.assertNotIn("/Users/", raw, "Found leaked /Users/ path in empirical_benchmarks.json")

        ok, errs = sync_docs.independent_recompute_metrics(bench_path, repo_root=REPO_ROOT)
        self.assertTrue(ok, f"Benchmark recomputation failed: {errs}")

    def test_all_registered_claims_resolve_and_sync(self):
        """Verify every claim in docs/claims_registry.json resolves and matches its target docs."""
        reg_path = DOCS_DIR / "claims_registry.json"
        self.assertTrue(reg_path.is_file(), f"Missing {reg_path}")
        reg = json.loads(reg_path.read_text(encoding="utf-8"))

        claims = reg.get("claims", {})
        self.assertGreaterEqual(len(claims), 12)

        target_docs = set()
        for cid, cfg in claims.items():
            val = sync_docs.resolve_claim_value(cid, cfg, repo_root=REPO_ROOT)
            self.assertTrue(str(val).strip(), f"Empty resolved value for {cid}")
            for rel_doc in cfg.get("used_in", []):
                target_docs.add(REPO_ROOT / rel_doc)

        for doc_path in sorted(target_docs):
            ok, errs = sync_docs.sync_document(doc_path, reg_path, write=False, repo_root=REPO_ROOT)
            self.assertTrue(ok, f"Document sync failed for {doc_path.name}: {errs}")

    def test_negative_claim_drift_detected(self):
        """Verify sync_document fails closed when a claim value drifts from ground truth."""
        reg_path = DOCS_DIR / "claims_registry.json"
        with tempfile.TemporaryDirectory() as tmp:
            drifted_doc = Path(tmp) / "README.md"
            drifted_doc.write_text(
                "<!-- claim:bench_throughput_lps -->999,999.99 lines/sec<!-- /claim -->\n",
                encoding="utf-8",
            )
            ok, errs = sync_docs.sync_document(drifted_doc, reg_path, write=False, repo_root=REPO_ROOT)
            self.assertFalse(ok)
            self.assertTrue(any("Claim drift" in e for e in errs), f"Expected Claim drift error, got {errs}")

    def test_negative_unmanaged_digit_in_empirical_block_detected(self):
        """Verify check_empirical_regions fails closed when an unmanaged digit or status appears in an empirical table."""
        bad_md = """
<!-- BEGIN_EMPIRICAL -->
| Metric | Target | Measured Result | Status |
| :--- | :--- | :--- | :--- |
| **Throughput** | 50,000 lines/sec | **<!-- claim:bench_throughput_lps -->277,192.64 lines/sec<!-- /claim -->** | PASSED |
<!-- END_EMPIRICAL -->
"""
        ok, errs = sync_docs.check_empirical_regions(bad_md, "bad.md")
        self.assertFalse(ok)
        self.assertTrue(any("unmanaged digits" in e for e in errs))
        self.assertTrue(any("status cell" in e for e in errs))

    def test_negative_prohibited_and_unguarded_words_detected(self):
        """Verify lint_prose_and_words catches prohibited superlatives and unguarded claims."""
        reg_path = DOCS_DIR / "claims_registry.json"

        ok1, errs1 = sync_docs.lint_prose_and_words(
            "Genesis is a bulletproof and infinitely scalable log engine.", "test1.md", registry_path=reg_path
        )
        self.assertFalse(ok1)
        self.assertGreaterEqual(len(errs1), 2)

        ok2, errs2 = sync_docs.lint_prose_and_words(
            "This is 100% verified without a marker.", "test2.md", registry_path=reg_path
        )
        self.assertFalse(ok2)
        self.assertGreaterEqual(len(errs2), 2)

        # Meta-phrases must NOT bypass prohibited adjectives, and /Users/, /home/, C:\Users\ must fail closed.
        ok3, errs3 = sync_docs.lint_prose_and_words(
            "Note on prohibited word policy: this engine is bulletproof.", "test3.md", registry_path=reg_path
        )
        self.assertFalse(ok3)
        self.assertTrue(any("bulletproof" in e for e in errs3), errs3)

        for leaked in (
            "Saved in /Users/alice/genesis.seg",
            "Saved in /home/alice/genesis.seg",
            r"Saved in C:\Users\alice\genesis.seg",
        ):
            ok_p, errs_p = sync_docs.lint_prose_and_words(leaked, "path.md", registry_path=reg_path)
            self.assertFalse(ok_p, leaked)
            self.assertTrue(any("Personal path leak" in e for e in errs_p), errs_p)

    def test_hardened_json_registry_markers_recompute_and_static_boundaries(self):
        """Verify strict JSON, registry hygiene, cross-doc markers, GFM parity, server zero-egress, and landscape cadence."""
        import importlib.util

        # 1. Strict JSON rejects NaN, 1e309 float overflow, and duplicate keys.
        for bad_raw, sub in (
            ('{"v": NaN}', "Non-standard non-finite"),
            ('{"v": 1e309}', "Non-finite JSON floating-point"),
            ('{"k": 1, "k": 2}', "Duplicate JSON object member"),
        ):
            with self.assertRaises(ValueError) as ctx:
                sync_docs._loads_strict_json(bad_raw)
            self.assertIn(sub, str(ctx.exception))

        # 2. Registry validation passes on committed registry and rejects orphans / missing markers.
        reg_path = DOCS_DIR / "claims_registry.json"
        ok_reg, errs_reg = sync_docs.validate_claims_registry(reg_path, repo_root=REPO_ROOT)
        self.assertTrue(ok_reg, errs_reg)

        with tempfile.TemporaryDirectory() as tmp:
            tmp_root = Path(tmp)
            (tmp_root / "docs" / "benchmarks").mkdir(parents=True)
            (tmp_root / "internal" / "parser").mkdir(parents=True)
            (tmp_root / "internal" / "parser" / "parser.go").write_text(
                "package parser\nconst DefaultMaxLineLength = 65536\n", encoding="utf-8"
            )
            orphan_reg = tmp_root / "docs" / "orphan.json"
            orphan_reg.write_text(
                json.dumps(
                    {
                        "version": "2.0.0",
                        "claims": {
                            "max_line": {
                                "source_artifact": "internal/parser/parser.go",
                                "go_const": "DefaultMaxLineLength",
                                "format_rule": "{value}",
                                "used_in": [],
                            }
                        },
                    }
                ),
                encoding="utf-8",
            )
            ok_o, errs_o = sync_docs.validate_claims_registry(orphan_reg, repo_root=tmp_root)
            self.assertFalse(ok_o)
            self.assertTrue(any("orphaned claim" in e for e in errs_o), errs_o)

            # 3. Cross-doc marker linter catches unknown/unclosed/mismatched/unbalanced while ignoring fences.
            fenced_ok, fenced_errs = sync_docs.lint_claim_markers_and_regions(
                "```md\n<!-- claim:fake -->1<!-- /claim -->\n<!-- BEGIN_EMPIRICAL -->\n```\n",
                "CONTRIBUTING.md",
                registry_path=reg_path,
            )
            self.assertTrue(fenced_ok, fenced_errs)

            for bad_doc, sub in (
                ("<!-- claim:no_such_claim -->1<!-- /claim -->", "unknown claim ID"),
                ("<!-- claim:bench_p50_us -->10", "unclosed"),
                ("orphan <!-- /claim -->", "orphan closing"),
                ("<!-- BEGIN_EMPIRICAL -->\n| A | B | C |\n", "unbalanced empirical delimiters"),
            ):
                ok_m, errs_m = sync_docs.lint_claim_markers_and_regions(bad_doc, "doc.md", registry_path=reg_path)
                self.assertFalse(ok_m, bad_doc)
                self.assertTrue(any(sub in e for e in errs_m), (bad_doc, errs_m))

            # 4. Data row mentioning "Measured Result" in BEGIN_EMPIRICAL must NOT bypass checks.
            bypass_emp = (
                "<!-- BEGIN_EMPIRICAL -->\n"
                "| Gate | Target | Measured Result | Status |\n"
                "| :--- | :--- | :--- | :--- |\n"
                "| **Fake** | 123 | Measured Result 999 | PASSED |\n"
                "<!-- END_EMPIRICAL -->\n"
            )
            ok_emp, errs_emp = sync_docs.check_empirical_regions(bypass_emp, "bypass.md")
            self.assertFalse(ok_emp)
            self.assertTrue(any("status cell" in e for e in errs_emp), errs_emp)
            self.assertTrue(any("unmanaged digits" in e for e in errs_emp), errs_emp)

            # 5. Recompute checks: dirty git_commit, -r2/-r1 filenames, hidden HTML-commented table rows, recomputed TPS boundary.
            base_bench = json.loads((DOCS_DIR / "benchmarks" / "empirical_benchmarks.json").read_text(encoding="utf-8"))
            md_rel = base_bench["metadata"]["report_markdown"]
            md_text = (REPO_ROOT / md_rel).read_text(encoding="utf-8")

            r2_rel = md_rel[:-3] + "-r2.md"
            (tmp_root / r2_rel).write_text(md_text, encoding="utf-8")
            r2_bench = json.loads(json.dumps(base_bench))
            r2_bench["metadata"]["report_markdown"] = r2_rel
            p_r2 = tmp_root / "r2.json"
            p_r2.write_text(json.dumps(r2_bench), encoding="utf-8")
            ok_r2, errs_r2 = sync_docs.independent_recompute_metrics(p_r2, repo_root=tmp_root)
            self.assertTrue(ok_r2, errs_r2)

            dirty_bench = json.loads(json.dumps(base_bench))
            dirty_bench["metadata"]["git_commit"] = f"{base_bench['metadata']['git_commit']}-dirty"
            p_dirty = tmp_root / "dirty.json"
            p_dirty.write_text(json.dumps(dirty_bench), encoding="utf-8")
            ok_d, errs_d = sync_docs.independent_recompute_metrics(p_dirty, repo_root=tmp_root)
            self.assertFalse(ok_d)
            self.assertTrue(any("dirty/unknown" in e for e in errs_d), errs_d)

            hidden_root = tmp_root / "hidden_md"
            (hidden_root / "docs" / "benchmarks").mkdir(parents=True)
            hidden_md = md_text.replace(
                "| **Throughput (Lines Indexed)** | $\\ge 50,000$ lines/sec over 60s | **1046322.42 lines/sec** | ✅ PASS |",
                "<!-- | **Throughput (Lines Indexed)** | $\\ge 50,000$ lines/sec over 60s | **1046322.42 lines/sec** | ✅ PASS | -->\n"
                "| **Throughput (Lines Indexed)** | $\\ge 50,000$ lines/sec over 60s | **999.00 lines/sec** | ❌ FAIL |",
            )
            (hidden_root / md_rel).write_text(hidden_md, encoding="utf-8")
            p_hid = hidden_root / "bench.json"
            p_hid.write_text(json.dumps(base_bench), encoding="utf-8")
            ok_h, errs_h = sync_docs.independent_recompute_metrics(p_hid, repo_root=hidden_root)
            self.assertFalse(ok_h)
            self.assertTrue(any("published-table parity error" in e for e in errs_h), errs_h)

            # 6. audit-claims.py static server-core zero-egress boundary & fail-closed landscape cadence cap.
            spec = importlib.util.spec_from_file_location("genesis_audit_mod", REPO_ROOT / "scripts" / "audit-claims.py")
            audit_mod = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(audit_mod)

            live_errs: list[str] = []
            audit_mod.audit_go_server_network_egress_boundary(live_errs, repo_root=REPO_ROOT)
            audit_mod.audit_landscape_freshness(live_errs, repo_root=REPO_ROOT)
            self.assertEqual(live_errs, [])

            (tmp_root / "internal" / "engine").mkdir(parents=True)
            (tmp_root / "internal" / "engine" / "bad_egress.go").write_text(
                'package engine\nimport ("net"; "net/http")\n'
                'func leak() { var d net.Dialer; _, _ = net.Dial("tcp", "1.1.1.1:80"); _, _ = http.Get("https://x"); _ = d }\n',
                encoding="utf-8",
            )
            egress_errs: list[str] = []
            audit_mod.audit_go_server_network_egress_boundary(egress_errs, repo_root=tmp_root)
            self.assertEqual(len(egress_errs), 3, egress_errs)

            for bad_cad, expected_sub in (("99999", "between 1 and 180"), ("invalid", "not a valid integer")):
                (tmp_root / "docs" / "ALTERNATIVES.md").write_text(
                    f"<!-- landscape-meta\nlast-reviewed: 2026-11-10\nreview-cadence-days: {bad_cad}\n"
                    "reviewed-versions: [a@1, b@2, c@3]\n-->\n# Alt\n",
                    encoding="utf-8",
                )
                cad_errs: list[str] = []
                audit_mod.audit_landscape_freshness(cad_errs, repo_root=tmp_root)
                self.assertTrue(any(expected_sub in e for e in cad_errs), (bad_cad, cad_errs))


if __name__ == "__main__":
    unittest.main()

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


if __name__ == "__main__":
    unittest.main()

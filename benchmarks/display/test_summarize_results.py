import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("summarize_results.py")
SPEC = importlib.util.spec_from_file_location("summarize_results", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def physical_result():
    frames = [10.0 + (index % 7) for index in range(300)]
    return {
        "measurement_status": "physical",
        "board": "test board",
        "board_revision": "test revision",
        "firmware_commit": "0123456789abcdef",
        "esp_idf_version": "test",
        "stack": "test",
        "display": "test",
        "workload": {
            "partial": {"frames_ms": frames},
            "full": {"frames_ms": frames},
        },
        "coexistence": {
            "audio_playback": True,
            "wifi_traffic": True,
            "ble_activity": "not_supported",
        },
        "binary_size_bytes": 1,
        "heap": {
            "internal_free_bytes": 0,
            "internal_minimum_free_bytes": 0,
            "internal_largest_block_bytes": 0,
            "psram_free_bytes": 0,
            "psram_minimum_free_bytes": 0,
            "psram_largest_block_bytes": 0,
        },
        "frames_ms": "deprecated: per-workload values are authoritative",
        "visual_inspection": "test",
        "recovery": "test",
    }


class DisplayBenchmarkSummaryTests(unittest.TestCase):
    def test_reports_nearest_rank_percentiles(self):
        summary = MODULE.summarize(physical_result())
        self.assertEqual(summary["runs"]["full"]["sample_count"], 300)
        self.assertEqual(summary["runs"]["full"]["p50_ms"], 13.0)
        self.assertEqual(summary["runs"]["full"]["p95_ms"], 16.0)
        self.assertTrue(summary["runs"]["full"]["meets_initial_30fps_p95_target"])

    def test_pending_results_are_not_evidence(self):
        result = physical_result()
        result["measurement_status"] = "pending"
        with self.assertRaisesRegex(MODULE.BenchmarkError, "physical"):
            MODULE.summarize(result)

    def test_rejects_short_runs(self):
        result = physical_result()
        result["workload"]["partial"]["frames_ms"] = [1.0] * 299
        with self.assertRaisesRegex(MODULE.BenchmarkError, "at least 300"):
            MODULE.summarize(result)


if __name__ == "__main__":
    unittest.main()

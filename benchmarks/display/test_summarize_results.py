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
        "firmware_commit": "0123456789abcdef0123456789abcdef01234567",
        "raw_log_sha256": "a" * 64,
        "dependency_lock_sha256": "b" * 64,
        "esp_idf_version": "v6.0.2",
        "stack": "test",
        "display": "test",
        "workload": {
            "partial": {"frames_ms": frames, "dropped_frames": 0},
            "full": {"frames_ms": frames, "dropped_frames": 2},
        },
        "coexistence": {
            "audio_playback": True,
            "wifi_traffic": True,
            "ble_activity": False,
            "ble_reason": "BLE run unavailable in this fixture",
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
        "power": {
            "instrumented": False,
            "reason": "No current probe in unit-test fixture",
            "supply_voltage_v": None,
            "sample_rate_hz": None,
            "scenarios_ma": {},
        },
        "visual_inspection": "test",
        "recovery": "test",
    }


def instrumented_power():
    values = {"p50": 100.0, "p95": 120.0, "peak": 150.0}
    return {
        "instrumented": True,
        "reason": "",
        "supply_voltage_v": 5.0,
        "sample_rate_hz": 1000.0,
        "scenarios_ma": {
            name: dict(values)
            for name in MODULE.REQUIRED_POWER_SCENARIOS
        },
    }


class DisplayBenchmarkSummaryTests(unittest.TestCase):
    def test_reports_nearest_rank_percentiles_and_drops(self):
        summary = MODULE.summarize(physical_result())
        self.assertEqual(summary["runs"]["full"]["sample_count"], 300)
        self.assertEqual(summary["runs"]["full"]["dropped_frames"], 2)
        self.assertEqual(summary["runs"]["full"]["p50_ms"], 13.0)
        self.assertEqual(summary["runs"]["full"]["p95_ms"], 16.0)
        self.assertTrue(summary["runs"]["full"]["meets_initial_30fps_p95_target"])
        self.assertFalse(summary["power_measurement_complete"])

    def test_accepts_complete_instrumented_power(self):
        result = physical_result()
        result["power"] = instrumented_power()
        summary = MODULE.summarize(result)
        self.assertTrue(summary["power_measurement_complete"])

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

    def test_rejects_invalid_dropped_frames(self):
        result = physical_result()
        result["workload"]["partial"]["dropped_frames"] = -1
        with self.assertRaisesRegex(MODULE.BenchmarkError, "dropped_frames"):
            MODULE.summarize(result)

    def test_rejects_abbreviated_or_non_hex_firmware_commit(self):
        for value in ("0123456789abcdef", "g" * 40):
            with self.subTest(value=value):
                result = physical_result()
                result["firmware_commit"] = value
                with self.assertRaisesRegex(MODULE.BenchmarkError, "40-character hexadecimal"):
                    MODULE.summarize(result)

    def test_rejects_bad_evidence_digest(self):
        result = physical_result()
        result["raw_log_sha256"] = "short"
        with self.assertRaisesRegex(MODULE.BenchmarkError, "SHA-256"):
            MODULE.summarize(result)

    def test_uninstrumented_power_requires_reason_and_no_fake_values(self):
        result = physical_result()
        result["power"]["reason"] = ""
        with self.assertRaisesRegex(MODULE.BenchmarkError, "reason"):
            MODULE.summarize(result)

        result = physical_result()
        result["power"]["supply_voltage_v"] = 5.0
        with self.assertRaisesRegex(MODULE.BenchmarkError, "invented"):
            MODULE.summarize(result)

    def test_instrumented_power_rejects_invalid_percentile_order(self):
        result = physical_result()
        result["power"] = instrumented_power()
        result["power"]["scenarios_ma"]["idle"] = {"p50": 130.0, "p95": 120.0, "peak": 150.0}
        with self.assertRaisesRegex(MODULE.BenchmarkError, "p50 <= p95 <= peak"):
            MODULE.summarize(result)

    def test_ble_skip_requires_reason(self):
        result = physical_result()
        result["coexistence"]["ble_reason"] = ""
        with self.assertRaisesRegex(MODULE.BenchmarkError, "ble_reason"):
            MODULE.summarize(result)


if __name__ == "__main__":
    unittest.main()

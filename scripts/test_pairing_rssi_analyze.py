#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("pairing_rssi_analyze.py")
SPEC = importlib.util.spec_from_file_location("pairing_rssi_analyze", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)

FIRMWARE_SHA = "1" * 40
CONFIG_FINGERPRINT = "2" * 64


def record(
    capture: str,
    near: bool,
    index: int,
    rssi: int,
    *,
    source: str = "synthetic",
    environment: str = "lab",
    distance: float = 20.0,
    orientation: str = "front",
    firmware_sha: str = FIRMWARE_SHA,
    board_revision: str = "esp32-s3-rev-a",
    esp_idf_version: str = "v6.0.2",
    config_fingerprint: str = CONFIG_FINGERPRINT,
    enclosure: str = "production-v1-prototype",
) -> dict[str, object]:
    return {
        "schema_version": 1,
        "source": source,
        "capture_id": capture,
        "dut_tx": "dut-a",
        "dut_rx": "dut-b",
        "environment": environment,
        "distance_cm": distance,
        "orientation": orientation,
        "expected_near": near,
        "firmware_sha": firmware_sha,
        "board_revision": board_revision,
        "esp_idf_version": esp_idf_version,
        "config_fingerprint": config_fingerprint,
        "enclosure": enclosure,
        "sample_index": index,
        "discovery_id": "CP-ABCDEFGHIJKLMNOP",
        "rssi_dbm": rssi,
        "seen_at_ms": index * 100,
    }


def capture_rows(
    capture: str,
    near: bool,
    rssi: int,
    *,
    source: str = "synthetic",
    environment: str = "lab",
    distance: float = 20.0,
    orientation: str = "front",
) -> list[dict[str, object]]:
    return [
        record(
            capture,
            near,
            index,
            rssi,
            source=source,
            environment=environment,
            distance=distance,
            orientation=orientation,
        )
        for index in range(20)
    ]


class PairingRssiAnalyzeTest(unittest.TestCase):
    def corpus(self, rows: list[dict[str, object]]) -> Path:
        directory = Path(tempfile.mkdtemp())
        path = directory / "corpus.jsonl"
        path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
        # unittest cleanups run LIFO: remove the file before removing its directory.
        self.addCleanup(lambda: directory.rmdir())
        self.addCleanup(lambda: path.unlink(missing_ok=True))
        return path

    def test_physical_flag_rejects_synthetic_fixture(self) -> None:
        path = self.corpus(
            [record("near", True, index, -45) for index in range(3)]
            + [record("far", False, index, -90) for index in range(3)]
        )
        with self.assertRaisesRegex(ValueError, "require-physical rejects"):
            MODULE.load_samples(path, True)

    def test_gate_distinguishes_clean_near_and_far_captures(self) -> None:
        rows = capture_rows("near", True, -45)
        rows += capture_rows("far", False, -90)
        samples = MODULE.load_samples(self.corpus(rows), False)
        captures = MODULE.build_captures(samples, 20)
        result = MODULE.metrics(captures, -65, 3, 2)
        self.assertEqual(result["tp"], 1)
        self.assertEqual(result["tn"], 1)
        self.assertEqual(result["fp"], 0)
        self.assertEqual(result["fn"], 0)

    def test_invalid_alias_is_rejected(self) -> None:
        rows = capture_rows("near", True, -50)
        rows += capture_rows("far", False, -85)
        rows[3]["discovery_id"] = "not-a-device-id"
        with self.assertRaisesRegex(ValueError, "invalid rotating discovery_id"):
            MODULE.load_samples(self.corpus(rows), False)

    def test_capture_metadata_and_provenance_cannot_change_mid_run(self) -> None:
        rows = capture_rows("near", True, -50)
        rows += capture_rows("far", False, -85)
        rows[4]["config_fingerprint"] = "3" * 64
        samples = MODULE.load_samples(self.corpus(rows), False)
        with self.assertRaisesRegex(ValueError, "metadata changes"):
            MODULE.build_captures(samples, 20)

    def test_coverage_gate_rejects_synthetic_experiment_shape(self) -> None:
        rows = capture_rows("near", True, -50)
        rows += capture_rows("far", False, -85)
        samples = MODULE.load_samples(self.corpus(rows), False)
        captures = MODULE.build_captures(samples, 20)
        summary = MODULE.coverage(samples, captures)
        with self.assertRaisesRegex(ValueError, "physical_hil samples only"):
            MODULE.enforce_coverage(summary)

    def test_valid_physical_coverage_requires_one_immutable_candidate(self) -> None:
        rows: list[dict[str, object]] = []
        rows += capture_rows(
            "near-open", True, -45, source="physical_hil", environment="open", distance=20, orientation="front"
        )
        rows += capture_rows(
            "near-room", True, -50, source="physical_hil", environment="room", distance=30, orientation="side"
        )
        rows += capture_rows(
            "near-obstructed", True, -55, source="physical_hil", environment="obstructed", distance=40, orientation="front"
        )
        rows += capture_rows(
            "far-open", False, -85, source="physical_hil", environment="open", distance=100, orientation="side"
        )
        rows += capture_rows(
            "far-room", False, -88, source="physical_hil", environment="room", distance=150, orientation="front"
        )
        rows += capture_rows(
            "far-obstructed", False, -92, source="physical_hil", environment="obstructed", distance=200, orientation="side"
        )
        samples = MODULE.load_samples(self.corpus(rows), True)
        captures = MODULE.build_captures(samples, 20)
        summary = MODULE.coverage(samples, captures)
        MODULE.enforce_coverage(summary)
        self.assertEqual(summary["firmware_shas"], [FIRMWARE_SHA])
        self.assertEqual(summary["config_fingerprints"], [CONFIG_FINGERPRINT])

    def test_top_candidates_include_environment_and_orientation_breakdown(self) -> None:
        rows = capture_rows("lab-near", True, -45, environment="lab", orientation="front")
        rows += capture_rows("lab-far", False, -90, environment="lab", orientation="side")
        rows += capture_rows("office-near", True, -50, environment="office", orientation="front")
        rows += capture_rows("office-far", False, -88, environment="office", orientation="side")
        samples = MODULE.load_samples(self.corpus(rows), False)
        captures = MODULE.build_captures(samples, 20)
        result = MODULE.analyze(samples, captures, 1)
        candidate = result["top_candidates"][0]
        self.assertEqual(set(candidate["by_environment"]), {"lab", "office"})
        self.assertEqual(set(candidate["by_orientation"]), {"front", "side"})
        for environment_metrics in candidate["by_environment"].values():
            self.assertIn("false_positive_rate", environment_metrics)
            self.assertIn("false_negative_rate", environment_metrics)


if __name__ == "__main__":
    unittest.main()

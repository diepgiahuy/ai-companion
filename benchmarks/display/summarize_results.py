#!/usr/bin/env python3
"""Validate and summarize a physical display benchmark result.

This program is intentionally dependency-free so a trusted GitHub Actions runner
can validate evidence without pretending that a host or simulator measured a
display.  It refuses pending, incomplete, or malformed physical result files.
"""

from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
from typing import Any

REQUIRED_FIELDS = (
    "measurement_status",
    "board",
    "board_revision",
    "firmware_commit",
    "esp_idf_version",
    "stack",
    "display",
    "workload",
    "coexistence",
    "binary_size_bytes",
    "heap",
    "frames_ms",
    "visual_inspection",
    "recovery",
)
REQUIRED_WORKLOADS = {"partial", "full"}
REQUIRED_HEAP = {
    "internal_free_bytes",
    "internal_minimum_free_bytes",
    "internal_largest_block_bytes",
    "psram_free_bytes",
    "psram_minimum_free_bytes",
    "psram_largest_block_bytes",
}


class BenchmarkError(ValueError):
    """The result is not evidence that can be summarized."""


def percentile_nearest_rank(sorted_values: list[float], quantile: float) -> float:
    """Return the nearest-rank percentile; input must be sorted and non-empty."""
    if not sorted_values:
        raise BenchmarkError("frames_ms must not be empty")
    if not 0 < quantile <= 1:
        raise BenchmarkError("quantile must be in (0, 1]")
    return sorted_values[math.ceil(quantile * len(sorted_values)) - 1]


def _require_string(document: dict[str, Any], field: str) -> str:
    value = document.get(field)
    if not isinstance(value, str) or not value.strip():
        raise BenchmarkError(f"{field} must be a non-empty string")
    return value


def validate(document: dict[str, Any]) -> None:
    missing = [field for field in REQUIRED_FIELDS if field not in document]
    if missing:
        raise BenchmarkError("missing fields: " + ", ".join(missing))
    if document["measurement_status"] != "physical":
        raise BenchmarkError("measurement_status must be 'physical'; pending data is not evidence")
    for field in (
        "board",
        "board_revision",
        "firmware_commit",
        "esp_idf_version",
        "stack",
        "display",
        "visual_inspection",
        "recovery",
    ):
        _require_string(document, field)

    workload = document["workload"]
    if not isinstance(workload, dict) or set(workload) != REQUIRED_WORKLOADS:
        raise BenchmarkError("workload must contain exactly partial and full runs")
    for name, run in workload.items():
        if not isinstance(run, dict):
            raise BenchmarkError(f"workload.{name} must be an object")
        frames = run.get("frames_ms")
        if not isinstance(frames, list) or len(frames) < 300:
            raise BenchmarkError(f"workload.{name}.frames_ms must contain at least 300 frames")
        if any(not isinstance(value, (int, float)) or isinstance(value, bool) or value <= 0
               for value in frames):
            raise BenchmarkError(f"workload.{name}.frames_ms must contain positive numbers")

    coexistence = document["coexistence"]
    if not isinstance(coexistence, dict) or coexistence.get("audio_playback") is not True:
        raise BenchmarkError("coexistence.audio_playback must be true")
    if coexistence.get("wifi_traffic") is not True:
        raise BenchmarkError("coexistence.wifi_traffic must be true")
    if coexistence.get("ble_activity") not in (True, False, "not_supported"):
        raise BenchmarkError("coexistence.ble_activity must be true, false, or 'not_supported'")

    if not isinstance(document["binary_size_bytes"], int) or document["binary_size_bytes"] <= 0:
        raise BenchmarkError("binary_size_bytes must be a positive integer")
    heap = document["heap"]
    if not isinstance(heap, dict) or set(heap) != REQUIRED_HEAP:
        raise BenchmarkError("heap must contain the six required internal/PSRAM values")
    if any(not isinstance(value, int) or value < 0 for value in heap.values()):
        raise BenchmarkError("heap values must be non-negative integers")


def summarize(document: dict[str, Any]) -> dict[str, Any]:
    validate(document)
    runs: dict[str, dict[str, float | int]] = {}
    for name, run in document["workload"].items():
        frames = sorted(float(value) for value in run["frames_ms"])
        runs[name] = {
            "sample_count": len(frames),
            "p50_ms": percentile_nearest_rank(frames, 0.50),
            "p95_ms": percentile_nearest_rank(frames, 0.95),
            "max_ms": frames[-1],
            "meets_initial_30fps_p95_target": percentile_nearest_rank(frames, 0.95) <= 33.0,
        }
    return {
        "evidence": {
            key: document[key]
            for key in ("board", "board_revision", "firmware_commit", "esp_idf_version",
                        "stack", "display", "binary_size_bytes", "heap",
                        "visual_inspection", "recovery")
        },
        "coexistence": document["coexistence"],
        "runs": runs,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("result", type=Path, help="physical result JSON")
    parser.add_argument("--output", type=Path, help="summary JSON output; stdout when omitted")
    args = parser.parse_args()
    try:
        document = json.loads(args.result.read_text(encoding="utf-8"))
        result = summarize(document)
    except (OSError, json.JSONDecodeError, BenchmarkError) as error:
        parser.error(str(error))
    encoded = json.dumps(result, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

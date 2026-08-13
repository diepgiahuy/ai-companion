#!/usr/bin/env python3
"""Validate and summarize a physical display benchmark result.

This program is intentionally dependency-free so a trusted GitHub Actions runner
can validate evidence without pretending that a host or simulator measured a
display. It refuses pending, incomplete, or malformed physical result files.

Structural validation is not provenance attestation: this program cannot prove that
frame samples came from a physical board or that a syntactically valid commit SHA
was actually flashed. Raw serial logs and operator/run evidence remain required.
"""

from __future__ import annotations

import argparse
import json
import math
import re
from pathlib import Path
from typing import Any

REQUIRED_FIELDS = (
    "measurement_status",
    "board",
    "board_revision",
    "firmware_commit",
    "raw_log_sha256",
    "dependency_lock_sha256",
    "esp_idf_version",
    "stack",
    "display",
    "workload",
    "coexistence",
    "binary_size_bytes",
    "heap",
    "power",
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
REQUIRED_POWER_SCENARIOS = {
    "idle",
    "full_white",
    "animation_audio",
    "animation_wifi_ble",
    "storage_write",
    "recovery",
}
FULL_GIT_SHA_RE = re.compile(r"^[0-9a-fA-F]{40}$")
SHA256_RE = re.compile(r"^[0-9a-fA-F]{64}$")


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


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def _validate_power(power: Any) -> None:
    required = {"instrumented", "reason", "supply_voltage_v", "sample_rate_hz", "scenarios_ma"}
    if not isinstance(power, dict) or set(power) != required:
        raise BenchmarkError("power must contain exactly instrumented/reason/supply_voltage_v/sample_rate_hz/scenarios_ma")
    if not isinstance(power["instrumented"], bool):
        raise BenchmarkError("power.instrumented must be boolean")
    if not isinstance(power["reason"], str):
        raise BenchmarkError("power.reason must be a string")

    if not power["instrumented"]:
        if not power["reason"].strip():
            raise BenchmarkError("power.reason is required when current instrumentation was unavailable")
        if power["supply_voltage_v"] is not None or power["sample_rate_hz"] is not None or power["scenarios_ma"] != {}:
            raise BenchmarkError("uninstrumented power evidence must not contain invented measurements")
        return

    if not _is_number(power["supply_voltage_v"]) or power["supply_voltage_v"] <= 0:
        raise BenchmarkError("power.supply_voltage_v must be positive when instrumented")
    if not _is_number(power["sample_rate_hz"]) or power["sample_rate_hz"] <= 0:
        raise BenchmarkError("power.sample_rate_hz must be positive when instrumented")
    scenarios = power["scenarios_ma"]
    if not isinstance(scenarios, dict) or set(scenarios) != REQUIRED_POWER_SCENARIOS:
        raise BenchmarkError("power.scenarios_ma must contain all required measurement scenarios")
    for name, values in scenarios.items():
        if not isinstance(values, dict) or set(values) != {"p50", "p95", "peak"}:
            raise BenchmarkError(f"power.scenarios_ma.{name} must contain exactly p50/p95/peak")
        p50, p95, peak = values["p50"], values["p95"], values["peak"]
        if any(not _is_number(value) or value < 0 for value in (p50, p95, peak)):
            raise BenchmarkError(f"power.scenarios_ma.{name} values must be finite non-negative numbers")
        if not p50 <= p95 <= peak:
            raise BenchmarkError(f"power.scenarios_ma.{name} must satisfy p50 <= p95 <= peak")


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
        "raw_log_sha256",
        "dependency_lock_sha256",
        "esp_idf_version",
        "stack",
        "display",
        "visual_inspection",
        "recovery",
    ):
        _require_string(document, field)
    if not FULL_GIT_SHA_RE.fullmatch(document["firmware_commit"]):
        raise BenchmarkError("firmware_commit must be a full 40-character hexadecimal Git commit SHA")
    for field in ("raw_log_sha256", "dependency_lock_sha256"):
        if not SHA256_RE.fullmatch(document[field]):
            raise BenchmarkError(f"{field} must be a 64-character hexadecimal SHA-256 digest")

    workload = document["workload"]
    if not isinstance(workload, dict) or set(workload) != REQUIRED_WORKLOADS:
        raise BenchmarkError("workload must contain exactly partial and full runs")
    for name, run in workload.items():
        if not isinstance(run, dict) or set(run) != {"frames_ms", "dropped_frames"}:
            raise BenchmarkError(f"workload.{name} must contain exactly frames_ms and dropped_frames")
        frames = run["frames_ms"]
        if not isinstance(frames, list) or len(frames) < 300:
            raise BenchmarkError(f"workload.{name}.frames_ms must contain at least 300 frames")
        if any(not _is_number(value) or value <= 0 for value in frames):
            raise BenchmarkError(f"workload.{name}.frames_ms must contain positive finite numbers")
        dropped = run["dropped_frames"]
        if not isinstance(dropped, int) or isinstance(dropped, bool) or dropped < 0:
            raise BenchmarkError(f"workload.{name}.dropped_frames must be a non-negative integer")

    coexistence = document["coexistence"]
    if not isinstance(coexistence, dict) or set(coexistence) != {
        "audio_playback", "wifi_traffic", "ble_activity", "ble_reason"
    }:
        raise BenchmarkError("coexistence must contain audio_playback/wifi_traffic/ble_activity/ble_reason")
    if coexistence["audio_playback"] is not True:
        raise BenchmarkError("coexistence.audio_playback must be true")
    if coexistence["wifi_traffic"] is not True:
        raise BenchmarkError("coexistence.wifi_traffic must be true")
    if not isinstance(coexistence["ble_activity"], bool):
        raise BenchmarkError("coexistence.ble_activity must be boolean")
    if not isinstance(coexistence["ble_reason"], str):
        raise BenchmarkError("coexistence.ble_reason must be a string")
    if not coexistence["ble_activity"] and not coexistence["ble_reason"].strip():
        raise BenchmarkError("coexistence.ble_reason is required when BLE was not exercised")

    if not isinstance(document["binary_size_bytes"], int) or isinstance(document["binary_size_bytes"], bool) or document["binary_size_bytes"] <= 0:
        raise BenchmarkError("binary_size_bytes must be a positive integer")
    heap = document["heap"]
    if not isinstance(heap, dict) or set(heap) != REQUIRED_HEAP:
        raise BenchmarkError("heap must contain the six required internal/PSRAM values")
    if any(not isinstance(value, int) or isinstance(value, bool) or value < 0 for value in heap.values()):
        raise BenchmarkError("heap values must be non-negative integers")

    _validate_power(document["power"])


def summarize(document: dict[str, Any]) -> dict[str, Any]:
    validate(document)
    runs: dict[str, dict[str, float | int | bool]] = {}
    for name, run in document["workload"].items():
        frames = sorted(float(value) for value in run["frames_ms"])
        p95 = percentile_nearest_rank(frames, 0.95)
        runs[name] = {
            "sample_count": len(frames),
            "dropped_frames": run["dropped_frames"],
            "p50_ms": percentile_nearest_rank(frames, 0.50),
            "p95_ms": p95,
            "max_ms": frames[-1],
            "meets_initial_30fps_p95_target": p95 <= 33.0,
        }
    return {
        "evidence": {
            key: document[key]
            for key in (
                "board", "board_revision", "firmware_commit", "raw_log_sha256",
                "dependency_lock_sha256", "esp_idf_version", "stack", "display",
                "binary_size_bytes", "heap", "power", "visual_inspection", "recovery"
            )
        },
        "coexistence": document["coexistence"],
        "power_measurement_complete": document["power"]["instrumented"],
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

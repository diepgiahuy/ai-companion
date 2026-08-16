#!/usr/bin/env python3
"""Validate a pairing RSSI corpus and sweep conservative proximity gates.

This tool is deliberately evidence-neutral: it reports metrics but never edits
`evidence/status.json` or declares a production threshold. Acceptance coverage
requires an explicitly physical corpus with immutable build/hardware provenance.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import statistics
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Iterable


@dataclass(frozen=True)
class Sample:
    source: str
    capture_id: str
    dut_tx: str
    dut_rx: str
    environment: str
    distance_cm: float
    orientation: str
    expected_near: bool
    firmware_sha: str
    board_revision: str
    esp_idf_version: str
    config_fingerprint: str
    enclosure: str
    sample_index: int
    discovery_id: str
    rssi_dbm: int
    seen_at_ms: int


@dataclass(frozen=True)
class Capture:
    capture_id: str
    expected_near: bool
    samples: tuple[Sample, ...]


REQUIRED_KEYS = {
    "schema_version",
    "source",
    "capture_id",
    "dut_tx",
    "dut_rx",
    "environment",
    "distance_cm",
    "orientation",
    "expected_near",
    "firmware_sha",
    "board_revision",
    "esp_idf_version",
    "config_fingerprint",
    "enclosure",
    "sample_index",
    "discovery_id",
    "rssi_dbm",
    "seen_at_ms",
}
GIT_SHA_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("corpus", type=Path)
    p.add_argument("--output", type=Path)
    p.add_argument("--require-physical", action="store_true")
    p.add_argument("--require-coverage", action="store_true")
    p.add_argument("--minimum-samples-per-capture", type=int, default=20)
    p.add_argument("--top", type=int, default=20)
    return p.parse_args()


def _nonempty_text(value: object, key: str, line: int) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"line {line}: {key} must be non-empty text")
    return value.strip()


def _valid_alias(value: str) -> bool:
    return (
        len(value) == 19
        and value.startswith("CP-")
        and all(("A" <= c <= "Z") or ("2" <= c <= "7") for c in value[3:])
    )


def _validated_git_sha(value: object, line: int) -> str:
    normalized = _nonempty_text(value, "firmware_sha", line).lower()
    if not GIT_SHA_RE.fullmatch(normalized):
        raise ValueError(f"line {line}: firmware_sha must be a full 40-character Git SHA")
    return normalized


def _validated_config_fingerprint(value: object, line: int) -> str:
    normalized = _nonempty_text(value, "config_fingerprint", line).lower()
    if not SHA256_RE.fullmatch(normalized):
        raise ValueError(f"line {line}: config_fingerprint must be a SHA-256 hex digest")
    return normalized


def load_samples(path: Path, require_physical: bool) -> list[Sample]:
    samples: list[Sample] = []
    with path.open("r", encoding="utf-8") as source:
        for line_number, raw in enumerate(source, 1):
            raw = raw.strip()
            if not raw:
                continue
            try:
                item = json.loads(raw)
            except json.JSONDecodeError as exc:
                raise ValueError(f"line {line_number}: invalid JSON: {exc}") from exc
            if not isinstance(item, dict):
                raise ValueError(f"line {line_number}: record must be an object")
            missing = sorted(REQUIRED_KEYS - item.keys())
            if missing:
                raise ValueError(f"line {line_number}: missing keys {missing}")
            if item["schema_version"] != 1:
                raise ValueError(f"line {line_number}: unsupported schema_version")
            source_kind = _nonempty_text(item["source"], "source", line_number)
            if source_kind not in {"physical_hil", "synthetic"}:
                raise ValueError(f"line {line_number}: unsupported source {source_kind!r}")
            if require_physical and source_kind != "physical_hil":
                raise ValueError(
                    f"line {line_number}: --require-physical rejects {source_kind!r}"
                )
            dut_tx = _nonempty_text(item["dut_tx"], "dut_tx", line_number)
            dut_rx = _nonempty_text(item["dut_rx"], "dut_rx", line_number)
            if dut_tx == dut_rx:
                raise ValueError(f"line {line_number}: dut_tx and dut_rx must differ")
            discovery_id = _nonempty_text(
                item["discovery_id"], "discovery_id", line_number
            )
            if not _valid_alias(discovery_id):
                raise ValueError(f"line {line_number}: invalid rotating discovery_id")
            distance = item["distance_cm"]
            if (
                not isinstance(distance, (int, float))
                or isinstance(distance, bool)
                or not math.isfinite(distance)
                or distance <= 0
            ):
                raise ValueError(f"line {line_number}: distance_cm must be positive")
            expected = item["expected_near"]
            if not isinstance(expected, bool):
                raise ValueError(f"line {line_number}: expected_near must be boolean")
            sample_index = item["sample_index"]
            seen_at = item["seen_at_ms"]
            rssi = item["rssi_dbm"]
            if not isinstance(sample_index, int) or isinstance(sample_index, bool) or sample_index < 0:
                raise ValueError(f"line {line_number}: sample_index must be non-negative int")
            if not isinstance(seen_at, int) or isinstance(seen_at, bool) or seen_at < 0:
                raise ValueError(f"line {line_number}: seen_at_ms must be non-negative int")
            if not isinstance(rssi, int) or isinstance(rssi, bool) or not -127 <= rssi <= 20:
                raise ValueError(f"line {line_number}: rssi_dbm outside [-127,20]")
            samples.append(
                Sample(
                    source=source_kind,
                    capture_id=_nonempty_text(item["capture_id"], "capture_id", line_number),
                    dut_tx=dut_tx,
                    dut_rx=dut_rx,
                    environment=_nonempty_text(item["environment"], "environment", line_number),
                    distance_cm=float(distance),
                    orientation=_nonempty_text(item["orientation"], "orientation", line_number),
                    expected_near=expected,
                    firmware_sha=_validated_git_sha(item["firmware_sha"], line_number),
                    board_revision=_nonempty_text(item["board_revision"], "board_revision", line_number),
                    esp_idf_version=_nonempty_text(item["esp_idf_version"], "esp_idf_version", line_number),
                    config_fingerprint=_validated_config_fingerprint(
                        item["config_fingerprint"], line_number
                    ),
                    enclosure=_nonempty_text(item["enclosure"], "enclosure", line_number),
                    sample_index=sample_index,
                    discovery_id=discovery_id,
                    rssi_dbm=rssi,
                    seen_at_ms=seen_at,
                )
            )
    if not samples:
        raise ValueError("corpus contains no samples")
    return samples


def build_captures(samples: Iterable[Sample], minimum_samples: int) -> list[Capture]:
    if minimum_samples <= 0:
        raise ValueError("minimum_samples must be positive")
    grouped: dict[str, list[Sample]] = defaultdict(list)
    for sample in samples:
        grouped[sample.capture_id].append(sample)
    captures: list[Capture] = []
    for capture_id, values in sorted(grouped.items()):
        reference = values[0]
        metadata = (
            reference.source,
            reference.dut_tx,
            reference.dut_rx,
            reference.environment,
            reference.distance_cm,
            reference.orientation,
            reference.expected_near,
            reference.firmware_sha,
            reference.board_revision,
            reference.esp_idf_version,
            reference.config_fingerprint,
            reference.enclosure,
        )
        for value in values[1:]:
            current = (
                value.source,
                value.dut_tx,
                value.dut_rx,
                value.environment,
                value.distance_cm,
                value.orientation,
                value.expected_near,
                value.firmware_sha,
                value.board_revision,
                value.esp_idf_version,
                value.config_fingerprint,
                value.enclosure,
            )
            if current != metadata:
                raise ValueError(f"capture {capture_id}: metadata changes within capture")
        values.sort(key=lambda sample: sample.sample_index)
        indices = [value.sample_index for value in values]
        if len(indices) != len(set(indices)):
            raise ValueError(f"capture {capture_id}: duplicate sample_index")
        if len(values) < minimum_samples:
            raise ValueError(
                f"capture {capture_id}: {len(values)} samples < minimum {minimum_samples}"
            )
        captures.append(Capture(capture_id, reference.expected_near, tuple(values)))
    if not any(c.expected_near for c in captures) or not any(not c.expected_near for c in captures):
        raise ValueError("corpus needs at least one near and one far capture")
    return captures


def gate_capture(capture: Capture, threshold: int, window: int, consecutive: int) -> bool:
    if window <= 0 or consecutive <= 0:
        raise ValueError("window and consecutive must be positive")
    rssi = [sample.rssi_dbm for sample in capture.samples]
    streak = 0
    for end in range(window, len(rssi) + 1):
        median = statistics.median(rssi[end - window : end])
        if median >= threshold:
            streak += 1
            if streak >= consecutive:
                return True
        else:
            streak = 0
    return False


def metrics(captures: list[Capture], threshold: int, window: int, consecutive: int) -> dict[str, object]:
    tp = tn = fp = fn = 0
    for capture in captures:
        predicted = gate_capture(capture, threshold, window, consecutive)
        if capture.expected_near and predicted:
            tp += 1
        elif capture.expected_near:
            fn += 1
        elif predicted:
            fp += 1
        else:
            tn += 1
    positives = tp + fn
    negatives = tn + fp
    fpr = fp / negatives if negatives else 0.0
    fnr = fn / positives if positives else 0.0
    # False-positive pairing is the higher-risk error, so rank it twice as high.
    score = 2.0 * fpr + fnr
    return {
        "threshold_dbm": threshold,
        "window_samples": window,
        "consecutive_windows": consecutive,
        "near_captures": positives,
        "far_captures": negatives,
        "tp": tp,
        "tn": tn,
        "fp": fp,
        "fn": fn,
        "false_positive_rate": round(fpr, 6),
        "false_negative_rate": round(fnr, 6),
        "safety_weighted_score": round(score, 6),
    }


def _breakdown(
    captures: list[Capture],
    threshold: int,
    window: int,
    consecutive: int,
    key: Callable[[Sample], str],
) -> dict[str, dict[str, object]]:
    groups: dict[str, list[Capture]] = defaultdict(list)
    for capture in captures:
        groups[key(capture.samples[0])].append(capture)
    return {
        name: metrics(group, threshold, window, consecutive)
        for name, group in sorted(groups.items())
    }


def coverage(samples: list[Sample], captures: list[Capture]) -> dict[str, object]:
    return {
        "samples": len(samples),
        "captures": len(captures),
        "physical_devices": sorted(
            {sample.dut_tx for sample in samples} | {sample.dut_rx for sample in samples}
        ),
        "environments": sorted({sample.environment for sample in samples}),
        "distances_cm": sorted({sample.distance_cm for sample in samples}),
        "orientations": sorted({sample.orientation for sample in samples}),
        "near_captures": sum(c.expected_near for c in captures),
        "far_captures": sum(not c.expected_near for c in captures),
        "sources": dict(sorted(Counter(sample.source for sample in samples).items())),
        "firmware_shas": sorted({sample.firmware_sha for sample in samples}),
        "board_revisions": sorted({sample.board_revision for sample in samples}),
        "esp_idf_versions": sorted({sample.esp_idf_version for sample in samples}),
        "config_fingerprints": sorted({sample.config_fingerprint for sample in samples}),
        "enclosures": sorted({sample.enclosure for sample in samples}),
    }


def _require_single(summary: dict[str, object], key: str, description: str) -> None:
    values = summary[key]
    if not isinstance(values, list) or len(values) != 1:
        raise ValueError(f"coverage requires exactly one {description} across the corpus")


def enforce_coverage(summary: dict[str, object]) -> None:
    sources = summary["sources"]
    if not isinstance(sources, dict) or set(sources) != {"physical_hil"}:
        raise ValueError("coverage acceptance requires physical_hil samples only")
    if len(summary["physical_devices"]) < 2:  # type: ignore[arg-type]
        raise ValueError("coverage requires at least two physical DUT identifiers")
    if len(summary["environments"]) < 3:  # type: ignore[arg-type]
        raise ValueError("coverage requires at least three environments")
    if len(summary["distances_cm"]) < 3:  # type: ignore[arg-type]
        raise ValueError("coverage requires at least three fixed distances")
    if len(summary["orientations"]) < 2:  # type: ignore[arg-type]
        raise ValueError("coverage requires at least two orientations")
    if int(summary["near_captures"]) < 3 or int(summary["far_captures"]) < 3:
        raise ValueError("coverage requires at least three near and three far captures")
    _require_single(summary, "firmware_shas", "firmware SHA")
    _require_single(summary, "board_revisions", "board revision")
    _require_single(summary, "esp_idf_versions", "ESP-IDF version")
    _require_single(summary, "config_fingerprints", "firmware config fingerprint")
    _require_single(summary, "enclosures", "enclosure definition")


def analyze(samples: list[Sample], captures: list[Capture], top: int) -> dict[str, object]:
    candidates: list[dict[str, object]] = []
    minimum_capture_size = min(len(c.samples) for c in captures)
    for threshold in range(-95, -29):
        for window in (1, 3, 5, 7):
            for consecutive in (1, 2, 3):
                if window * consecutive > minimum_capture_size:
                    continue
                candidates.append(metrics(captures, threshold, window, consecutive))
    candidates.sort(
        key=lambda row: (
            row["safety_weighted_score"],
            row["false_positive_rate"],
            row["false_negative_rate"],
            -int(row["threshold_dbm"]),
            int(row["window_samples"]),
            int(row["consecutive_windows"]),
        )
    )

    enriched: list[dict[str, object]] = []
    for row in candidates[: max(top, 1)]:
        threshold = int(row["threshold_dbm"])
        window = int(row["window_samples"])
        consecutive = int(row["consecutive_windows"])
        enriched_row = dict(row)
        enriched_row["by_environment"] = _breakdown(
            captures, threshold, window, consecutive, lambda sample: sample.environment
        )
        enriched_row["by_orientation"] = _breakdown(
            captures, threshold, window, consecutive, lambda sample: sample.orientation
        )
        enriched.append(enriched_row)

    return {
        "schema_version": 1,
        "promotion": "none",
        "warning": "candidate metrics are not production/HIL evidence by themselves",
        "coverage": coverage(samples, captures),
        "top_candidates": enriched,
    }


def main() -> int:
    args = parse_args()
    if args.require_coverage and not args.require_physical:
        raise SystemExit("--require-coverage requires --require-physical")
    if args.minimum_samples_per_capture <= 0:
        raise SystemExit("--minimum-samples-per-capture must be positive")
    if args.top <= 0:
        raise SystemExit("--top must be positive")
    samples = load_samples(args.corpus, args.require_physical)
    captures = build_captures(samples, args.minimum_samples_per_capture)
    result = analyze(samples, captures, args.top)
    if args.require_coverage:
        enforce_coverage(result["coverage"])  # type: ignore[arg-type]
    encoded = json.dumps(result, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

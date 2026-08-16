#!/usr/bin/env python3
"""Convert ESP32 pairing RSSI HIL log lines into a labeled JSONL corpus.

The firmware emits raw observations only when the HIL-only capture path is used.
This script adds human-controlled experiment labels and immutable build/hardware
provenance without inventing samples or promoting the result into repository
evidence.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

LOG_RE = re.compile(
    r"PAIRING_RSSI\s+alias=(CP-[A-Z2-7]{16})\s+rssi=(-?\d+)\s+seen_ms=(\d+)"
)
GIT_SHA_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def parse_bool(value: str) -> bool:
    normalized = value.strip().lower()
    if normalized in {"1", "true", "yes", "near"}:
        return True
    if normalized in {"0", "false", "no", "far"}:
        return False
    raise argparse.ArgumentTypeError("expected true/false")


def nonempty(value: str, name: str) -> str:
    normalized = value.strip()
    if not normalized:
        raise SystemExit(f"{name} must be non-empty")
    return normalized


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--capture-id", required=True)
    p.add_argument("--dut-tx", required=True)
    p.add_argument("--dut-rx", required=True)
    p.add_argument("--environment", required=True)
    p.add_argument("--distance-cm", required=True, type=float)
    p.add_argument("--orientation", required=True)
    p.add_argument("--expected-near", required=True, type=parse_bool)
    p.add_argument("--firmware-sha", required=True)
    p.add_argument("--board-revision", required=True)
    p.add_argument("--esp-idf-version", required=True)
    p.add_argument("--config-fingerprint", required=True)
    p.add_argument("--enclosure", required=True)
    p.add_argument("--output", required=True, type=Path)
    return p


def main() -> int:
    args = parser().parse_args()
    capture_id = nonempty(args.capture_id, "capture-id")
    dut_tx = nonempty(args.dut_tx, "dut-tx")
    dut_rx = nonempty(args.dut_rx, "dut-rx")
    environment = nonempty(args.environment, "environment")
    orientation = nonempty(args.orientation, "orientation")
    firmware_sha = nonempty(args.firmware_sha, "firmware-sha").lower()
    board_revision = nonempty(args.board_revision, "board-revision")
    esp_idf_version = nonempty(args.esp_idf_version, "esp-idf-version")
    config_fingerprint = nonempty(args.config_fingerprint, "config-fingerprint").lower()
    enclosure = nonempty(args.enclosure, "enclosure")

    if dut_tx == dut_rx:
        raise SystemExit("dut-tx and dut-rx must be different physical devices")
    if args.distance_cm <= 0:
        raise SystemExit("distance-cm must be positive")
    if not GIT_SHA_RE.fullmatch(firmware_sha):
        raise SystemExit("firmware-sha must be a full 40-character hexadecimal Git SHA")
    if not SHA256_RE.fullmatch(config_fingerprint):
        raise SystemExit("config-fingerprint must be a 64-character hexadecimal SHA-256")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    samples = 0
    with args.output.open("a", encoding="utf-8") as target:
        for line in sys.stdin:
            match = LOG_RE.search(line)
            if not match:
                continue
            alias, raw_rssi, raw_seen = match.groups()
            rssi = int(raw_rssi)
            seen_ms = int(raw_seen)
            if not -127 <= rssi <= 20:
                print(f"skip implausible RSSI {rssi}", file=sys.stderr)
                continue
            record = {
                "schema_version": 1,
                "source": "physical_hil",
                "capture_id": capture_id,
                "dut_tx": dut_tx,
                "dut_rx": dut_rx,
                "environment": environment,
                "distance_cm": args.distance_cm,
                "orientation": orientation,
                "expected_near": args.expected_near,
                "firmware_sha": firmware_sha,
                "board_revision": board_revision,
                "esp_idf_version": esp_idf_version,
                "config_fingerprint": config_fingerprint,
                "enclosure": enclosure,
                "sample_index": samples,
                "discovery_id": alias,
                "rssi_dbm": rssi,
                "seen_at_ms": seen_ms,
            }
            target.write(json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n")
            samples += 1

    print(f"captured {samples} physical RSSI samples -> {args.output}")
    return 0 if samples else 2


if __name__ == "__main__":
    raise SystemExit(main())

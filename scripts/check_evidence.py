#!/usr/bin/env python3
"""Fail CI when production evidence claims violate repository policy.

This checker intentionally uses only the Python standard library so the release
truth gate cannot be skipped because a YAML/third-party parser is unavailable.
"""

from __future__ import annotations

import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
STATUS = ROOT / "evidence" / "status.json"

REAL_EVIDENCE_KINDS = {
    "github_actions",
    "manual_real_environment",
    "hardware_manual",
    "real_provider",
    "hil",
    "real_network",
    "soak",
    "fault_injection",
    "release_artifact",
}
NON_PRODUCTION_EVIDENCE_KINDS = {
    "mock",
    "fake_model",
    "stub",
    "simulated_provider",
    "tier1_orchestration",
    "wokwi_simulation",
    "wokwi_unavailable",
}

# Generic hosted CI is valid real evidence for deterministic software gates, but
# it is not sufficient by itself for claims whose acceptance explicitly depends
# on a physical device, a real model/provider, or a long-running soak. Each tuple
# entry is an AND-ed requirement group; a passed gate must contain at least one
# evidence kind from every group.
SENSITIVE_GATE_REQUIRED_GROUPS: dict[str, tuple[frozenset[str], ...]] = {
    "mic_signal_hardware": (
        frozenset({"hardware_manual", "hil"}),
    ),
    "real_voice_e2e": (
        frozenset({"hardware_manual", "hil"}),
        frozenset({"real_provider"}),
    ),
    "real_asr_quality": (
        frozenset({"real_provider"}),
    ),
    "real_tts_quality": (
        frozenset({"real_provider"}),
    ),
    "real_llm_tool_quality": (
        frozenset({"real_provider"}),
    ),
    "prompt_regression_eval": (
        frozenset({"real_provider"}),
    ),
    "real_device_24h_soak": (
        frozenset({"hardware_manual", "hil"}),
        frozenset({"soak"}),
    ),
}


def fail(message: str) -> None:
    print(f"EVIDENCE ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def validate(data: object) -> list[str]:
    if not isinstance(data, dict):
        return ["status root must be an object"]
    if data.get("schema_version") != 1:
        return ["unsupported schema_version"]

    allowed = set(data.get("rules", {}).get("allowed_statuses", []))
    if not allowed:
        return ["allowed_statuses is empty"]

    gates = data.get("gates")
    if not isinstance(gates, dict) or not gates:
        return ["gates must be a non-empty object"]

    problems: list[str] = []
    for name, gate in sorted(gates.items()):
        if not isinstance(gate, dict):
            problems.append(f"{name}: gate must be an object")
            continue
        status = gate.get("status")
        if status not in allowed:
            problems.append(f"{name}: invalid status {status!r}")
            continue
        evidence = gate.get("evidence", [])
        if not isinstance(evidence, list):
            problems.append(f"{name}: evidence must be an array")
            continue

        kinds = {item.get("kind") for item in evidence if isinstance(item, dict)}
        if status == "passed":
            if not evidence:
                problems.append(f"{name}: passed gate has no evidence")
            if kinds & NON_PRODUCTION_EVIDENCE_KINDS:
                problems.append(f"{name}: passed gate contains non-production/mock/simulator evidence")
            if not (kinds & REAL_EVIDENCE_KINDS):
                problems.append(f"{name}: passed gate has no recognized real evidence")
            for required_group in SENSITIVE_GATE_REQUIRED_GROUPS.get(name, ()):
                if not (kinds & required_group):
                    required = ", ".join(sorted(required_group))
                    problems.append(
                        f"{name}: passed gate requires domain evidence kind from {{{required}}}"
                    )

        if status in {"unproven", "blocked", "failed"} and gate.get("production_ready") is True:
            problems.append(f"{name}: non-passed gate cannot set production_ready=true")

    return problems


def main() -> None:
    data = json.loads(STATUS.read_text(encoding="utf-8"))
    problems = validate(data)
    if problems:
        for problem in problems:
            print(f"EVIDENCE ERROR: {problem}", file=sys.stderr)
        raise SystemExit(1)

    gates = data["gates"]
    passed = sum(1 for gate in gates.values() if gate.get("status") == "passed")
    print(f"EVIDENCE PASS: {passed}/{len(gates)} gates currently passed; unproven/partial gates are not promoted")


if __name__ == "__main__":
    main()

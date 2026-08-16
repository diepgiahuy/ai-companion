#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import pathlib
import sys
import unittest

MODULE_PATH = pathlib.Path(__file__).with_name("check_evidence.py")
SPEC = importlib.util.spec_from_file_location("check_evidence", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def status(gates: dict[str, dict[str, object]]) -> dict[str, object]:
    return {
        "schema_version": 1,
        "rules": {"allowed_statuses": ["passed", "partial", "unproven", "blocked", "failed"]},
        "gates": gates,
    }


class EvidenceKindPolicyTest(unittest.TestCase):
    def test_generic_software_gate_accepts_hosted_ci(self) -> None:
        data = status({"software": {"status": "passed", "evidence": [{"kind": "github_actions"}]}})
        self.assertEqual(MODULE.validate(data), [])

    def test_real_asr_cannot_be_promoted_by_generic_ci_only(self) -> None:
        data = status({"real_asr_quality": {"status": "passed", "evidence": [{"kind": "github_actions"}]}})
        problems = MODULE.validate(data)
        self.assertTrue(any("real_provider" in problem for problem in problems), problems)

    def test_real_voice_requires_physical_and_provider_evidence(self) -> None:
        provider_only = status({"real_voice_e2e": {"status": "passed", "evidence": [{"kind": "real_provider"}]}})
        self.assertTrue(any("hardware_manual" in problem or "hil" in problem for problem in MODULE.validate(provider_only)))

        complete = status({"real_voice_e2e": {"status": "passed", "evidence": [{"kind": "real_provider"}, {"kind": "hil"}]}})
        self.assertEqual(MODULE.validate(complete), [])

    def test_device_soak_requires_hardware_and_soak_evidence(self) -> None:
        data = status({"real_device_24h_soak": {"status": "passed", "evidence": [{"kind": "hil"}]}})
        problems = MODULE.validate(data)
        self.assertTrue(any("soak" in problem for problem in problems), problems)

    def test_existing_mic_hardware_kind_is_valid(self) -> None:
        data = status({"mic_signal_hardware": {"status": "passed", "evidence": [{"kind": "hardware_manual"}]}})
        self.assertEqual(MODULE.validate(data), [])


if __name__ == "__main__":
    unittest.main()

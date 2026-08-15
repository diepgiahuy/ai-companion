#!/usr/bin/env python3
import importlib.util
import sys
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "check_pr_contract.py"
spec = importlib.util.spec_from_file_location("check_pr_contract", SCRIPT)
mod = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = mod
assert spec.loader is not None
spec.loader.exec_module(mod)


GOOD = """## Summary

Issue: Refs #96
Risk: L1
Local tested head: 9ca6b7c
Human gate: none

Harden the PR execution record.

## Scope

Changed:
- PR contract metadata.

Not changed:
- Product code.

## Verification

| Oracle | Result | Tested SHA |
|---|---|---|
| PR contract tests | PASS | 9ca6b7c |

## Evidence boundary

Proves:
- PR metadata structure.

Does not prove:
- Product correctness.

## Risk / Rollback

Low risk. Revert the infrastructure commit.

## Remaining

PR blockers: none
Issue remaining: none
Human action: none

Closes #96
"""


class ContractTests(unittest.TestCase):
    def test_good_body_passes(self):
        result = mod.validate(GOOD, "9ca6b7c371df85515f1f48d636e6d9038456ac92")
        self.assertTrue(result.ok, result.errors)

    def test_missing_issue_reference_fails(self):
        result = mod.validate(
            GOOD.replace("Issue: Refs #96\n", "Issue: none\n").replace("Closes #96", "No linked issue")
        )
        self.assertIn("reference at least one GitHub issue", " ".join(result.errors))

    def test_invalid_risk_fails(self):
        result = mod.validate(GOOD.replace("Risk: L1", "Risk: HIGH"))
        self.assertIn("Risk must start", " ".join(result.errors))

    def test_placeholder_fails(self):
        result = mod.validate(GOOD.replace("Human gate: none", "Human gate: {{none|description}}"))
        self.assertIn("template placeholders", " ".join(result.errors))

    def test_pass_requires_tested_sha(self):
        result = mod.validate(GOOD.replace("Local tested head: 9ca6b7c", "Local tested head: not-run"))
        self.assertIn("claims a local PASS", " ".join(result.errors))

    def test_close_with_remaining_work_fails(self):
        result = mod.validate(GOOD.replace("Issue remaining: none", "Issue remaining: physical Tier-3 proof"))
        self.assertIn("Issue remaining", " ".join(result.errors))

    def test_refs_allows_remaining_work(self):
        body = GOOD.replace("Closes #96", "Refs #96").replace(
            "Issue remaining: none", "Issue remaining: physical Tier-3 proof"
        ).replace("Human action: none", "Human action: run trusted HIL")
        result = mod.validate(body)
        self.assertTrue(result.ok, result.errors)

    def test_head_drift_is_warning_not_failure(self):
        result = mod.validate(GOOD, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
        self.assertTrue(result.ok, result.errors)
        self.assertTrue(result.warnings)

    def test_required_section_missing_fails(self):
        body = GOOD.replace("## Evidence boundary", "## Evidence")
        result = mod.validate(body)
        self.assertIn("Missing required section: ## Evidence boundary", result.errors)


if __name__ == "__main__":
    unittest.main()

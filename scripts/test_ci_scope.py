#!/usr/bin/env python3
from __future__ import annotations

import unittest

import ci_scope


class CIScopeTests(unittest.TestCase):
    def test_docs_only_pr_is_prepare_only(self) -> None:
        scope = ci_scope.classify("pull_request", ["README.md", "docs/architecture.md"])
        self.assertEqual(scope, ci_scope.Scope())

    def test_backend_only_pr_uses_fast_backend_oracle(self) -> None:
        scope = ci_scope.classify("pull_request", ["backend/internal/domain/note.go"])
        self.assertTrue(scope.backend)
        self.assertFalse(scope.backend_full)
        self.assertFalse(scope.codeql)
        self.assertFalse(scope.postgres)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_postgres_change_keeps_real_database_oracle(self) -> None:
        scope = ci_scope.classify("pull_request", ["backend/internal/pgstore/settings.go"])
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_ownerweb_go_change_keeps_real_database_oracle(self) -> None:
        scope = ci_scope.classify("pull_request", ["backend/internal/ownerweb/dashboard.go"])
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_ownerweb_html_only_change_stays_fast(self) -> None:
        scope = ci_scope.classify("pull_request", ["backend/internal/ownerweb/dashboard.html"])
        self.assertTrue(scope.backend)
        self.assertFalse(scope.postgres)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_classifier_script_does_not_force_unrelated_hardware_gates(self) -> None:
        scope = ci_scope.classify(
            "pull_request",
            [
                "scripts/ci_scope.py",
                "scripts/test_ci_scope.py",
                "backend/internal/ownerweb/dashboard.go",
            ],
        )
        self.assertEqual(scope.mode, "pr-targeted")
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertFalse(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_firmware_change_uses_host_tests_only_on_pr(self) -> None:
        scope = ci_scope.classify("pull_request", ["components/companion_app/src/app.cpp"])
        self.assertTrue(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.backend)
        self.assertFalse(scope.tier1)

    def test_provisioning_change_skips_firmware_compile_on_pr(self) -> None:
        scope = ci_scope.classify(
            "pull_request", ["components/esp32_provisioning/src/claim_client.cpp"]
        )
        self.assertTrue(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.backend)
        self.assertFalse(scope.tier1)

    def test_software_device_change_skips_tier1_on_pr(self) -> None:
        scope = ci_scope.classify(
            "pull_request", ["host/companion_software_device/main.cpp"]
        )
        self.assertTrue(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_backend_device_cross_boundary_skips_tier1_on_pr(self) -> None:
        scope = ci_scope.classify(
            "pull_request",
            [
                "backend/internal/controlplane/settings.go",
                "components/companion_app/src/app.cpp",
            ],
        )
        self.assertTrue(scope.backend)
        self.assertTrue(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)
        self.assertFalse(scope.backend_full)
        self.assertFalse(scope.codeql)

    def test_backend_and_provisioning_change_skips_tier1_on_pr(self) -> None:
        scope = ci_scope.classify(
            "pull_request",
            [
                "backend/cmd/companiond/owner_auth.go",
                "components/esp32_provisioning/src/claim_client.cpp",
            ],
        )
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertTrue(scope.host)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_explicit_tier1_scenario_waits_for_promotion(self) -> None:
        scope = ci_scope.classify("pull_request", ["testdata/scenarios/config_reconnect.json"])
        self.assertTrue(scope.host)
        self.assertTrue(scope.backend)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)

    def test_ci_control_pr_skips_promotion_only_device_gates(self) -> None:
        scope = ci_scope.classify("pull_request", [".github/workflows/ci.yml"])
        self.assertEqual(scope.mode, "pr-ci-control")
        self.assertTrue(scope.host)
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertFalse(scope.backend_full)
        self.assertFalse(scope.codeql)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)
        self.assertFalse(scope.promotion)

    def test_unknown_pr_change_set_skips_promotion_only_device_gates(self) -> None:
        scope = ci_scope.classify("pull_request", unknown_changes=True)
        self.assertEqual(scope.mode, "pr-fail-safe")
        self.assertTrue(scope.host)
        self.assertTrue(scope.backend)
        self.assertTrue(scope.postgres)
        self.assertFalse(scope.backend_full)
        self.assertFalse(scope.codeql)
        self.assertFalse(scope.protocol)
        self.assertFalse(scope.tier1)
        self.assertFalse(scope.promotion)

    def test_main_push_is_broad_promotion(self) -> None:
        scope = ci_scope.classify("push")
        self.assertEqual(scope.mode, "promotion")
        self.assertTrue(scope.promotion)
        self.assertTrue(scope.backend_full)
        self.assertTrue(scope.codeql)
        self.assertTrue(scope.postgres)
        self.assertTrue(scope.protocol)
        self.assertTrue(scope.tier1)

    def test_manual_run_is_broad_promotion(self) -> None:
        self.assertTrue(ci_scope.classify("workflow_dispatch").promotion)

    def test_schedule_is_security_only(self) -> None:
        scope = ci_scope.classify("schedule")
        self.assertEqual(scope.mode, "scheduled-security")
        self.assertTrue(scope.codeql)
        self.assertFalse(scope.backend)
        self.assertFalse(scope.promotion)

    def test_pr_draft_state_is_not_an_input(self) -> None:
        scope = ci_scope.classify("pull_request", ["backend/internal/domain/note.go"])
        self.assertEqual(scope.mode, "pr-targeted")

    def test_unsupported_event_fails_closed(self) -> None:
        with self.assertRaises(ValueError):
            ci_scope.classify("issue_comment")


if __name__ == "__main__":
    unittest.main()

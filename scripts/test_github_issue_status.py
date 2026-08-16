#!/usr/bin/env python3
from __future__ import annotations

import unittest
from unittest.mock import patch

import github_issue_status as status
import reconcile_github_issue_status as reconcile


def issue(*labels: str, state: str = "open", subissues: int = 0) -> dict:
    return {
        "number": 23,
        "state": state,
        "labels": [{"name": label} for label in labels],
        "sub_issues_summary": {"total": subissues},
    }


class StatusDecisionTests(unittest.TestCase):
    def test_unclaimed_leaf_stays_backlog(self) -> None:
        desired, reason = status.derive_execution_label(issue("enhancement"), [])
        self.assertIsNone(desired)
        self.assertEqual(reason, "unclaimed backlog")
        self.assertEqual(status.derive_project_status(issue("enhancement"), []), ("Backlog", reason))

    def test_blocked_leaf_with_cleared_blockers_promotes_ready(self) -> None:
        desired, reason = status.derive_execution_label(issue("status:blocked"), [])
        self.assertEqual(desired, "status:ready")
        self.assertEqual(reason, "blockers cleared")
        self.assertEqual(status.derive_project_status(issue("status:blocked"), []), ("Ready", reason))

    def test_open_blocker_forces_blocked(self) -> None:
        desired, reason = status.derive_execution_label(issue("status:ready"), [91])
        self.assertEqual(desired, "status:blocked")
        self.assertEqual(reason, "open blockers [91]")
        self.assertEqual(status.derive_project_status(issue("status:ready"), [91]), ("Blocked", reason))

    def test_in_progress_wins_over_stale_statuses_after_blockers_clear(self) -> None:
        sample = issue("status:ready", "status:blocked", "status:in-progress")
        desired, _ = status.derive_execution_label(sample, [])
        self.assertEqual(desired, "status:in-progress")
        normalized = status.normalized_issue_labels(status.issue_label_names(sample), desired)
        self.assertEqual(set(normalized) & status.STATUS_LABELS, {"status:in-progress"})

    def test_closed_issue_has_no_execution_label_and_project_done(self) -> None:
        sample = issue("enhancement", "status:in-progress", state="closed")
        self.assertEqual(status.derive_execution_label(sample, [999])[0], None)
        self.assertEqual(status.derive_project_status(sample, [999])[0], "Done")

    def test_parent_has_no_execution_label_and_project_backlog(self) -> None:
        sample = issue("status:ready", subissues=2)
        self.assertEqual(status.derive_execution_label(sample, [999])[0], None)
        self.assertEqual(status.derive_project_status(sample, [999])[0], "Backlog")

    def test_normalized_labels_preserve_non_status_labels(self) -> None:
        current = {"enhancement", "area:infra", "status:blocked", "status:ready"}
        normalized = status.normalized_issue_labels(current, "status:ready")
        self.assertEqual(
            normalized,
            ["area:infra", "enhancement", "status:ready"],
        )


class ReconcileMutationTests(unittest.TestCase):
    def test_transition_uses_one_atomic_set_labels_request(self) -> None:
        calls: list[tuple[str, str, dict | None]] = []

        def fake_api(path: str, *, method: str = "GET", body=None):
            calls.append((path, method, body))
            return []

        current = {"enhancement", "status:blocked"}
        with patch.object(reconcile, "api", side_effect=fake_api):
            changed = reconcile.set_status(23, current, "status:ready")

        self.assertTrue(changed)
        self.assertEqual(
            calls,
            [
                (
                    "repos/diepgiahuy/ai-companion/issues/23/labels",
                    "PUT",
                    {"labels": ["enhancement", "status:ready"]},
                )
            ],
        )

    def test_converged_state_is_noop_so_self_trigger_cannot_loop(self) -> None:
        with patch.object(reconcile, "api") as api:
            changed = reconcile.set_status(
                23,
                {"enhancement", "status:ready"},
                "status:ready",
            )
        self.assertFalse(changed)
        api.assert_not_called()

    def test_cleared_blocker_reconcile_converges_in_one_mutation(self) -> None:
        sample = issue("enhancement", "status:blocked")
        calls: list[tuple[str, str, dict | None]] = []

        def fake_api(path: str, *, method: str = "GET", body=None):
            calls.append((path, method, body))
            return []

        with (
            patch.object(reconcile, "open_blockers", return_value=[]),
            patch.object(reconcile, "api", side_effect=fake_api),
        ):
            reconcile.reconcile(sample)

        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][1], "PUT")
        self.assertEqual(calls[0][2], {"labels": ["enhancement", "status:ready"]})


if __name__ == "__main__":
    unittest.main()

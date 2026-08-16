#!/usr/bin/env python3
"""Pure execution-status decisions shared by issue labels and GitHub Project sync."""
from __future__ import annotations

from typing import Any, Iterable

STATUS_LABELS = frozenset({"status:ready", "status:in-progress", "status:blocked"})


def issue_label_names(issue: dict[str, Any]) -> set[str]:
    return {label["name"] for label in issue.get("labels", [])}


def has_subissues(issue: dict[str, Any]) -> bool:
    return int(issue.get("sub_issues_summary", {}).get("total", 0)) > 0


def derive_execution_label(
    issue: dict[str, Any], open_blocker_numbers: Iterable[int]
) -> tuple[str | None, str]:
    """Return the one desired compatibility execution label and a stable reason."""
    if "pull_request" in issue:
        return None, "pull request"
    if issue.get("state") == "closed":
        return None, "closed"
    if has_subissues(issue):
        return None, "parent/sub-issue container"

    blockers = sorted({int(number) for number in open_blocker_numbers})
    if blockers:
        return "status:blocked", f"open blockers {blockers}"

    current = issue_label_names(issue)
    if "status:in-progress" in current:
        return "status:in-progress", "explicit in-progress"
    if "status:ready" in current:
        return "status:ready", "explicit ready"
    if "status:blocked" in current:
        return "status:ready", "blockers cleared"
    return None, "unclaimed backlog"


def normalized_issue_labels(current_labels: set[str], desired: str | None) -> list[str]:
    """Return a deterministic label set containing at most one execution label."""
    result = set(current_labels) - STATUS_LABELS
    if desired:
        if desired not in STATUS_LABELS:
            raise ValueError(f"unsupported execution label: {desired}")
        result.add(desired)
    return sorted(result)


def derive_project_status(
    issue: dict[str, Any], open_blocker_numbers: Iterable[int]
) -> tuple[str, str]:
    """Return the Project Status derived from the same execution truth as labels."""
    if issue.get("state") == "closed":
        return "Done", "closed"
    if has_subissues(issue):
        return "Backlog", "parent/sub-issue container"

    desired, reason = derive_execution_label(issue, open_blocker_numbers)
    if desired == "status:blocked":
        return "Blocked", reason
    if desired == "status:in-progress":
        return "In Progress", reason
    if desired == "status:ready":
        return "Ready", reason
    return "Backlog", reason

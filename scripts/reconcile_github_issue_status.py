#!/usr/bin/env python3
"""Keep compatibility execution labels aligned with native GitHub issue state.

Normal issue events reconcile only the changed issue and issues it directly blocks.
`--all` is a drift-repair mode for scheduled/manual recovery. Native issue
relationships remain graph truth; this script never owns dependency metadata.
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from typing import Any

from github_issue_status import (
    STATUS_LABELS,
    derive_execution_label,
    issue_label_names,
    normalized_issue_labels,
)

REPO = os.getenv("PROJECT_REPOSITORY", "diepgiahuy/ai-companion")
TOKEN = os.getenv("REPO_TOKEN") or os.getenv("GH_TOKEN", "")
API_VERSION = "2026-03-10"


def die(message: str) -> None:
    print(f"::error::{message}", file=sys.stderr)
    raise SystemExit(1)


def api(path: str, *, method: str = "GET", body: dict[str, Any] | None = None) -> Any:
    if not TOKEN:
        die("REPO_TOKEN is not configured")
    cmd = [
        "gh", "api",
        "-H", "Accept:application/vnd.github+json",
        "-H", f"X-GitHub-Api-Version:{API_VERSION}",
    ]
    if method != "GET":
        cmd += ["--method", method]
    cmd.append(path)
    stdin = None
    if body is not None:
        cmd += ["--input", "-"]
        stdin = json.dumps(body)
    env = os.environ.copy()
    env["GH_TOKEN"] = TOKEN
    proc = subprocess.run(cmd, input=stdin, text=True, capture_output=True, env=env)
    if proc.returncode != 0:
        die(f"GitHub API failed for {method} {path}: {proc.stderr.strip()}")
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def issue_info(number: int) -> dict[str, Any]:
    return api(f"repos/{REPO}/issues/{number}")


def set_status(number: int, current: set[str], desired: str | None) -> bool:
    """Converge execution labels with one idempotent Set-labels request when needed."""
    desired_status = {desired} if desired else set()
    if STATUS_LABELS & current == desired_status:
        return False

    labels = normalized_issue_labels(current, desired)
    api(
        f"repos/{REPO}/issues/{number}/labels",
        method="PUT",
        body={"labels": labels},
    )
    return True


def dependency_numbers(number: int, relation: str) -> list[int]:
    items = api(f"repos/{REPO}/issues/{number}/dependencies/{relation}?per_page=100") or []
    return [int(item["number"]) for item in items]


def open_blockers(number: int) -> list[int]:
    items = api(f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100") or []
    return [int(item["number"]) for item in items if item.get("state") != "closed"]


def affected_numbers(number: int) -> set[int]:
    # A changed blocker can change the status of each issue it directly blocks.
    return {number, *dependency_numbers(number, "blocking")}


def reconcile(item: dict[str, Any]) -> None:
    if "pull_request" in item:
        return
    number = int(item["number"])
    current = issue_label_names(item)
    blockers = open_blockers(number)
    desired, reason = derive_execution_label(item, blockers)
    changed = set_status(number, current, desired)
    if changed:
        print(f"#{number}: execution status -> {desired or 'none'} ({reason})")


def reconcile_number(number: int) -> None:
    reconcile(issue_info(number))


def reconcile_all() -> None:
    page = 1
    while True:
        batch = api(f"repos/{REPO}/issues?state=all&per_page=100&page={page}") or []
        if not batch:
            break
        for item in batch:
            reconcile(item)
        if len(batch) < 100:
            break
        page += 1


def main() -> None:
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--issue", type=int, help="Reconcile this issue and direct dependents")
    group.add_argument("--all", action="store_true", help="Full drift-repair reconciliation")
    args = parser.parse_args()

    if args.issue is not None:
        targets = sorted(affected_numbers(args.issue))
        for number in targets:
            reconcile_number(number)
        print(f"Reconciled issue-event targets: {targets}")
    else:
        reconcile_all()
        print("Full compatibility execution-label reconciliation complete.")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Keep compatibility execution labels consistent with native GitHub state.

Project Status is the human management surface. These labels remain a compact
machine/connector compatibility signal until every agent can read Project V2
fields directly. Native issue dependencies win over stale labels.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from typing import Any

REPO = os.getenv("PROJECT_REPOSITORY", "diepgiahuy/ai-companion")
TOKEN = os.getenv("REPO_TOKEN") or os.getenv("GH_TOKEN", "")
API_VERSION = "2026-03-10"
STATUS_LABELS = {"status:ready", "status:in-progress", "status:blocked"}
NON_CODING_PARENTS = {2, 7, 18, 21, 91}
DEPENDENCY_OWNED = {9, 17, 99, 100, 102, 103, 104, 106, 114}


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


def issue(number: int) -> dict[str, Any]:
    return api(f"repos/{REPO}/issues/{number}")


def labels(item: dict[str, Any]) -> set[str]:
    return {label["name"] for label in item.get("labels", [])}


def remove_label(number: int, label: str) -> None:
    # REST DELETE returns 200 when the label exists. We only call it after checking.
    api(f"repos/{REPO}/issues/{number}/labels/{label}", method="DELETE")
    print(f"Removed {label} from #{number}")


def add_label(number: int, label: str) -> None:
    api(f"repos/{REPO}/issues/{number}/labels", method="POST", body={"labels": [label]})
    print(f"Added {label} to #{number}")


def set_status(number: int, desired: str | None) -> None:
    item = issue(number)
    current = labels(item)
    for label in sorted(STATUS_LABELS):
        if label != desired and label in current:
            remove_label(number, label)
    if desired and desired not in current:
        add_label(number, desired)


def reconcile_parent(number: int) -> None:
    item = issue(number)
    if item.get("state") == "closed":
        set_status(number, None)
        return
    # Parent/epic issues are never coding work items. Their progress derives from children.
    set_status(number, None)


def reconcile_dependency_owned(number: int) -> None:
    item = issue(number)
    if item.get("state") == "closed":
        set_status(number, None)
        return

    blockers = api(f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100") or []
    open_blockers = [int(blocker["number"]) for blocker in blockers if blocker.get("state") != "closed"]
    current = labels(item)

    if open_blockers:
        set_status(number, "status:blocked")
        print(f"#{number} remains blocked by open issues {open_blockers}")
        return

    # Only automatically promote something that was dependency-blocked. Do not invent
    # Ready for arbitrary backlog/human-gated work merely because it has no blocker.
    if "status:blocked" in current:
        set_status(number, "status:ready")
        print(f"#{number} dependencies are closed; promoted blocked -> ready")


def cleanup_closed_status_labels() -> None:
    page = 1
    while True:
        batch = api(f"repos/{REPO}/issues?state=closed&per_page=100&page={page}") or []
        if not batch:
            break
        for item in batch:
            if "pull_request" in item:
                continue
            current = labels(item)
            for label in sorted(STATUS_LABELS & current):
                remove_label(int(item["number"]), label)
        if len(batch) < 100:
            break
        page += 1


def main() -> None:
    cleanup_closed_status_labels()
    for number in sorted(NON_CODING_PARENTS):
        reconcile_parent(number)
    for number in sorted(DEPENDENCY_OWNED):
        reconcile_dependency_owned(number)
    print("Execution labels are consistent with closed state and native dependencies.")


if __name__ == "__main__":
    main()

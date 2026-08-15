#!/usr/bin/env python3
"""Keep compatibility status labels aligned with live native GitHub relationships.

No issue-number allowlists are used. Closed issues and parent issues carry no
execution label. Open native blockers deterministically own `status:blocked`.
When a previously blocked leaf becomes unblocked, it is promoted to
`status:ready`; otherwise human-selected ready/in-progress state is preserved.
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


def labels(item: dict[str, Any]) -> set[str]:
    return {label["name"] for label in item.get("labels", [])}


def remove_label(number: int, label: str) -> None:
    api(f"repos/{REPO}/issues/{number}/labels/{label}", method="DELETE")


def add_label(number: int, label: str) -> None:
    api(f"repos/{REPO}/issues/{number}/labels", method="POST", body={"labels": [label]})


def set_status(number: int, current: set[str], desired: str | None) -> None:
    for label in sorted(STATUS_LABELS & current):
        if label != desired:
            remove_label(number, label)
    if desired and desired not in current:
        add_label(number, desired)


def open_blockers(number: int) -> list[int]:
    blockers = api(f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100") or []
    return [int(item["number"]) for item in blockers if item.get("state") != "closed"]


def reconcile(item: dict[str, Any]) -> None:
    if "pull_request" in item:
        return
    number = int(item["number"])
    current = labels(item)

    if item.get("state") == "closed":
        set_status(number, current, None)
        return

    if int(item.get("sub_issues_summary", {}).get("total", 0)) > 0:
        set_status(number, current, None)
        return

    blockers = open_blockers(number)
    if blockers:
        set_status(number, current, "status:blocked")
        print(f"#{number}: blocked by open native dependencies {blockers}")
        return

    if "status:blocked" in current:
        set_status(number, current, "status:ready")
        print(f"#{number}: blockers cleared; promoted blocked -> ready")
        return

    if "status:in-progress" in current:
        set_status(number, current, "status:in-progress")
    elif "status:ready" in current:
        set_status(number, current, "status:ready")


def main() -> None:
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
    print("Compatibility execution labels now derive from live issue state and native relationships.")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Idempotently reconcile native GitHub sub-issue and dependency relationships.

Uses only the repository-scoped GITHUB_TOKEN with Issues: write. PROJECT_TOKEN is
intentionally not used here.
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

SUBISSUES: dict[int, list[int]] = {
    2: [5, 6, 7, 8, 9],
    7: [98, 99, 100],
    18: [105, 106],
    91: [101, 102, 103, 104],
}

BLOCKED_BY: dict[int, list[int]] = {
    9: [8],
    17: [3],
    99: [98],
    100: [98, 99, 3],
    102: [101],
    103: [102],
    104: [101, 102, 103, 3],
    106: [105],
    114: [108, 3],
}


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


def reconcile_subissues() -> None:
    for parent, children in SUBISSUES.items():
        current = api(f"repos/{REPO}/issues/{parent}/sub_issues?per_page=100") or []
        existing = {int(item["number"]) for item in current}
        for child in children:
            if child in existing:
                continue
            child_id = int(issue(child)["id"])
            api(
                f"repos/{REPO}/issues/{parent}/sub_issues",
                method="POST",
                body={"sub_issue_id": child_id, "replace_parent": False},
            )
            print(f"Linked sub-issue #{child} under #{parent}")


def reconcile_dependencies() -> None:
    for issue_number, blockers in BLOCKED_BY.items():
        current = api(
            f"repos/{REPO}/issues/{issue_number}/dependencies/blocked_by?per_page=100"
        ) or []
        existing = {int(item["number"]) for item in current}
        for blocker in blockers:
            if blocker in existing:
                continue
            blocker_id = int(issue(blocker)["id"])
            api(
                f"repos/{REPO}/issues/{issue_number}/dependencies/blocked_by",
                method="POST",
                body={"issue_id": blocker_id},
            )
            print(f"Linked dependency #{issue_number} blocked by #{blocker}")


def main() -> None:
    reconcile_subissues()
    reconcile_dependencies()
    print("Native GitHub issue hierarchy/dependencies are reconciled.")


if __name__ == "__main__":
    main()

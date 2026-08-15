#!/usr/bin/env python3
"""One-shot #126 migration from hard-coded control-plane state to native GitHub truth.

This file is intentionally temporary. After the first successful main run is
verified, delete it and remove the migration step/path from project-management.yml.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from typing import Any

OWNER = os.getenv("PROJECT_OWNER", "diepgiahuy")
REPO = os.getenv("PROJECT_REPOSITORY", "diepgiahuy/ai-companion")
TITLE = os.getenv("PROJECT_TITLE", "AI Companion — Production v1")
PROJECT_TOKEN = os.getenv("PROJECT_TOKEN", "")
REPO_TOKEN = os.getenv("REPO_TOKEN", "")
API_VERSION = "2026-03-10"

TARGET_CHILDREN = [101, 102, 103, 122, 123, 104]
TARGET_BLOCKERS = {
    122: {103},
    123: {103},
    104: {3, 103, 122, 123},
}
RETIRED_FIELDS = {"Risk", "Start Date", "Target Date", "Size"}


def die(message: str) -> None:
    print(f"::error::{message}", file=sys.stderr)
    raise SystemExit(1)


def run(cmd: list[str], *, token: str, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env["GH_TOKEN"] = token
    proc = subprocess.run(cmd, input=stdin, text=True, capture_output=True, env=env)
    if proc.returncode != 0:
        die(f"command failed ({proc.returncode}): {' '.join(cmd[:5])}\n{proc.stderr.strip()}")
    return proc


def api(path: str, *, token: str, method: str = "GET", body: dict[str, Any] | None = None) -> Any:
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
    proc = run(cmd, token=token, stdin=stdin)
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def graphql(query: str, variables: dict[str, Any], *, token: str) -> dict[str, Any]:
    proc = run(
        ["gh", "api", "graphql", "--input", "-"],
        token=token,
        stdin=json.dumps({"query": query, "variables": variables}),
    )
    payload = json.loads(proc.stdout)
    if payload.get("errors"):
        die(f"GraphQL error: {json.dumps(payload['errors'], ensure_ascii=False)}")
    return payload["data"]


def issue(number: int) -> dict[str, Any]:
    return api(f"repos/{REPO}/issues/{number}", token=REPO_TOKEN)


def migrate_children() -> None:
    current = api(f"repos/{REPO}/issues/91/sub_issues?per_page=100", token=REPO_TOKEN) or []
    existing = {int(item["number"]) for item in current}
    for child in TARGET_CHILDREN:
        if child in existing:
            continue
        api(
            f"repos/{REPO}/issues/91/sub_issues",
            token=REPO_TOKEN,
            method="POST",
            body={"sub_issue_id": int(issue(child)["id"]), "replace_parent": False},
        )
        print(f"Linked #91 -> child #{child}")


def migrate_dependencies() -> None:
    for number, desired in TARGET_BLOCKERS.items():
        current = api(
            f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100",
            token=REPO_TOKEN,
        ) or []
        by_number = {int(item["number"]): int(item["id"]) for item in current}

        for blocker in sorted(desired - by_number.keys()):
            api(
                f"repos/{REPO}/issues/{number}/dependencies/blocked_by",
                token=REPO_TOKEN,
                method="POST",
                body={"issue_id": int(issue(blocker)["id"])},
            )
            print(f"Added dependency #{number} blocked by #{blocker}")

        for blocker in sorted(by_number.keys() - desired):
            api(
                f"repos/{REPO}/issues/{number}/dependencies/blocked_by/{by_number[blocker]}",
                token=REPO_TOKEN,
                method="DELETE",
            )
            print(f"Removed stale dependency #{number} blocked by #{blocker}")


def retire_project_fields() -> None:
    query = """
    query($login:String!) {
      user(login:$login) {
        projectsV2(first:100) {
          nodes {
            id number title
            fields(first:100) {
              nodes {
                __typename
                ... on ProjectV2Field { id name }
                ... on ProjectV2SingleSelectField { id name }
                ... on ProjectV2IterationField { id name }
              }
            }
            views(first:100) { nodes { name number } }
          }
        }
      }
    }
    """
    user = graphql(query, {"login": OWNER}, token=PROJECT_TOKEN)["user"]
    project = next((item for item in user["projectsV2"]["nodes"] if item["title"] == TITLE), None)
    if project is None:
        die(f"Project {TITLE!r} not found")

    mutation = """
    mutation($fieldId:ID!) {
      deleteProjectV2Field(input:{fieldId:$fieldId}) {
        projectV2Field { id }
      }
    }
    """
    for field in project["fields"]["nodes"]:
        if field.get("name") in RETIRED_FIELDS:
            graphql(mutation, {"fieldId": field["id"]}, token=PROJECT_TOKEN)
            print(f"Retired Project field: {field['name']}")

    if any(view.get("name") == "05 — Product Roadmap" for view in project["views"]["nodes"]):
        print("::warning::05 — Product Roadmap remains UI-only; GitHub exposes no documented view deletion API")


def verify_native_graph() -> None:
    children = api(f"repos/{REPO}/issues/91/sub_issues?per_page=100", token=REPO_TOKEN) or []
    child_numbers = {int(item["number"]) for item in children}
    missing = set(TARGET_CHILDREN) - child_numbers
    if missing:
        die(f"#91 is missing target children: {sorted(missing)}")

    for number, desired in TARGET_BLOCKERS.items():
        current = api(
            f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100",
            token=REPO_TOKEN,
        ) or []
        actual = {int(item["number"]) for item in current}
        if actual != desired:
            die(f"#{number} blockers mismatch: actual={sorted(actual)} desired={sorted(desired)}")
    print("Native #126 graph migration verified.")


def main() -> None:
    if not REPO_TOKEN:
        die("REPO_TOKEN is not configured")
    if not PROJECT_TOKEN:
        die("PROJECT_TOKEN is not configured")
    migrate_children()
    migrate_dependencies()
    retire_project_fields()
    verify_native_graph()


if __name__ == "__main__":
    main()

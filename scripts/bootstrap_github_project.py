#!/usr/bin/env python3
"""Sync GitHub Project item membership and deterministic Status only.

Planning judgment fields (Priority, Wave, Required Evidence) are intentionally
read-only to automation. Native issue relationships and issue state remain the
execution truth; this script never creates fields/views or owns graph metadata.
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from typing import Any

OWNER = os.getenv("PROJECT_OWNER", "diepgiahuy")
REPO = os.getenv("PROJECT_REPOSITORY", "diepgiahuy/ai-companion")
TITLE = os.getenv("PROJECT_TITLE", "AI Companion — Production v1")
PROJECT_TOKEN = os.getenv("PROJECT_TOKEN") or os.getenv("GH_TOKEN", "")
API_VERSION = "2026-03-10"


def die(message: str) -> None:
    print(f"::error::{message}", file=sys.stderr)
    raise SystemExit(1)


def run(cmd: list[str], *, token: str = PROJECT_TOKEN, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env["GH_TOKEN"] = token
    proc = subprocess.run(cmd, input=stdin, text=True, capture_output=True, env=env)
    if proc.returncode != 0:
        die(f"command failed ({proc.returncode}): {' '.join(cmd[:4])}\n{proc.stderr.strip()}")
    return proc


def graphql(query: str, variables: dict[str, Any]) -> dict[str, Any]:
    payload = json.dumps({"query": query, "variables": variables})
    proc = run(["gh", "api", "graphql", "--input", "-"], stdin=payload)
    result = json.loads(proc.stdout)
    if result.get("errors"):
        die(f"GraphQL error: {json.dumps(result['errors'], ensure_ascii=False)}")
    return result["data"]


def api_json(path: str) -> Any:
    proc = run([
        "gh", "api",
        "-H", "Accept:application/vnd.github+json",
        "-H", f"X-GitHub-Api-Version:{API_VERSION}",
        path,
    ])
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def project() -> dict[str, Any]:
    query = """
    query($login:String!) {
      user(login:$login) {
        projectsV2(first:100) { nodes { id number title url } }
      }
    }
    """
    user = graphql(query, {"login": OWNER}).get("user")
    if not user:
        die(f"GitHub user {OWNER!r} not found")
    for item in user["projectsV2"]["nodes"]:
        if item["title"] == TITLE:
            return item
    die(f"Project {TITLE!r} does not exist; steady-state sync will not create control-plane schema")


def snapshot(project_number: int) -> dict[str, Any]:
    query = """
    query($login:String!,$number:Int!) {
      user(login:$login) {
        projectV2(number:$number) {
          id
          fields(first:100) {
            nodes {
              __typename
              ... on ProjectV2SingleSelectField {
                id name options { id name }
              }
              ... on ProjectV2Field { id name dataType }
            }
          }
          items(first:100) {
            nodes {
              id
              content {
                __typename
                ... on Issue { id number state url repository { nameWithOwner } }
              }
            }
          }
        }
      }
    }
    """
    result = graphql(query, {"login": OWNER, "number": project_number})["user"]["projectV2"]
    if result is None:
        die(f"Project #{project_number} not found")
    return result


def status_field(project_snapshot: dict[str, Any]) -> tuple[dict[str, Any], dict[str, str]]:
    for field in project_snapshot["fields"]["nodes"]:
        if field.get("name") == "Status":
            if field.get("__typename") != "ProjectV2SingleSelectField":
                die("Project Status field is not single-select")
            options = {option["name"]: option["id"] for option in field["options"]}
            required = {"Backlog", "Ready", "In Progress", "Blocked", "Done"}
            missing = sorted(required - options.keys())
            if missing:
                die(f"Project Status is missing required options: {missing}")
            return field, options
    die("Project built-in Status field is missing")


def issue_info(number: int) -> dict[str, Any]:
    return api_json(f"repos/{REPO}/issues/{number}")


def dependency_numbers(number: int, relation: str) -> set[int]:
    items = api_json(f"repos/{REPO}/issues/{number}/dependencies/{relation}?per_page=100") or []
    return {int(item["number"]) for item in items}


def open_blockers(number: int) -> list[int]:
    items = api_json(f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100") or []
    return [int(item["number"]) for item in items if item.get("state") != "closed"]


def derive_status(issue: dict[str, Any]) -> str:
    if issue.get("state") == "closed":
        return "Done"
    if open_blockers(int(issue["number"])):
        return "Blocked"
    labels = {label["name"] for label in issue.get("labels", [])}
    if "status:in-progress" in labels:
        return "In Progress"
    if "status:ready" in labels:
        return "Ready"
    return "Backlog"


def ensure_item(project_data: dict[str, Any], project_snapshot: dict[str, Any], issue: dict[str, Any]) -> str:
    number = int(issue["number"])
    for node in project_snapshot["items"]["nodes"]:
        content = node.get("content") or {}
        if (
            content.get("__typename") == "Issue"
            and int(content.get("number", -1)) == number
            and content.get("repository", {}).get("nameWithOwner") == REPO
        ):
            return node["id"]

    mutation = """
    mutation($projectId:ID!,$contentId:ID!) {
      addProjectV2ItemById(input:{projectId:$projectId,contentId:$contentId}) {
        item { id }
      }
    }
    """
    data = graphql(mutation, {"projectId": project_data["id"], "contentId": issue["node_id"]})
    item_id = data["addProjectV2ItemById"]["item"]["id"]
    project_snapshot["items"]["nodes"].append({
        "id": item_id,
        "content": {
            "__typename": "Issue",
            "number": number,
            "repository": {"nameWithOwner": REPO},
        },
    })
    print(f"Added issue #{number} to Project")
    return item_id


def set_status(project_id: str, item_id: str, field_id: str, option_id: str) -> None:
    mutation = """
    mutation($projectId:ID!,$itemId:ID!,$fieldId:ID!,$optionId:String!) {
      updateProjectV2ItemFieldValue(input:{
        projectId:$projectId,itemId:$itemId,fieldId:$fieldId,
        value:{singleSelectOptionId:$optionId}
      }) { projectV2Item { id } }
    }
    """
    graphql(mutation, {
        "projectId": project_id,
        "itemId": item_id,
        "fieldId": field_id,
        "optionId": option_id,
    })


def sync_numbers(project_data: dict[str, Any], numbers: set[int], project_snapshot: dict[str, Any] | None = None) -> None:
    snap = project_snapshot or snapshot(project_data["number"])
    field, options = status_field(snap)
    for number in sorted(numbers):
        issue = issue_info(number)
        if "pull_request" in issue:
            continue
        item_id = ensure_item(project_data, snap, issue)
        desired = derive_status(issue)
        set_status(project_data["id"], item_id, field["id"], options[desired])
        print(f"Synced issue #{number}: Status={desired}")


def list_issue_numbers(state: str) -> set[int]:
    proc = run([
        "gh", "issue", "list", "--repo", REPO, "--state", state,
        "--limit", "200", "--json", "number",
    ])
    return {int(item["number"]) for item in json.loads(proc.stdout or "[]")}


def sync_one_with_dependents(project_data: dict[str, Any], number: int) -> None:
    numbers = {number} | dependency_numbers(number, "blocking")
    sync_numbers(project_data, numbers)


def sync_all(project_data: dict[str, Any]) -> None:
    numbers = list_issue_numbers("open")
    snap = snapshot(project_data["number"])
    for node in snap["items"]["nodes"]:
        content = node.get("content") or {}
        if (
            content.get("__typename") == "Issue"
            and content.get("repository", {}).get("nameWithOwner") == REPO
            and content.get("number") is not None
        ):
            numbers.add(int(content["number"]))
    sync_numbers(project_data, numbers, project_snapshot=snap)


def main() -> None:
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--sync-issue", type=int, help="Sync this issue and direct dependents")
    group.add_argument("--sync-all", action="store_true", help="Full drift-repair sync")
    args = parser.parse_args()

    if not PROJECT_TOKEN:
        die("PROJECT_TOKEN is not configured")
    data = project()
    if args.sync_issue is not None:
        sync_one_with_dependents(data, args.sync_issue)
    else:
        sync_all(data)


if __name__ == "__main__":
    main()

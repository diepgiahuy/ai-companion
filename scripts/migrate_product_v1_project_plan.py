#!/usr/bin/env python3
"""One-shot Product-v1 GitHub issue graph + Project planning-field migration.

This file is intentionally temporary. It is removed after the live migration is
verified so steady-state automation keeps no hard-coded Product-v1 issue map.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from typing import Any, Iterable

from github_issue_status import STATUS_LABELS, derive_project_status, issue_label_names

OWNER = os.getenv("PROJECT_OWNER", "diepgiahuy")
REPO = os.getenv("PROJECT_REPOSITORY", "diepgiahuy/ai-companion")
PROJECT_TITLE = os.getenv("PROJECT_TITLE", "AI Companion — Production v1")
REPO_TOKEN = os.getenv("REPO_TOKEN") or os.getenv("GH_TOKEN", "")
PROJECT_TOKEN = os.getenv("PROJECT_TOKEN", "")
API_VERSION = "2026-03-10"

# Canonical hierarchy added by #210. Existing historical families are preserved.
PARENT_CHILDREN: dict[int, tuple[int, ...]] = {
    2: (195, 199, 201, 203, 205, 206, 208),
    195: (196, 197, 198),
    199: (200,),
    201: (202,),
    203: (204,),
    206: (207,),
}

# Add only these edges if missing. Existing additional historical dependencies stay intact.
DEPENDENCIES_ADD: dict[int, tuple[int, ...]] = {
    197: (196,),
    198: (197,),
    210: (209,),
    9: (8,),
    17: (3,),
    100: (3,),
    104: (3,),
    106: (105,),
    114: (3,),
}

# Rebaseline decision: provider qualification (#18) and model benchmark (#23) are
# independent Wave-1 evidence lanes. #23's issue contract has no #18 prerequisite.
DEPENDENCIES_REMOVE: dict[int, tuple[int, ...]] = {
    23: (18,),
}

# Existing Project options only. No schema/options are created by this migration.
PROJECT_META: dict[int, dict[str, str]] = {
    209: {"Priority": "P0", "Wave": "W0 Foundation", "Required Evidence": "T0 Contract"},
    210: {"Priority": "P0", "Wave": "W0 Foundation", "Required Evidence": "T0 Contract"},
    196: {"Priority": "P0", "Wave": "W0 Foundation", "Required Evidence": "T1 Software Device"},
    200: {"Priority": "P1", "Wave": "W1 Core", "Required Evidence": "T0 Contract"},
    202: {"Priority": "P1", "Wave": "W1 Core", "Required Evidence": "T1 Software Device"},
    204: {"Priority": "P2", "Wave": "W1 Core", "Required Evidence": "T1 Software Device"},
    207: {"Priority": "P1", "Wave": "W1 Core", "Required Evidence": "T1 Software Device"},
    105: {"Priority": "P1", "Wave": "W1 Core", "Required Evidence": "T1 Software Device"},
    23: {"Priority": "P1", "Wave": "W1 Core", "Required Evidence": "T1 Software Device"},
    197: {"Priority": "P1", "Wave": "W2 Product", "Required Evidence": "T1 Software Device"},
    198: {"Priority": "P1", "Wave": "W2 Product", "Required Evidence": "T1 Software Device"},
    208: {"Priority": "P0", "Wave": "W2 Product", "Required Evidence": "T3 Physical HIL"},
}

REQUIRED_PROJECT_OPTIONS: dict[str, set[str]] = {
    "Priority": {"P0", "P1", "P2"},
    "Wave": {"W0 Foundation", "W1 Core", "W2 Product", "W3 Social", "Future"},
    "Required Evidence": {"T0 Contract", "T1 Software Device", "T2 Wokwi", "T3 Physical HIL"},
}

EXECUTION_VERIFY_ISSUES = set(PROJECT_META) | set(PARENT_CHILDREN) | {
    child for children in PARENT_CHILDREN.values() for child in children
} | set(DEPENDENCIES_ADD) | set(DEPENDENCIES_REMOVE)

_PARENT_RE = re.compile(r"/issues/(\d+)$")


class MigrationError(RuntimeError):
    pass


@dataclass(frozen=True)
class GraphAction:
    kind: str
    issue: int
    related: int

    def text(self) -> str:
        if self.kind == "add_parent":
            return f"parent #{self.issue} <- child #{self.related}"
        if self.kind == "add_blocker":
            return f"blocked_by #{self.issue} <- #{self.related}"
        if self.kind == "remove_blocker":
            return f"remove blocked_by #{self.issue} <- #{self.related}"
        return f"{self.kind} #{self.issue} #{self.related}"


def die(message: str) -> None:
    raise MigrationError(message)


def run(cmd: list[str], *, token: str, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env["GH_TOKEN"] = token
    proc = subprocess.run(cmd, input=stdin, text=True, capture_output=True, env=env)
    if proc.returncode != 0:
        safe = " ".join(cmd[:5])
        die(f"command failed ({proc.returncode}): {safe}\n{proc.stderr.strip()}")
    return proc


def repo_api(
    path: str,
    *,
    method: str = "GET",
    body: dict[str, Any] | None = None,
) -> Any:
    if not REPO_TOKEN:
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
    proc = run(cmd, token=REPO_TOKEN, stdin=stdin)
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def graphql(query: str, variables: dict[str, Any]) -> dict[str, Any]:
    if not PROJECT_TOKEN:
        die("PROJECT_TOKEN is not configured")
    payload = json.dumps({"query": query, "variables": variables})
    proc = run(["gh", "api", "graphql", "--input", "-"], token=PROJECT_TOKEN, stdin=payload)
    result = json.loads(proc.stdout)
    if result.get("errors"):
        die(f"GraphQL error: {json.dumps(result['errors'], ensure_ascii=False)}")
    return result["data"]


class RepoState:
    def __init__(self) -> None:
        self.issues: dict[int, dict[str, Any]] = {}
        self.blockers: dict[int, list[dict[str, Any]]] = {}

    def issue(self, number: int, *, refresh: bool = False) -> dict[str, Any]:
        if refresh or number not in self.issues:
            self.issues[number] = repo_api(f"repos/{REPO}/issues/{number}")
        return self.issues[number]

    def blocker_items(self, number: int, *, refresh: bool = False) -> list[dict[str, Any]]:
        if refresh or number not in self.blockers:
            self.blockers[number] = repo_api(
                f"repos/{REPO}/issues/{number}/dependencies/blocked_by?per_page=100"
            ) or []
        return self.blockers[number]

    def blocker_numbers(self, number: int, *, refresh: bool = False) -> set[int]:
        return {int(item["number"]) for item in self.blocker_items(number, refresh=refresh)}

    def open_blocker_numbers(self, number: int, *, refresh: bool = False) -> list[int]:
        return [
            int(item["number"])
            for item in self.blocker_items(number, refresh=refresh)
            if item.get("state") != "closed"
        ]

    def invalidate_issue(self, number: int) -> None:
        self.issues.pop(number, None)
        self.blockers.pop(number, None)


def parent_number(issue: dict[str, Any]) -> int | None:
    url = issue.get("parent_issue_url")
    if not url:
        return None
    match = _PARENT_RE.search(str(url))
    if not match:
        die(f"cannot parse parent_issue_url: {url!r}")
    return int(match.group(1))


def validate_static_plan() -> None:
    child_to_parent: dict[int, int] = {}
    for parent, children in PARENT_CHILDREN.items():
        if parent in children:
            die(f"issue #{parent} cannot be its own sub-issue")
        for child in children:
            previous = child_to_parent.setdefault(child, parent)
            if previous != parent:
                die(f"issue #{child} has two planned parents: #{previous}, #{parent}")

    # Validate the planned hierarchy itself, not only the current live hierarchy.
    for start in PARENT_CHILDREN:
        seen: set[int] = set()
        current = start
        while current in child_to_parent:
            current = child_to_parent[current]
            if current == start or current in seen:
                die(f"planned parent hierarchy contains a cycle at #{start}")
            seen.add(current)

    for issue, blockers in DEPENDENCIES_ADD.items():
        if issue in blockers:
            die(f"issue #{issue} cannot block itself")
    if 208 in DEPENDENCIES_ADD:
        die("#208 must not receive release-blocker fan-in during #210")

    # Planned add-only dependency edges must also be acyclic before live graph checks.
    planned_blockers = {issue: set(blockers) for issue, blockers in DEPENDENCIES_ADD.items()}

    def reaches(start: int, target: int, seen: set[int] | None = None) -> bool:
        if start == target:
            return True
        seen = seen or set()
        if start in seen:
            return False
        seen.add(start)
        return any(reaches(blocker, target, seen) for blocker in planned_blockers.get(start, set()))

    for issue, blockers in planned_blockers.items():
        for blocker in blockers:
            if reaches(blocker, issue):
                die(f"planned dependency graph contains cycle #{issue} <- #{blocker}")

    for number, fields in PROJECT_META.items():
        if set(fields) != set(REQUIRED_PROJECT_OPTIONS):
            die(f"Project metadata for #{number} is incomplete: {sorted(fields)}")
        for field, value in fields.items():
            if value not in REQUIRED_PROJECT_OPTIONS[field]:
                die(f"unsupported planned option for #{number}: {field}={value}")

    if PROJECT_META[197]["Wave"] != "W2 Product" or PROJECT_META[198]["Wave"] != "W2 Product":
        die("#197/#198 must remain in the Wave-2 product lane")
    if PROJECT_META[208]["Required Evidence"] != "T3 Physical HIL":
        die("#208 final real-path gate must retain T3 Physical HIL planning evidence")


def current_parent_chain(state: RepoState, number: int) -> list[int]:
    chain: list[int] = []
    seen: set[int] = set()
    current = number
    while True:
        parent = parent_number(state.issue(current))
        if parent is None:
            return chain
        if parent in seen or parent == number:
            die(f"existing parent cycle encountered from #{number}: {chain + [parent]}")
        seen.add(parent)
        chain.append(parent)
        current = parent


def blocker_reaches(state: RepoState, start: int, target: int, seen: set[int] | None = None) -> bool:
    if start == target:
        return True
    seen = seen or set()
    if start in seen:
        return False
    seen.add(start)
    for blocker in state.blocker_numbers(start):
        if blocker_reaches(state, blocker, target, seen):
            return True
    return False


def build_graph_actions(state: RepoState) -> tuple[list[GraphAction], list[str]]:
    actions: list[GraphAction] = []
    notes: list[str] = []

    for parent, children in PARENT_CHILDREN.items():
        for child in children:
            current = parent_number(state.issue(child))
            if current == parent:
                notes.append(f"NOOP parent #{parent} <- child #{child}")
                continue
            if current is not None:
                die(f"child #{child} already has conflicting parent #{current}; wanted #{parent}")
            if child in current_parent_chain(state, parent):
                die(f"adding parent #{parent} <- child #{child} would create a hierarchy cycle")
            actions.append(GraphAction("add_parent", parent, child))

    for issue, blockers in DEPENDENCIES_ADD.items():
        current = state.blocker_numbers(issue)
        for blocker in blockers:
            if blocker in current:
                notes.append(f"NOOP blocked_by #{issue} <- #{blocker}")
                continue
            if blocker_reaches(state, blocker, issue):
                die(f"adding blocked_by #{issue} <- #{blocker} would create a dependency cycle")
            actions.append(GraphAction("add_blocker", issue, blocker))

    for issue, blockers in DEPENDENCIES_REMOVE.items():
        current = state.blocker_numbers(issue)
        for blocker in blockers:
            if blocker not in current:
                notes.append(f"NOOP remove blocked_by #{issue} <- #{blocker} (absent)")
                continue
            actions.append(GraphAction("remove_blocker", issue, blocker))

    return actions, notes


def apply_graph_actions(state: RepoState, actions: Iterable[GraphAction]) -> None:
    for action in actions:
        if action.kind == "add_parent":
            child_id = int(state.issue(action.related)["id"])
            repo_api(
                f"repos/{REPO}/issues/{action.issue}/sub_issues",
                method="POST",
                body={"sub_issue_id": child_id},
            )
            state.invalidate_issue(action.related)
        elif action.kind == "add_blocker":
            blocker_id = int(state.issue(action.related)["id"])
            repo_api(
                f"repos/{REPO}/issues/{action.issue}/dependencies/blocked_by",
                method="POST",
                body={"issue_id": blocker_id},
            )
            state.blockers.pop(action.issue, None)
        elif action.kind == "remove_blocker":
            blocker_id = int(state.issue(action.related)["id"])
            repo_api(
                f"repos/{REPO}/issues/{action.issue}/dependencies/blocked_by/{blocker_id}",
                method="DELETE",
            )
            state.blockers.pop(action.issue, None)
        else:
            die(f"unknown graph action: {action.kind}")
        print(f"APPLIED {action.text()}")


def verify_graph(state: RepoState) -> None:
    for parent, children in PARENT_CHILDREN.items():
        for child in children:
            actual = parent_number(state.issue(child, refresh=True))
            if actual != parent:
                die(f"parent verification failed for #{child}: got {actual}, want {parent}")

    for issue, blockers in DEPENDENCIES_ADD.items():
        actual = state.blocker_numbers(issue, refresh=True)
        missing = set(blockers) - actual
        if missing:
            die(f"dependency verification failed for #{issue}; missing blockers {sorted(missing)}")

    for issue, blockers in DEPENDENCIES_REMOVE.items():
        actual = state.blocker_numbers(issue, refresh=True)
        stale = set(blockers) & actual
        if stale:
            die(f"dependency verification failed for #{issue}; stale blockers remain {sorted(stale)}")

    print("Native graph verification: PASS")


def project_snapshot() -> dict[str, Any]:
    # Discover only the target Project first. Nesting items/field-values under all 100
    # possible projects can exceed GitHub GraphQL's node-cost limit even for one account.
    discovery = """
    query($login:String!) {
      user(login:$login) {
        projectsV2(first:100) { nodes { id number title } }
      }
    }
    """
    user = graphql(discovery, {"login": OWNER}).get("user")
    if not user:
        die(f"GitHub user {OWNER!r} not found")
    target = next(
        (project for project in user["projectsV2"]["nodes"] if project["title"] == PROJECT_TITLE),
        None,
    )
    if target is None:
        die(f"Project {PROJECT_TITLE!r} not found")

    query = """
    query($login:String!,$number:Int!) {
      user(login:$login) {
        projectV2(number:$number) {
          id number title
          fields(first:20) {
            totalCount
            nodes {
              __typename
              ... on ProjectV2SingleSelectField {
                id name options { id name }
              }
            }
          }
          items(first:100) {
            totalCount
            nodes {
              id
              content {
                __typename
                ... on Issue { id number state repository { nameWithOwner } }
              }
              fieldValues(first:20) {
                nodes {
                  __typename
                  ... on ProjectV2ItemFieldSingleSelectValue {
                    name
                    field { ... on ProjectV2SingleSelectField { name } }
                  }
                }
              }
            }
          }
        }
      }
    }
    """
    snapshot = graphql(query, {"login": OWNER, "number": int(target["number"])})["user"]["projectV2"]
    if snapshot is None:
        die(f"Project #{target['number']} disappeared during snapshot")
    if int(snapshot["fields"]["totalCount"]) > len(snapshot["fields"]["nodes"]):
        die("Project has more than 20 fields; refuse a partial planning-field snapshot")
    if int(snapshot["items"]["totalCount"]) > len(snapshot["items"]["nodes"]):
        die("Project has more than 100 items; refuse a partial migration snapshot")
    return snapshot


def project_field_map(snapshot: dict[str, Any]) -> dict[str, dict[str, Any]]:
    fields: dict[str, dict[str, Any]] = {}
    for field in snapshot["fields"]["nodes"]:
        if field.get("__typename") == "ProjectV2SingleSelectField" and field.get("name"):
            fields[field["name"]] = field
    for name, required in REQUIRED_PROJECT_OPTIONS.items():
        field = fields.get(name)
        if field is None:
            die(f"Project is missing required single-select field {name!r}")
        actual = {option["name"] for option in field.get("options", [])}
        missing = required - actual
        if missing:
            die(f"Project field {name!r} is missing options: {sorted(missing)}")
    return fields


def project_item_map(snapshot: dict[str, Any]) -> dict[int, dict[str, Any]]:
    result: dict[int, dict[str, Any]] = {}
    for item in snapshot["items"]["nodes"]:
        content = item.get("content") or {}
        if (
            content.get("__typename") == "Issue"
            and content.get("repository", {}).get("nameWithOwner") == REPO
            and content.get("number") is not None
        ):
            result[int(content["number"])] = item
    return result


def item_single_select_values(item: dict[str, Any]) -> dict[str, str]:
    values: dict[str, str] = {}
    for node in item.get("fieldValues", {}).get("nodes", []):
        if node.get("__typename") != "ProjectV2ItemFieldSingleSelectValue":
            continue
        field = node.get("field") or {}
        if field.get("name") and node.get("name"):
            values[str(field["name"])] = str(node["name"])
    return values


def add_project_item(project_id: str, issue: dict[str, Any]) -> str:
    mutation = """
    mutation($projectId:ID!,$contentId:ID!) {
      addProjectV2ItemById(input:{projectId:$projectId,contentId:$contentId}) {
        item { id }
      }
    }
    """
    data = graphql(mutation, {"projectId": project_id, "contentId": issue["node_id"]})
    return data["addProjectV2ItemById"]["item"]["id"]


def set_project_field(project_id: str, item_id: str, field_id: str, option_id: str) -> None:
    mutation = """
    mutation($projectId:ID!,$itemId:ID!,$fieldId:ID!,$optionId:String!) {
      updateProjectV2ItemFieldValue(input:{
        projectId:$projectId,itemId:$itemId,fieldId:$fieldId,
        value:{singleSelectOptionId:$optionId}
      }) { projectV2Item { id } }
    }
    """
    graphql(
        mutation,
        {
            "projectId": project_id,
            "itemId": item_id,
            "fieldId": field_id,
            "optionId": option_id,
        },
    )


def project_plan(state: RepoState, snapshot: dict[str, Any]) -> list[dict[str, Any]]:
    fields = project_field_map(snapshot)
    items = project_item_map(snapshot)
    plan: list[dict[str, Any]] = []
    for number, desired_fields in sorted(PROJECT_META.items()):
        item = items.get(number)
        current = item_single_select_values(item) if item else {}
        for field_name, desired in desired_fields.items():
            if current.get(field_name) == desired:
                continue
            plan.append(
                {
                    "issue": number,
                    "item_id": item.get("id") if item else None,
                    "field": field_name,
                    "before": current.get(field_name),
                    "after": desired,
                    "field_id": fields[field_name]["id"],
                    "option_id": next(
                        option["id"]
                        for option in fields[field_name]["options"]
                        if option["name"] == desired
                    ),
                }
            )
        if item is None:
            # Prove the referenced issue exists before any Project write.
            state.issue(number)
    return plan


def apply_project_plan(state: RepoState, snapshot: dict[str, Any], plan: list[dict[str, Any]]) -> None:
    project_id = snapshot["id"]
    item_ids = {number: item["id"] for number, item in project_item_map(snapshot).items()}
    for change in plan:
        number = int(change["issue"])
        item_id = item_ids.get(number)
        if item_id is None:
            item_id = add_project_item(project_id, state.issue(number))
            item_ids[number] = item_id
            print(f"APPLIED add Project item #{number}")
        set_project_field(
            project_id,
            item_id,
            str(change["field_id"]),
            str(change["option_id"]),
        )
        print(
            f"APPLIED Project #{number} {change['field']}: "
            f"{change['before']!r} -> {change['after']!r}"
        )


def verify_project_planning(snapshot: dict[str, Any]) -> None:
    project_field_map(snapshot)
    items = project_item_map(snapshot)
    for number, expected in PROJECT_META.items():
        item = items.get(number)
        if item is None:
            die(f"Project verification failed: missing item #{number}")
        actual = item_single_select_values(item)
        for field, value in expected.items():
            if actual.get(field) != value:
                die(
                    f"Project verification failed for #{number} {field}: "
                    f"got {actual.get(field)!r}, want {value!r}"
                )
    print("Project planning-field verification: PASS")


def verify_execution_state(state: RepoState, snapshot: dict[str, Any]) -> None:
    items = project_item_map(snapshot)
    for number in sorted(EXECUTION_VERIFY_ISSUES):
        issue = state.issue(number, refresh=True)
        blockers = state.open_blocker_numbers(number, refresh=True)
        desired_project, reason = derive_project_status(issue, blockers)
        labels = issue_label_names(issue)
        execution = labels & STATUS_LABELS
        if len(execution) > 1:
            die(f"#{number} has contradictory execution labels: {sorted(execution)}")
        item = items.get(number)
        if item is None:
            die(f"execution verification failed: Project item #{number} missing")
        actual_project = item_single_select_values(item).get("Status")
        if actual_project != desired_project:
            die(
                f"Project Status mismatch for #{number}: got {actual_project!r}, "
                f"want {desired_project!r} ({reason})"
            )
        if execution == {"status:ready"} and blockers:
            die(f"Ready issue #{number} still has open blockers {blockers}")

    for parent in PARENT_CHILDREN:
        issue = state.issue(parent, refresh=True)
        execution = issue_label_names(issue) & STATUS_LABELS
        if execution:
            die(f"feature parent #{parent} must be non-executable; found {sorted(execution)}")

    # Live smoke inherited from #209: after stale #18 -> #23 is removed, #23 is a Ready leaf.
    issue23 = state.issue(23, refresh=True)
    open23 = state.open_blocker_numbers(23, refresh=True)
    if open23:
        die(f"#23 still has open blockers after graph normalization: {open23}")
    if "status:ready" not in issue_label_names(issue23):
        die("#23 did not converge to status:ready after stale blocker removal")

    print("Execution-label / Project-Status verification: PASS")


def print_graph_plan(actions: list[GraphAction], notes: list[str]) -> None:
    print("## Native graph dry-run")
    for note in notes:
        print(note)
    for action in actions:
        print(f"PLAN {action.text()}")
    if not actions:
        print("PLAN no native graph mutations required")


def print_project_plan(plan: list[dict[str, Any]]) -> None:
    print("## Project planning-field dry-run")
    if not plan:
        print("PLAN no Project planning-field mutations required")
        return
    for change in plan:
        print(
            f"PLAN Project #{change['issue']} {change['field']}: "
            f"{change['before']!r} -> {change['after']!r}"
        )


def main() -> int:
    parser = argparse.ArgumentParser()
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--dry-run", action="store_true")
    modes.add_argument("--apply", action="store_true")
    modes.add_argument("--verify", action="store_true")
    parser.add_argument(
        "--skip-project",
        action="store_true",
        help="Read/validate native issue graph only; useful for untrusted PR validation without PROJECT_TOKEN.",
    )
    parser.add_argument(
        "--check-execution",
        action="store_true",
        help="With --verify, also prove execution labels and Project Status after steady-state reconciliation.",
    )
    args = parser.parse_args()

    try:
        validate_static_plan()
        state = RepoState()

        if args.verify:
            verify_graph(state)
            if not args.skip_project:
                snapshot = project_snapshot()
                verify_project_planning(snapshot)
                if args.check_execution:
                    verify_execution_state(state, snapshot)
            print("Product-v1 migration verification: PASS")
            return 0

        actions, notes = build_graph_actions(state)
        print_graph_plan(actions, notes)

        snapshot: dict[str, Any] | None = None
        pplan: list[dict[str, Any]] = []
        if not args.skip_project:
            snapshot = project_snapshot()
            pplan = project_plan(state, snapshot)
            print_project_plan(pplan)

        if args.dry_run:
            print("Product-v1 migration dry-run: PASS")
            return 0

        # All graph + Project validation above completes before the first write.
        apply_graph_actions(state, actions)
        verify_graph(state)
        if snapshot is not None:
            apply_project_plan(state, snapshot, pplan)
            verify_project_planning(project_snapshot())
        print("Product-v1 migration apply: PASS")
        return 0
    except MigrationError as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

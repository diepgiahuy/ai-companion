#!/usr/bin/env python3
"""Idempotently bootstrap/sync the AI Companion GitHub Project.

Requires:
  GH_TOKEN / PROJECT_TOKEN: classic PAT with `project` scope.
  REPO_TOKEN: repository-scoped GitHub Actions token (optional for bootstrap;
              required for hierarchy/dependency reconciliation).
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
REPO_OWNER, REPO_NAME = REPO.split("/", 1)
TITLE = os.getenv("PROJECT_TITLE", "AI Companion — Production v1")
PROJECT_TOKEN = os.getenv("PROJECT_TOKEN") or os.getenv("GH_TOKEN", "")
REPO_TOKEN = os.getenv("REPO_TOKEN", "")
API_VERSION = "2026-03-10"
USER_DATABASE_ID = os.getenv("PROJECT_OWNER_DATABASE_ID", "30974088")

STATUS_OPTIONS = [
    ("Backlog", "GRAY", "Not ready or not selected for execution."),
    ("Ready", "GREEN", "Definition of Ready is satisfied and the work can be picked."),
    ("In Progress", "YELLOW", "Actively owned implementation or qualification work."),
    ("In Review", "PURPLE", "Implementation is in PR/review/exact-head verification."),
    ("Blocked", "RED", "Cannot proceed until an explicit dependency or human gate clears."),
    ("Done", "BLUE", "Issue outcome is complete/closed."),
]

FIELD_SPECS = {
    "Priority": [
        ("P0", "RED", "Production-v1 blocker or critical path."),
        ("P1", "ORANGE", "Important v1 product capability, not a core blocker."),
        ("P2", "GRAY", "Post-v1, optional, or future enhancement."),
    ],
    "Wave": [
        ("W0 Foundation", "PURPLE", "Repository/process/platform foundation."),
        ("W1 Core", "BLUE", "Core production path."),
        ("W2 Product", "GREEN", "Integrated product experience and qualification."),
        ("W3 Social", "YELLOW", "Relationship/social interaction capability."),
        ("Future", "GRAY", "Not ready for Production-v1 execution."),
    ],
    "Size": [
        ("XS", "GRAY", "Tiny docs/config/mechanical change."),
        ("S", "GREEN", "Small focused implementation."),
        ("M", "YELLOW", "Normal coherent implementation issue."),
        ("L", "RED", "Parent/mega scope; split before coding."),
    ],
    "Risk": [
        ("L1", "GREEN", "Low-risk localized change."),
        ("L2", "YELLOW", "Meaningful runtime/integration risk."),
        ("L3", "RED", "Security/data/firmware/physical/release-critical risk."),
    ],
    "Required Evidence": [
        ("T0 Contract", "GRAY", "Deterministic contract/static/unit proof."),
        ("T1 Software Device", "BLUE", "Software-device/integration/provider proof."),
        ("T2 Wokwi", "YELLOW", "Simulator/target behavior where meaningful."),
        ("T3 Physical HIL", "RED", "Trusted physical hardware proof required."),
    ],
}

# priority, wave, size, risk, evidence
ITEM_META: dict[int, tuple[str, str, str, str, str]] = {
    2: ("P1", "W3 Social", "L", "L3", "T3 Physical HIL"),
    3: ("P0", "W1 Core", "M", "L3", "T3 Physical HIL"),
    7: ("P1", "W3 Social", "L", "L3", "T3 Physical HIL"),
    8: ("P0", "W1 Core", "M", "L3", "T3 Physical HIL"),
    9: ("P1", "W2 Product", "L", "L3", "T3 Physical HIL"),
    17: ("P0", "W2 Product", "M", "L3", "T3 Physical HIL"),
    18: ("P0", "W1 Core", "L", "L3", "T1 Software Device"),
    21: ("P0", "W0 Foundation", "L", "L3", "T3 Physical HIL"),
    23: ("P0", "W1 Core", "M", "L2", "T1 Software Device"),
    91: ("P0", "W1 Core", "L", "L3", "T3 Physical HIL"),
    98: ("P1", "W3 Social", "M", "L3", "T1 Software Device"),
    99: ("P1", "W3 Social", "M", "L3", "T1 Software Device"),
    100: ("P1", "W3 Social", "M", "L3", "T3 Physical HIL"),
    101: ("P0", "W1 Core", "M", "L3", "T1 Software Device"),
    102: ("P0", "W1 Core", "M", "L3", "T1 Software Device"),
    103: ("P0", "W1 Core", "M", "L3", "T1 Software Device"),
    104: ("P0", "W1 Core", "M", "L3", "T3 Physical HIL"),
    105: ("P0", "W1 Core", "M", "L3", "T1 Software Device"),
    106: ("P0", "W1 Core", "M", "L3", "T1 Software Device"),
    107: ("P0", "W1 Core", "M", "L2", "T1 Software Device"),
    108: ("P0", "W2 Product", "M", "L3", "T1 Software Device"),
    114: ("P0", "W2 Product", "M", "L3", "T3 Physical HIL"),
    115: ("P1", "W0 Foundation", "S", "L1", "T0 Contract"),
}

# Native issue hierarchy. A sub-issue may have only one parent.
SUBISSUES: dict[int, list[int]] = {
    2: [5, 6, 7, 8, 9],
    7: [98, 99, 100],
    18: [105, 106],
    91: [101, 102, 103, 104],
}

# Native execution dependencies.
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

VIEWS = [
    ("01 — Command Center", "table", "is:issue -status:Done"),
    ("02 — Execution", "board", "is:issue -status:Backlog -status:Done"),
    ("03 — Critical Path", "table", "is:issue priority:P0 -status:Done"),
    ("04 — Physical / Evidence", "table", 'is:issue required-evidence:"T3 Physical HIL" -status:Done'),
    ("05 — Product Roadmap", "roadmap", "is:issue"),
]


def die(message: str) -> None:
    print(f"::error::{message}", file=sys.stderr)
    raise SystemExit(1)


def run(cmd: list[str], *, token: str | None = None, stdin: str | None = None,
        check: bool = True) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    if token is not None:
        env["GH_TOKEN"] = token
    proc = subprocess.run(cmd, input=stdin, text=True, capture_output=True, env=env)
    if check and proc.returncode != 0:
        safe = " ".join(cmd[:4])
        die(f"command failed ({proc.returncode}): {safe}\n{proc.stderr.strip()}")
    return proc


def gh_json(args: list[str], *, token: str = PROJECT_TOKEN) -> Any:
    proc = run(["gh", *args], token=token)
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def graphql(query: str, variables: dict[str, Any], *, token: str = PROJECT_TOKEN) -> dict[str, Any]:
    payload = json.dumps({"query": query, "variables": variables})
    proc = run(["gh", "api", "graphql", "--input", "-"], token=token, stdin=payload)
    result = json.loads(proc.stdout)
    if result.get("errors"):
        die(f"GraphQL error: {json.dumps(result['errors'], ensure_ascii=False)}")
    return result["data"]


def api_json(path: str, *, method: str = "GET", body: dict[str, Any] | None = None,
             token: str = PROJECT_TOKEN, versioned: bool = False, check: bool = True) -> Any:
    cmd = ["gh", "api"]
    if method != "GET":
        cmd += ["--method", method]
    if versioned:
        cmd += ["-H", f"X-GitHub-Api-Version:{API_VERSION}", "-H", "Accept:application/vnd.github+json"]
    cmd.append(path)
    stdin = None
    if body is not None:
        cmd += ["--input", "-"]
        stdin = json.dumps(body)
    proc = run(cmd, token=token, stdin=stdin, check=check)
    if not check and proc.returncode != 0:
        return {"_error": proc.stderr.strip(), "_returncode": proc.returncode}
    raw = proc.stdout.strip()
    return json.loads(raw) if raw else None


def owner_and_project() -> tuple[str, dict[str, Any]]:
    query = """
    query($login:String!) {
      user(login:$login) {
        id
        projectsV2(first:100) { nodes { id number title url } }
      }
    }
    """
    data = graphql(query, {"login": OWNER})
    user = data.get("user")
    if not user:
        die(f"GitHub user {OWNER!r} not found")
    for project in user["projectsV2"]["nodes"]:
        if project["title"] == TITLE:
            return user["id"], project

    mutation = """
    mutation($ownerId:ID!, $title:String!) {
      createProjectV2(input:{ownerId:$ownerId,title:$title}) {
        projectV2 { id number title url }
      }
    }
    """
    created = graphql(mutation, {"ownerId": user["id"], "title": TITLE})
    project = created["createProjectV2"]["projectV2"]
    print(f"Created Project #{project['number']}: {project['url']}")
    return user["id"], project


def configure_project(project: dict[str, Any]) -> None:
    mutation = """
    mutation($projectId:ID!, $description:String!, $readme:String!) {
      updateProjectV2(input:{
        projectId:$projectId,
        shortDescription:$description,
        readme:$readme
      }) { projectV2 { id } }
    }
    """
    readme = (
        "Management surface for AI Companion Production v1.\n\n"
        "**Truth boundaries:** Project = priority/status/WIP; Issue = requirement; "
        "PR = execution record; Checks = automated proof; `evidence/status.json` = promoted claims; "
        "`main` code = product truth; README/architecture = documented truth.\n\n"
        "Default execution: one lead, at most two active coding lanes; physical/provider proof is never "
        "inferred from mocks, compile success, or simulation."
    )
    graphql(mutation, {
        "projectId": project["id"],
        "description": "Production-v1 priority, execution, evidence and physical-gate management.",
        "readme": readme,
    })


def project_snapshot(project_number: int) -> dict[str, Any]:
    query = """
    query($login:String!, $number:Int!) {
      user(login:$login) {
        projectV2(number:$number) {
          id
          repositories(first:100) { nodes { id nameWithOwner } }
          fields(first:100) {
            nodes {
              __typename
              ... on ProjectV2Field { id databaseId name dataType }
              ... on ProjectV2SingleSelectField {
                id databaseId name dataType
                options { id name color description }
              }
              ... on ProjectV2IterationField { id databaseId name dataType }
            }
          }
          views(first:100) { nodes { id number name layout filter } }
          items(first:100) {
            nodes {
              id
              content {
                __typename
                ... on Issue { id number state url repository { nameWithOwner } }
                ... on PullRequest { id number state url repository { nameWithOwner } }
              }
            }
          }
        }
      }
    }
    """
    data = graphql(query, {"login": OWNER, "number": project_number})
    project = data["user"]["projectV2"]
    if project is None:
        die(f"Project #{project_number} disappeared")
    return project


def link_repository(project: dict[str, Any], snapshot: dict[str, Any]) -> None:
    if any(n["nameWithOwner"] == REPO for n in snapshot["repositories"]["nodes"]):
        return
    repo_query = """
    query($owner:String!, $name:String!) { repository(owner:$owner,name:$name) { id } }
    """
    repo = graphql(repo_query, {"owner": REPO_OWNER, "name": REPO_NAME})["repository"]
    if not repo:
        die(f"repository {REPO} not found")
    mutation = """
    mutation($projectId:ID!, $repositoryId:ID!) {
      linkProjectV2ToRepository(input:{projectId:$projectId,repositoryId:$repositoryId}) {
        repository { id }
      }
    }
    """
    graphql(mutation, {"projectId": project["id"], "repositoryId": repo["id"]})
    print(f"Linked {REPO} to Project #{project['number']}")


def desired_option_inputs(
    desired: list[tuple[str, str, str]], existing: list[dict[str, Any]], *, status: bool = False
) -> list[dict[str, Any]]:
    by_name = {o["name"]: o for o in existing}
    result = []
    for name, color, description in desired:
        old = by_name.get(name)
        if status and name == "Backlog" and old is None:
            old = by_name.get("Todo")
        item: dict[str, Any] = {"name": name, "color": color, "description": description}
        if old:
            item["id"] = old["id"]
        result.append(item)
    return result


def update_single_select(field: dict[str, Any], desired: list[tuple[str, str, str]], *, status: bool = False) -> None:
    wanted_names = [x[0] for x in desired]
    current_names = [o["name"] for o in field.get("options", [])]
    if current_names == wanted_names:
        return
    mutation = """
    mutation($fieldId:ID!, $options:[ProjectV2SingleSelectFieldOptionInput!]) {
      updateProjectV2Field(input:{fieldId:$fieldId,singleSelectOptions:$options}) {
        projectV2Field { ... on ProjectV2SingleSelectField { id name options { id name } } }
      }
    }
    """
    graphql(mutation, {
        "fieldId": field["id"],
        "options": desired_option_inputs(desired, field.get("options", []), status=status),
    })
    print(f"Updated field options: {field['name']}")


def create_field(project_id: str, name: str, data_type: str,
                 options: list[tuple[str, str, str]] | None = None) -> None:
    mutation = """
    mutation($projectId:ID!, $name:String!, $dataType:ProjectV2CustomFieldType!,
             $options:[ProjectV2SingleSelectFieldOptionInput!]) {
      createProjectV2Field(input:{
        projectId:$projectId,name:$name,dataType:$dataType,singleSelectOptions:$options
      }) { projectV2Field { id } }
    }
    """
    variables: dict[str, Any] = {"projectId": project_id, "name": name, "dataType": data_type, "options": None}
    if options is not None:
        variables["options"] = [
            {"name": n, "color": c, "description": d} for n, c, d in options
        ]
    graphql(mutation, variables)
    print(f"Created field: {name}")


def ensure_fields(project: dict[str, Any], snapshot: dict[str, Any]) -> dict[str, Any]:
    fields = {f.get("name"): f for f in snapshot["fields"]["nodes"] if f.get("name")}
    status = fields.get("Status")
    if not status or status["__typename"] != "ProjectV2SingleSelectField":
        die("Project built-in Status field is missing or not single-select")
    update_single_select(status, STATUS_OPTIONS, status=True)

    for name, options in FIELD_SPECS.items():
        field = fields.get(name)
        if field is None:
            create_field(project["id"], name, "SINGLE_SELECT", options)
        elif field["__typename"] != "ProjectV2SingleSelectField":
            die(f"Existing field {name!r} has wrong type")
        else:
            update_single_select(field, options)

    for name in ("Start Date", "Target Date"):
        field = fields.get(name)
        if field is None:
            create_field(project["id"], name, "DATE")
        elif field.get("dataType") != "DATE":
            die(f"Existing field {name!r} has wrong type")

    return project_snapshot(project["number"])


def ensure_views(project: dict[str, Any], snapshot: dict[str, Any]) -> None:
    existing = {v["name"] for v in snapshot["views"]["nodes"]}
    database_fields = [
        f["databaseId"] for f in snapshot["fields"]["nodes"]
        if f.get("databaseId") is not None and f.get("name") in {
            "Status", "Priority", "Wave", "Size", "Risk", "Required Evidence",
            "Start Date", "Target Date", "Assignees", "Linked pull requests", "Parent issue",
        }
    ]
    for name, layout, filter_query in VIEWS:
        if name in existing:
            continue
        body: dict[str, Any] = {"name": name, "layout": layout, "filter": filter_query}
        if layout != "roadmap" and database_fields:
            body["visible_fields"] = database_fields
        response = api_json(
            f"users/{USER_DATABASE_ID}/projectsV2/{project['number']}/views",
            method="POST", body=body, versioned=True
        )
        value = response.get("value", response) if isinstance(response, dict) else {}
        print(f"Created view: {value.get('name', name)}")


def issue_info(number: int, *, token: str = PROJECT_TOKEN) -> dict[str, Any]:
    return api_json(f"repos/{REPO}/issues/{number}", token=token)


def ensure_item(project: dict[str, Any], snapshot: dict[str, Any], number: int) -> str:
    for node in snapshot["items"]["nodes"]:
        content = node.get("content")
        if content and content.get("__typename") == "Issue" and content.get("number") == number \
                and content.get("repository", {}).get("nameWithOwner") == REPO:
            return node["id"]
    issue = issue_info(number)
    mutation = """
    mutation($projectId:ID!, $contentId:ID!) {
      addProjectV2ItemById(input:{projectId:$projectId,contentId:$contentId}) {
        item { id }
      }
    }
    """
    data = graphql(mutation, {"projectId": project["id"], "contentId": issue["node_id"]})
    print(f"Added issue #{number} to Project")
    return data["addProjectV2ItemById"]["item"]["id"]


def field_maps(snapshot: dict[str, Any]) -> tuple[dict[str, dict[str, Any]], dict[str, dict[str, str]]]:
    fields: dict[str, dict[str, Any]] = {}
    options: dict[str, dict[str, str]] = {}
    for field in snapshot["fields"]["nodes"]:
        name = field.get("name")
        if not name:
            continue
        fields[name] = field
        if field["__typename"] == "ProjectV2SingleSelectField":
            options[name] = {o["name"]: o["id"] for o in field["options"]}
    return fields, options


def set_select(project_id: str, item_id: str, field: dict[str, Any], option_id: str) -> None:
    mutation = """
    mutation($projectId:ID!, $itemId:ID!, $fieldId:ID!, $optionId:String!) {
      updateProjectV2ItemFieldValue(input:{
        projectId:$projectId,itemId:$itemId,fieldId:$fieldId,
        value:{singleSelectOptionId:$optionId}
      }) { projectV2Item { id } }
    }
    """
    graphql(mutation, {
        "projectId": project_id, "itemId": item_id,
        "fieldId": field["id"], "optionId": option_id,
    })


def derive_status(issue: dict[str, Any]) -> str:
    if issue.get("state") == "closed":
        return "Done"
    labels = {label["name"] for label in issue.get("labels", [])}
    if "status:blocked" in labels:
        return "Blocked"
    if "status:in-progress" in labels:
        return "In Progress"
    if "status:ready" in labels:
        return "Ready"
    return "Backlog"


def apply_meta(project: dict[str, Any], snapshot: dict[str, Any], number: int,
               explicit: tuple[str, str, str, str, str] | None = None,
               status_override: str | None = None) -> None:
    item_id = ensure_item(project, snapshot, number)
    latest = project_snapshot(project["number"])
    fields, options = field_maps(latest)
    issue = issue_info(number)
    status = "Done" if issue.get("state") == "closed" else (status_override or derive_status(issue))
    values = [status]
    names = ["Status"]
    if explicit is not None:
        values.extend(explicit)
        names.extend(("Priority", "Wave", "Size", "Risk", "Required Evidence"))

    for field_name, value in zip(names, values):
        if value not in options[field_name]:
            die(f"option {value!r} missing from field {field_name!r}")
        set_select(project["id"], item_id, fields[field_name], options[field_name][value])


def ensure_seed_items(project: dict[str, Any], snapshot: dict[str, Any]) -> None:
    # Add all currently-open issues so the Project remains the complete live management surface.
    open_issues = gh_json([
        "issue", "list", "--repo", REPO, "--state", "open", "--limit", "200",
        "--json", "number"
    ], token=PROJECT_TOKEN)
    numbers = {int(item["number"]) for item in open_issues}
    # Retain key completed children needed to understand current parent progress.
    numbers.update({5, 6, 101, 107})
    for number in sorted(numbers):
        explicit = ITEM_META.get(number)
        override = "In Review" if number == 108 else ("In Progress" if number == 115 else None)
        apply_meta(project, snapshot, number, explicit, override)


def reconcile_relationships() -> None:
    if not REPO_TOKEN:
        print("REPO_TOKEN not set; skipping native sub-issue/dependency reconciliation.")
        return

    for parent, children in SUBISSUES.items():
        current = gh_json([
            "issue", "view", str(parent), "--repo", REPO, "--json", "subIssues"
        ], token=REPO_TOKEN)
        existing = {int(x["number"]) for x in current.get("subIssues", [])}
        for child in children:
            if child in existing:
                continue
            parent_node = issue_info(parent, token=REPO_TOKEN)["node_id"]
            child_node = issue_info(child, token=REPO_TOKEN)["node_id"]
            mutation = """
            mutation($issueId:ID!, $subIssueId:ID!) {
              addSubIssue(input:{issueId:$issueId,subIssueId:$subIssueId,replaceParent:false}) {
                issue { id } subIssue { id }
              }
            }
            """
            graphql(mutation, {"issueId": parent_node, "subIssueId": child_node}, token=REPO_TOKEN)
            print(f"Linked sub-issue #{child} under #{parent}")

    for issue_number, blockers in BLOCKED_BY.items():
        current = gh_json([
            "issue", "view", str(issue_number), "--repo", REPO, "--json", "blockedBy"
        ], token=REPO_TOKEN)
        existing = {int(x["number"]) for x in current.get("blockedBy", [])}
        for blocker in blockers:
            if blocker in existing:
                continue
            blocker_info = issue_info(blocker, token=REPO_TOKEN)
            response = api_json(
                f"repos/{REPO}/issues/{issue_number}/dependencies/blocked_by",
                method="POST",
                body={"issue_id": blocker_info["id"]},
                token=REPO_TOKEN,
                versioned=True,
                check=False,
            )
            if isinstance(response, dict) and response.get("_error"):
                die(f"failed dependency #{issue_number} blocked by #{blocker}: {response['_error']}")
            print(f"Linked dependency #{issue_number} blocked by #{blocker}")


def bootstrap() -> dict[str, Any]:
    if not PROJECT_TOKEN:
        die("PROJECT_TOKEN is not configured")
    run(["gh", "auth", "status"], token=PROJECT_TOKEN)
    _, project = owner_and_project()
    configure_project(project)
    snapshot = project_snapshot(project["number"])
    link_repository(project, snapshot)
    snapshot = ensure_fields(project, project_snapshot(project["number"]))
    ensure_views(project, snapshot)
    snapshot = project_snapshot(project["number"])
    ensure_seed_items(project, snapshot)
    reconcile_relationships()
    final = project_snapshot(project["number"])
    print(json.dumps({
        "project": {"number": project["number"], "title": TITLE, "url": project["url"]},
        "fields": [f.get("name") for f in final["fields"]["nodes"] if f.get("name")],
        "views": [v["name"] for v in final["views"]["nodes"]],
        "items": len(final["items"]["nodes"]),
    }, indent=2, ensure_ascii=False))
    return project


def sync_issue(number: int) -> None:
    if not PROJECT_TOKEN:
        die("PROJECT_TOKEN is not configured")
    _, project = owner_and_project()
    snapshot = ensure_fields(project, project_snapshot(project["number"]))
    apply_meta(project, snapshot, number, ITEM_META.get(number))
    print(f"Synced issue #{number} into Project #{project['number']}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bootstrap", action="store_true")
    parser.add_argument("--sync-issue", type=int)
    args = parser.parse_args()
    if args.sync_issue is not None:
        sync_issue(args.sync_issue)
    else:
        bootstrap()


if __name__ == "__main__":
    main()

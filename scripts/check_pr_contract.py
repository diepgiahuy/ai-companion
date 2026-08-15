#!/usr/bin/env python3
"""Deterministic PR body contract validator.

This validates metadata/structure only. It intentionally does not judge code quality,
test sufficiency, architecture quality, or whether an issue is semantically complete.
"""

from __future__ import annotations

import argparse
import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

REQUIRED_SECTIONS = (
    "Summary",
    "Scope",
    "Verification",
    "Evidence boundary",
    "Risk / Rollback",
    "Remaining",
)
VALID_RISKS = {"L0", "L1", "L2", "L3"}
ISSUE_REF_RE = re.compile(r"\b(?:refs?|closes|fixes|resolves)\s+#\d+\b", re.IGNORECASE)
CLOSE_RE = re.compile(r"\b(?:closes|fixes|resolves)\s+#\d+\b", re.IGNORECASE)
SHA_RE = re.compile(r"^[0-9a-fA-F]{7,40}$")
HEADING_RE = re.compile(r"^##\s+(.+?)\s*$", re.MULTILINE)
PLACEHOLDER_RE = re.compile(r"\{\{[^{}\n]+\}\}")


@dataclass
class ValidationResult:
    errors: list[str]
    warnings: list[str]

    @property
    def ok(self) -> bool:
        return not self.errors


def _normalize_heading(value: str) -> str:
    return " ".join(value.strip().lower().split())


def _sections(body: str) -> dict[str, str]:
    matches = list(HEADING_RE.finditer(body))
    out: dict[str, str] = {}
    for i, match in enumerate(matches):
        start = match.end()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(body)
        out[_normalize_heading(match.group(1))] = body[start:end].strip()
    return out


def _field(body: str, name: str) -> str | None:
    match = re.search(rf"(?mi)^\s*{re.escape(name)}\s*:\s*(.+?)\s*$", body)
    return match.group(1).strip() if match else None


def _is_none(value: str | None) -> bool:
    if value is None:
        return False
    return value.strip().lower() in {"none", "n/a", "na", "not applicable"}


def _claims_local_pass(verification: str) -> bool:
    for line in verification.splitlines():
        lowered = line.lower()
        if "hosted" in lowered or "github check" in lowered:
            continue
        if re.search(r"\bpass(?:ed)?\b", line, re.IGNORECASE):
            return True
    return False


def validate(body: str, head_sha: str | None = None) -> ValidationResult:
    errors: list[str] = []
    warnings: list[str] = []

    if not body.strip():
        return ValidationResult(["PR body is empty."], warnings)

    if PLACEHOLDER_RE.search(body):
        errors.append("PR body still contains template placeholders like {{...}}.")

    if not ISSUE_REF_RE.search(body):
        errors.append("PR body must reference at least one GitHub issue (Refs/Closes/Fixes/Resolves #N).")

    sections = _sections(body)
    for required in REQUIRED_SECTIONS:
        key = _normalize_heading(required)
        if key not in sections:
            errors.append(f"Missing required section: ## {required}")
        elif not sections[key].strip():
            errors.append(f"Required section is empty: ## {required}")

    risk = _field(body, "Risk")
    if risk is None:
        errors.append("Missing Risk field.")
    else:
        risk_token = risk.split()[0].upper()
        if risk_token not in VALID_RISKS:
            errors.append(f"Risk must start with one of {sorted(VALID_RISKS)}; got {risk!r}.")

    tested_head = _field(body, "Local tested head")
    verification = sections.get(_normalize_heading("Verification"), "")
    claims_local_pass = _claims_local_pass(verification)

    if tested_head is None:
        errors.append("Missing Local tested head field.")
    else:
        tested = tested_head.strip()
        if tested.lower() == "not-run":
            if claims_local_pass:
                errors.append("Verification claims a local PASS but Local tested head is 'not-run'.")
        elif not SHA_RE.fullmatch(tested):
            errors.append("Local tested head must be 'not-run' or a 7-40 character hexadecimal commit SHA.")
        elif head_sha and SHA_RE.fullmatch(head_sha):
            if not head_sha.lower().startswith(tested.lower()) and not tested.lower().startswith(head_sha.lower()):
                warnings.append(
                    f"Local tested head {tested} differs from current PR head {head_sha}; "
                    "rerun affected local oracles if code changed. Hosted checks own exact-head merge proof."
                )

    human_gate = _field(body, "Human gate")
    remaining = sections.get(_normalize_heading("Remaining"), "")
    issue_remaining = _field(remaining, "Issue remaining")
    human_action = _field(remaining, "Human action")
    pr_blockers = _field(remaining, "PR blockers")

    for name, value in (
        ("PR blockers", pr_blockers),
        ("Issue remaining", issue_remaining),
        ("Human action", human_action),
    ):
        if value is None:
            errors.append(f"Missing '{name}:' field in ## Remaining.")

    if CLOSE_RE.search(body):
        if issue_remaining is not None and not _is_none(issue_remaining):
            errors.append("PR uses Closes/Fixes/Resolves while 'Issue remaining' is not none.")
        if human_gate is not None and not _is_none(human_gate):
            errors.append("PR uses Closes/Fixes/Resolves while 'Human gate' is not none.")
        if human_action is not None and not _is_none(human_action):
            errors.append("PR uses Closes/Fixes/Resolves while 'Human action' is not none.")

    if risk and risk.split()[0].upper() == "L3":
        for name in ("Evidence boundary", "Risk / Rollback"):
            section = sections.get(_normalize_heading(name), "")
            if not section.strip():
                errors.append(f"L3 PR requires a non-empty ## {name} section.")

    return ValidationResult(errors, warnings)


def _read_event(path: Path) -> tuple[str, str | None]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    pr = payload.get("pull_request") or {}
    body = pr.get("body") or ""
    head_sha = (pr.get("head") or {}).get("sha")
    return body, head_sha


def main(argv: Iterable[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--event", type=Path, help="GitHub pull_request(_target) event JSON")
    group.add_argument("--body-file", type=Path, help="Plain Markdown PR body (for local tests)")
    parser.add_argument("--head-sha", help="Optional current PR head SHA for body-file mode")
    args = parser.parse_args(argv)

    if args.event:
        body, head_sha = _read_event(args.event)
    else:
        body = args.body_file.read_text(encoding="utf-8")
        head_sha = args.head_sha

    result = validate(body, head_sha)

    for warning in result.warnings:
        print(f"::warning::{warning}")
    for error in result.errors:
        print(f"::error::{error}")

    if result.ok:
        print("PR contract: PASS")
        return 0
    print(f"PR contract: FAIL ({len(result.errors)} error(s))")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())

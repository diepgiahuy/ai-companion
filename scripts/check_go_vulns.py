#!/usr/bin/env python3
"""Fail on reachable govulncheck findings except one narrow upstream Go patch gap.

Temporary exception owned by issue #45:
- scanner Go version must be exactly go1.26.5
- the vulnerable symbol must be in module "stdlib"
- govulncheck must report fixed_version exactly v1.26.6

Anything else remains blocking. Delete this helper after the repository moves to
Go 1.26.6 stable.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Iterable

ALLOWED_SCANNER_GO = "go1.26.5"
ALLOWED_MODULE = "stdlib"
# govulncheck JSON encodes Go standard-library fixed versions with a leading
# "v" (for example v1.26.6), while go env GOVERSION uses "go1.26.5".
ALLOWED_FIXED_VERSION = "v1.26.6"
TARGET_TOOLCHAIN = "go1.26.6"


def annotation(level: str, title: str, message: str) -> None:
    safe = message.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    print(f"::{level} title={title}::{safe}")


def decode_stream(text: str) -> Iterable[dict[str, Any]]:
    decoder = json.JSONDecoder()
    index = 0
    length = len(text)
    while index < length:
        while index < length and text[index].isspace():
            index += 1
        if index >= length:
            break
        value, index = decoder.raw_decode(text, index)
        if not isinstance(value, dict):
            raise ValueError("govulncheck JSON stream contained a non-object message")
        yield value


def reachable_findings(messages: Iterable[dict[str, Any]]) -> tuple[str, list[dict[str, Any]]]:
    scanner_go = ""
    reached: dict[tuple[str, str, str, str], dict[str, Any]] = {}

    for message in messages:
        config = message.get("config")
        if isinstance(config, dict) and config.get("go_version"):
            scanner_go = str(config["go_version"])

        sbom = message.get("SBOM")
        if isinstance(sbom, dict) and sbom.get("go_version"):
            scanner_go = str(sbom["go_version"])

        finding = message.get("finding")
        if not isinstance(finding, dict):
            continue
        trace = finding.get("trace")
        if not isinstance(trace, list) or not trace:
            continue
        first = trace[0]
        if not isinstance(first, dict) or not first.get("function"):
            # govulncheck emits module/package findings before symbol reachability.
            # Only symbol findings represent a reachable vulnerable function.
            continue

        key = (
            str(finding.get("osv", "")),
            str(first.get("module", "")),
            str(first.get("package", "")),
            str(first.get("function", "")),
        )
        reached[key] = finding

    return scanner_go, list(reached.values())


def evaluate(scanner_go: str, findings: list[dict[str, Any]]) -> tuple[list[str], list[str]]:
    allowed: list[str] = []
    blocked: list[str] = []

    for finding in findings:
        trace = finding["trace"]
        first = trace[0]
        osv = str(finding.get("osv", "unknown"))
        module = str(first.get("module", ""))
        package = str(first.get("package", ""))
        function = str(first.get("function", ""))
        fixed = str(finding.get("fixed_version", ""))
        description = f"{osv}: {module}:{package}.{function} (fixed {fixed or 'unknown'})"

        if (
            scanner_go == ALLOWED_SCANNER_GO
            and module == ALLOWED_MODULE
            and fixed == ALLOWED_FIXED_VERSION
        ):
            allowed.append(description)
        else:
            blocked.append(description)

    return allowed, blocked


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("json_file", type=Path)
    args = parser.parse_args()

    try:
        text = args.json_file.read_text(encoding="utf-8")
        scanner_go, findings = reachable_findings(decode_stream(text))
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        message = f"could not parse govulncheck JSON: {exc}"
        annotation("error", "govulncheck JSON parse failure", message)
        print(f"ERROR: {message}", file=sys.stderr)
        return 2

    if not scanner_go:
        message = "govulncheck JSON did not identify the scanner Go version"
        annotation("error", "govulncheck metadata failure", message)
        print(f"ERROR: {message}", file=sys.stderr)
        return 2

    allowed, blocked = evaluate(scanner_go, findings)

    for item in allowed:
        annotation(
            "warning",
            "Temporary Go stdlib vulnerability exception",
            f"{item}; tolerated only while {ALLOWED_SCANNER_GO} is the latest pinned stable toolchain. "
            f"Remove issue #45 exception after {TARGET_TOOLCHAIN} stable is released.",
        )

    if blocked:
        print("Reachable vulnerabilities remain blocking:", file=sys.stderr)
        for item in blocked:
            annotation("error", "Reachable Go vulnerability", item)
            print(f"- {item}", file=sys.stderr)
        return 1

    if allowed:
        print(
            f"govulncheck: {len(allowed)} reachable stdlib finding(s) temporarily tolerated; "
            "all non-stdlib findings remain blocking."
        )
    else:
        print("govulncheck: no reachable vulnerabilities found")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

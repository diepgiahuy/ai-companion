#!/usr/bin/env python3
"""Classify GitHub CI work by event and changed paths.

Pull requests get fast software-only oracles. Pushes/manual runs on main get broad
promotion proof. Draft/ready-for-review state is intentionally irrelevant.
"""
from __future__ import annotations

import argparse
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Iterable


@dataclass(frozen=True)
class Scope:
    host: bool = False
    backend: bool = False
    backend_full: bool = False
    codeql: bool = False
    postgres: bool = False
    protocol: bool = False
    tier1: bool = False
    promotion: bool = False
    mode: str = "pr-targeted"

    def github_outputs(self) -> dict[str, str]:
        values = asdict(self)
        return {
            key: (str(value).lower() if isinstance(value, bool) else str(value))
            for key, value in values.items()
        }


def _starts(path: str, *prefixes: str) -> bool:
    return any(path.startswith(prefix) for prefix in prefixes)


def _is_ci_control(path: str) -> bool:
    return (
        path.startswith(".github/workflows/")
        or path
        in {
            "Dockerfile.test",
            "scripts/backend_quality.sh",
            "scripts/check_evidence.py",
            "scripts/check_single_path.py",
        }
    )


def _promotion_scope() -> Scope:
    return Scope(
        host=True,
        backend=True,
        backend_full=True,
        codeql=True,
        postgres=True,
        protocol=True,
        tier1=True,
        promotion=True,
        mode="promotion",
    )


def _pr_broad_scope(mode: str) -> Scope:
    """Return the broadest PR scope without promotion-only device gates."""
    return Scope(
        host=True,
        backend=True,
        postgres=True,
        mode=mode,
    )


def classify(event: str, paths: Iterable[str] = (), *, unknown_changes: bool = False) -> Scope:
    """Return the CI scope for one workflow event."""
    if event in {"push", "workflow_dispatch"}:
        return _promotion_scope()
    if event == "schedule":
        return Scope(codeql=True, mode="scheduled-security")
    if event != "pull_request":
        raise ValueError(f"unsupported event: {event}")
    if unknown_changes:
        return _pr_broad_scope("pr-fail-safe")

    cleaned = sorted({path.strip() for path in paths if path.strip()})
    if any(_is_ci_control(path) for path in cleaned):
        return _pr_broad_scope("pr-ci-control")

    host = False
    backend = False
    postgres = False

    for path in cleaned:
        if (
            _starts(path, "components/", "host/", "testdata/")
            or path in {"CMakeLists.txt", "partitions.csv", "scripts/e2e.sh", "scripts/budget_check.py"}
        ):
            host = True

        if (
            _starts(path, "backend/", "db/postgres/", "ops/postgres/", "testdata/")
            or path in {"Dockerfile.test", "scripts/backend_quality.sh"}
        ):
            backend = True

        ownerweb_go = path.startswith("backend/internal/ownerweb/") and path.endswith(".go")
        if (
            _starts(
                path,
                "db/postgres/",
                "ops/postgres/",
                "backend/internal/pgstore/",
                "backend/internal/jobs/",
                "backend/cmd/companiond/",
                "backend/cmd/companion-river-migrate/",
                "backend/cmd/companion-migrate/",
            )
            or ownerweb_go
            or path in {"backend/go.mod", "backend/go.sum"}
        ):
            postgres = True

    return Scope(
        host=host,
        backend=backend,
        postgres=postgres,
    )


def _write_lines(path: str, lines: Iterable[str]) -> None:
    with open(path, "a", encoding="utf-8") as handle:
        for line in lines:
            handle.write(line)
            handle.write("\n")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--event", required=True)
    parser.add_argument("--files-file")
    parser.add_argument("--unknown-changes", action="store_true")
    parser.add_argument("--github-output")
    parser.add_argument("--summary")
    args = parser.parse_args()

    paths: list[str] = []
    if args.files_file:
        paths = Path(args.files_file).read_text(encoding="utf-8").splitlines()

    scope = classify(args.event, paths, unknown_changes=args.unknown_changes)
    outputs = scope.github_outputs()

    if args.github_output:
        _write_lines(args.github_output, (f"{key}={value}" for key, value in outputs.items()))
    else:
        for key, value in outputs.items():
            print(f"{key}={value}")

    if args.summary:
        _write_lines(
            args.summary,
            [
                "## CI scope",
                "",
                f"- mode: {scope.mode}",
                f"- host tests: {scope.host}",
                f"- backend quality: {scope.backend} ({'full' if scope.backend_full else 'fast'})",
                f"- CodeQL Go: {scope.codeql}",
                f"- PostgreSQL/River: {scope.postgres}",
                f"- Protocol firmware compile: {scope.protocol}",
                f"- Tier-1 software device: {scope.tier1}",
                f"- promotion aggregation: {scope.promotion}",
            ],
        )


if __name__ == "__main__":
    main()

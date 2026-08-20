#!/usr/bin/env python3
"""Classify GitHub CI work by event and changed paths.

Pull requests get the nearest useful oracle. Pushes/manual runs on main get broad
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
    software_device_compile: bool = False
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


def _broad(mode: str, *, promotion: bool) -> Scope:
    return Scope(
        host=True,
        software_device_compile=True,
        backend=True,
        backend_full=promotion,
        codeql=promotion,
        postgres=True,
        protocol=True,
        tier1=True,
        promotion=promotion,
        mode=mode,
    )


def classify(event: str, paths: Iterable[str] = (), *, unknown_changes: bool = False) -> Scope:
    """Return the CI scope for one workflow event."""
    if event in {"push", "workflow_dispatch"}:
        return _broad("promotion", promotion=True)
    if event == "schedule":
        return Scope(codeql=True, mode="scheduled-security")
    if event != "pull_request":
        raise ValueError(f"unsupported event: {event}")
    if unknown_changes:
        return _broad("pr-fail-safe", promotion=False)

    cleaned = sorted({path.strip() for path in paths if path.strip()})
    if any(_is_ci_control(path) for path in cleaned):
        # Workflow/gate implementation changes validate the broad gate they alter.
        # The classifier and its unit tests are validated directly by the always-on
        # classification-policy test and should not force unrelated hardware/Tier-1 work.
        return _broad("pr-ci-control", promotion=False)

    host = False
    software_device_compile = False
    backend = False
    postgres = False
    protocol = False
    explicit_tier1 = False
    backend_device_boundary = False
    firmware_device_boundary = False

    for path in cleaned:
        if (
            _starts(path, "components/", "host/", "testdata/")
            or path in {"CMakeLists.txt", "partitions.csv", "scripts/e2e.sh", "scripts/budget_check.py"}
        ):
            host = True

        # The Tier-1 logical device directly compiles CompanionApp. App/API
        # changes therefore need this cheap compile oracle on the PR itself even
        # when full PostgreSQL/backend orchestration is intentionally deferred to
        # promotion. Provisioning changes additionally require the real firmware
        # compile below because they execute before CompanionApp starts.
        if _starts(path, "components/companion_app/", "host/companion_software_device/"):
            software_device_compile = True

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

        if (
            _starts(
                path,
                "backend/internal/protocol/",
                "backend/internal/server/",
                "components/companion_app/",
                "components/esp32_network/",
                "components/esp32_provisioning/",
                "testdata/protocol/",
                "main/",
            )
            or path in {"CMakeLists.txt", "sdkconfig.defaults", "partitions.csv", "Dockerfile.esp-idf"}
        ):
            protocol = True

        if _starts(
            path,
            "backend/internal/controlplane/",
            "backend/internal/protocol/",
            "backend/internal/server/",
            "backend/cmd/companiond/",
        ):
            backend_device_boundary = True

        if _starts(
            path,
            "components/companion_app/",
            "components/esp32_network/",
            "components/esp32_provisioning/",
            "host/companion_software_device/",
            "main/",
        ):
            firmware_device_boundary = True

        if _starts(path, "host/companion_software_device/", "testdata/scenarios/"):
            explicit_tier1 = True

    tier1 = explicit_tier1 or (backend_device_boundary and firmware_device_boundary)
    return Scope(
        host=host,
        software_device_compile=software_device_compile,
        backend=backend,
        postgres=postgres,
        protocol=protocol,
        tier1=tier1,
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
                f"- software-device compile: {scope.software_device_compile}",
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

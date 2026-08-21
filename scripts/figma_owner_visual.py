#!/usr/bin/env python3
"""Figma-backed Owner Hub visual oracle.

This tool deliberately uses only the Figma REST API. It never prints the access token
or temporary rendered-image URLs. Repository/backend code remains feature authority;
Figma is visual-reference authority.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

API = "https://api.figma.com/v1"
DEFAULT_FILE_KEY = "MlU3bVLzIQ00YtcPakBss9"
CANDIDATE_RE = re.compile(
    r"(?:^|[\s/\-_])(?:live|backend[\s_-]*ready|home|companion|personal|settings|desktop|mobile)(?:$|[\s/\-_])",
    re.IGNORECASE,
)


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def figma_token() -> str:
    token = os.environ.get("FIGMA_TOKEN", "").strip()
    if not token:
        fail("FIGMA_TOKEN is not configured")
    return token


def request_json(token: str, path: str, query: dict[str, str] | None = None) -> dict:
    url = f"{API}{path}"
    if query:
        url += "?" + urllib.parse.urlencode(query)
    request = urllib.request.Request(
        url,
        headers={
            "X-Figma-Token": token,
            "Accept": "application/json",
            "User-Agent": "ai-companion-figma-visual-oracle/1",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=45) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = ""
        try:
            detail = exc.read().decode("utf-8", "replace")[:1000]
        except Exception:
            pass
        fail(f"Figma API {exc.code} for {path}: {detail or exc.reason}")
    except urllib.error.URLError as exc:
        fail(f"Figma API unavailable for {path}: {exc.reason}")
    raise AssertionError("unreachable")


def download(url: str, target: pathlib.Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": "ai-companion-figma-visual-oracle/1"})
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            target.write_bytes(response.read())
    except urllib.error.HTTPError as exc:
        fail(f"render download failed with HTTP {exc.code}")
    except urllib.error.URLError as exc:
        fail(f"render download failed: {exc.reason}")


def walk(node: dict, parents: list[str]) -> list[dict]:
    name = str(node.get("name") or "")
    node_type = str(node.get("type") or "")
    current = parents + ([name] if name else [])
    rows: list[dict] = []
    if node_type in {"CANVAS", "SECTION", "FRAME", "COMPONENT", "COMPONENT_SET"}:
        box = node.get("absoluteBoundingBox") or {}
        rows.append(
            {
                "id": node.get("id"),
                "type": node_type,
                "name": name,
                "path": " / ".join(current),
                "width": box.get("width"),
                "height": box.get("height"),
                "visible": node.get("visible", True),
            }
        )
    for child in node.get("children") or []:
        rows.extend(walk(child, current))
    return rows


def cmd_inventory(args: argparse.Namespace) -> None:
    token = figma_token()

    # depth=4 inventories pages, sections and screen frames without downloading every
    # leaf/vector in the design file.
    payload = request_json(token, f"/files/{args.file_key}", {"depth": "4"})
    document = payload.get("document")
    if not isinstance(document, dict):
        fail("Figma file response is missing document content")

    rows = walk(document, [])
    candidates = [row for row in rows if CANDIDATE_RE.search(row["path"])]
    result = {
        "file_key": args.file_key,
        "file_name": payload.get("name"),
        "version": payload.get("version"),
        "last_modified": payload.get("lastModified"),
        "candidate_count": len(candidates),
        "candidates": candidates,
        "all_structural_nodes": rows,
    }

    out = pathlib.Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    print(f"Figma file: {result['file_name']} ({args.file_key})")
    print(f"Figma version: {result['version']}")
    print(f"Candidate structural nodes: {len(candidates)}")
    for row in candidates:
        print(
            f"- {row['id']} [{row['type']}] {row['path']} "
            f"({row['width']}x{row['height']})"
        )
    if not candidates:
        fail("no Owner Hub/LIVE candidates found; inspect the uploaded frame inventory")


def cmd_export(args: argparse.Namespace) -> None:
    token = figma_token()
    node_ids = [item.strip() for item in args.ids.split(",") if item.strip()]
    if not node_ids:
        fail("--ids must contain at least one Figma node ID")

    payload = request_json(
        token,
        f"/images/{args.file_key}",
        {
            "ids": ",".join(node_ids),
            "format": "png",
            "scale": str(args.scale),
            "contents_only": "true",
        },
    )
    images = payload.get("images") or {}
    out_dir = pathlib.Path(args.output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    manifest: dict[str, dict[str, str | int]] = {}

    for node_id in node_ids:
        url = images.get(node_id)
        if not url:
            fail(f"Figma did not return a render URL for node {node_id}")
        filename = f"figma-{node_id.replace(':', '-')}.png"
        target = out_dir / filename
        download(url, target)
        manifest[node_id] = {"file": filename, "bytes": target.stat().st_size}
        print(f"exported {node_id} -> {filename} ({target.stat().st_size} bytes)")

    (out_dir / "render_manifest.json").write_text(
        json.dumps(
            {
                "file_key": args.file_key,
                "scale": args.scale,
                "nodes": manifest,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["inventory", "export"])
    parser.add_argument("--file-key", default=DEFAULT_FILE_KEY)
    parser.add_argument("--output", default="artifacts/figma/frame_inventory.json")
    parser.add_argument("--output-dir", default="artifacts/figma/renders")
    parser.add_argument("--ids", default="")
    parser.add_argument("--scale", type=float, default=1.0)
    args = parser.parse_args()
    if args.command == "inventory":
        cmd_inventory(args)
    elif args.command == "export":
        cmd_export(args)


if __name__ == "__main__":
    main()

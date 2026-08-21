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
from collections import Counter

API = "https://api.figma.com/v1"
DEFAULT_FILE_KEY = "MlU3bVLzIQ00YtcPakBss9"
CANDIDATE_RE = re.compile(
    r"(?:^|[\s/\-_])(?:live|backend[\s_-]*ready|home|companion|personal|settings|desktop|mobile)(?:$|[\s/\-_])",
    re.IGNORECASE,
)

# Verified from the live file inventory and rendered screenshots on 2026-08-21.
# The 10:* set is the current dark V3 surface. The 2:* set is an older light surface.
APPROVED_V3 = {
    "home": {"screen": "10:13", "sidebar": "10:14", "content": "10:28"},
    "companion": {"screen": "10:63", "sidebar": "10:64", "content": "10:78"},
    "personal": {"screen": "10:109", "sidebar": "10:110", "content": "10:124"},
    "settings": {"screen": "10:198", "sidebar": "10:199", "content": "10:213"},
    "mobile_home": {"screen": "11:146"},
}


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


def rgba_hex(color: dict | None) -> str | None:
    if not isinstance(color, dict):
        return None
    r = round(float(color.get("r", 0)) * 255)
    g = round(float(color.get("g", 0)) * 255)
    b = round(float(color.get("b", 0)) * 255)
    a = float(color.get("a", 1))
    if a >= 0.999:
        return f"#{r:02x}{g:02x}{b:02x}"
    return f"rgba({r},{g},{b},{a:.3f})"


def first_solid_fill(node: dict) -> str | None:
    for paint in node.get("fills") or []:
        if paint.get("visible", True) and paint.get("type") == "SOLID":
            color = dict(paint.get("color") or {})
            if "opacity" in paint:
                color["a"] = paint["opacity"]
            return rgba_hex(color)
    return None


def node_summary(node: dict) -> dict:
    box = node.get("absoluteBoundingBox") or {}
    style = node.get("style") or {}
    return {
        "id": node.get("id"),
        "name": node.get("name"),
        "type": node.get("type"),
        "width": box.get("width"),
        "height": box.get("height"),
        "x": box.get("x"),
        "y": box.get("y"),
        "fill": first_solid_fill(node),
        "corner_radius": node.get("cornerRadius"),
        "font_family": style.get("fontFamily"),
        "font_size": style.get("fontSize"),
        "font_weight": style.get("fontWeight"),
        "characters": node.get("characters") if node.get("type") == "TEXT" else None,
    }


def iter_nodes(node: dict):
    yield node
    for child in node.get("children") or []:
        yield from iter_nodes(child)


def cmd_inventory(args: argparse.Namespace) -> None:
    token = figma_token()
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
        json.dumps({"file_key": args.file_key, "scale": args.scale, "nodes": manifest}, indent=2) + "\n",
        encoding="utf-8",
    )


def cmd_contract(args: argparse.Namespace) -> None:
    token = figma_token()
    ids = []
    for view in APPROVED_V3.values():
        ids.extend(view.values())
    ids = list(dict.fromkeys(ids))
    payload = request_json(
        token,
        f"/files/{args.file_key}/nodes",
        {"ids": ",".join(ids), "depth": "3"},
    )
    nodes = payload.get("nodes") or {}
    missing = [node_id for node_id in ids if not nodes.get(node_id)]
    if missing:
        fail(f"approved V3 node IDs are missing from Figma: {', '.join(missing)}")

    structural: dict[str, dict[str, dict]] = {}
    font_sizes: Counter[str] = Counter()
    fills: Counter[str] = Counter()
    raw: dict[str, dict] = {}
    for view_name, mapping in APPROVED_V3.items():
        structural[view_name] = {}
        for role, node_id in mapping.items():
            doc = nodes[node_id]["document"]
            structural[view_name][role] = node_summary(doc)
            raw[node_id] = doc
            for item in iter_nodes(doc):
                summary = node_summary(item)
                if summary["font_size"] is not None:
                    font_sizes[str(summary["font_size"])] += 1
                if summary["fill"]:
                    fills[summary["fill"]] += 1

    contract = {
        "file_key": args.file_key,
        "file_name": payload.get("name"),
        "version": payload.get("version"),
        "approved_v3": APPROVED_V3,
        "structural": structural,
        "observed_font_sizes": dict(font_sizes.most_common()),
        "observed_solid_fills": dict(fills.most_common()),
        "raw_selected_nodes": raw,
    }
    out = pathlib.Path(args.contract_output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(contract, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print("Approved V3 Figma contract:")
    for view_name, roles in structural.items():
        parts = []
        for role, summary in roles.items():
            parts.append(
                f"{role}={summary['id']} {summary['width']}x{summary['height']} fill={summary['fill']}"
            )
        print(f"- {view_name}: " + "; ".join(parts))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["inventory", "export", "contract"])
    parser.add_argument("--file-key", default=DEFAULT_FILE_KEY)
    parser.add_argument("--output", default="artifacts/figma/frame_inventory.json")
    parser.add_argument("--output-dir", default="artifacts/figma/renders")
    parser.add_argument("--contract-output", default="artifacts/figma/figma_contract.json")
    parser.add_argument("--ids", default="")
    parser.add_argument("--scale", type=float, default=1.0)
    args = parser.parse_args()
    if args.command == "inventory":
        cmd_inventory(args)
    elif args.command == "export":
        cmd_export(args)
    elif args.command == "contract":
        cmd_contract(args)


if __name__ == "__main__":
    main()

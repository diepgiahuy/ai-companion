#!/usr/bin/env python3
"""Figma REST-backed Owner Hub visual oracle.

Figma is visual authority only. Repository/backend/firmware remain capability authority.
The token and temporary signed render URLs are never printed or persisted.
"""
from __future__ import annotations

import argparse
import json
import math
import os
import pathlib
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

API = "https://api.figma.com/v1"
DEFAULT_FILE_KEY = "MlU3bVLzIQ00YtcPakBss9"

# Current approved dark V3 nodes verified from the live Figma file. Candidate/planned
# nodes (Knowledge, Memory, data export) are intentionally excluded.
NODE = {
    "home_screen": "10:13", "home_sidebar": "10:14", "brand": "10:15",
    "nav_active": "10:16", "nav_active_text": "10:17", "nav_inactive": "10:18", "nav_inactive_text": "10:19",
    "home_content": "10:28", "home_h1": "10:29", "home_lead": "10:30", "home_live": "10:31", "home_live_text": "10:32",
    "home_device": "10:33", "home_quick_title": "10:37", "home_quick_group": "10:38", "home_quick": "10:39",
    "home_summary_title": "10:48", "home_summary_group": "10:49", "home_summary": "10:50", "home_truth": "10:62",
    "comp_content": "10:78", "comp_h1": "10:79", "comp_lead": "10:80", "comp_state": "10:83",
    "comp_title": "10:87", "comp_settings": "10:88", "comp_row": "10:89", "comp_advanced": "10:105",
    "mobile_shell": "11:146", "mobile_h1": "11:147", "mobile_device": "11:148", "mobile_nav": "11:151",
    "mobile_item": "11:152", "mobile_item_text": "11:153",
}
SCREEN_EXPORTS = ["10:13", "10:63", "10:109", "10:198", "11:146"]
CANDIDATE_RE = re.compile(r"(?:^|[\s/\-_])(?:home|companion|personal|settings|live|backend[\s_-]*ready|mobile)(?:$|[\s/\-_])", re.I)


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def token() -> str:
    value = os.environ.get("FIGMA_TOKEN", "").strip()
    if not value:
        fail("FIGMA_TOKEN is not configured")
    return value


def request_json(path: str, query: dict[str, str] | None = None) -> dict:
    url = API + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    request = urllib.request.Request(url, headers={"X-Figma-Token": token(), "Accept": "application/json", "User-Agent": "ai-companion-figma-oracle/1"})
    try:
        with urllib.request.urlopen(request, timeout=45) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:800]
        fail(f"Figma API {exc.code} for {path}: {detail or exc.reason}")
    except urllib.error.URLError as exc:
        fail(f"Figma API unavailable for {path}: {exc.reason}")
    raise AssertionError("unreachable")


def download(url: str, target: pathlib.Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": "ai-companion-figma-oracle/1"})
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            target.write_bytes(response.read())
    except (urllib.error.HTTPError, urllib.error.URLError) as exc:
        fail(f"Figma render download failed: {exc}")


def box(node: dict) -> dict:
    return node.get("absoluteBoundingBox") or {}


def solid(node: dict) -> str | None:
    for paint in node.get("fills") or []:
        if paint.get("visible", True) and paint.get("type") == "SOLID":
            c = paint.get("color") or {}
            r, g, b = (round(float(c.get(k, 0)) * 255) for k in ("r", "g", "b"))
            a = float(paint.get("opacity", c.get("a", 1)))
            return f"rgb({r},{g},{b})" if a >= .999 else f"rgba({r},{g},{b},{a:.3f})"
    return None


def style(node: dict) -> dict:
    return node.get("style") or {}


def text_style(node: dict) -> dict:
    s = style(node)
    return {"font_size": s.get("fontSize"), "font_weight": s.get("fontWeight"), "color": solid(node)}


def relative(node: dict, parent: dict) -> dict:
    a, p = box(node), box(parent)
    return {"x": a.get("x", 0) - p.get("x", 0), "y": a.get("y", 0) - p.get("y", 0), "width": a.get("width"), "height": a.get("height")}


def geom(node: dict, parent: dict, *, with_fill: bool = False, with_radius: bool = False) -> dict:
    out = relative(node, parent)
    if with_fill:
        out["bg"] = solid(node)
    if with_radius:
        out["radius"] = node.get("cornerRadius")
    return out


def gap(first: dict, second: dict, axis: str) -> float:
    a, b = box(first), box(second)
    return float(b[axis]) - float(a[axis]) - float(a["width" if axis == "x" else "height"])


def walk(node: dict, parents: list[str]) -> list[dict]:
    name = str(node.get("name") or "")
    current = parents + ([name] if name else [])
    rows = []
    if node.get("type") in {"CANVAS", "SECTION", "FRAME", "COMPONENT", "COMPONENT_SET"}:
        b = box(node)
        rows.append({"id": node.get("id"), "type": node.get("type"), "name": name, "path": " / ".join(current), "width": b.get("width"), "height": b.get("height")})
    for child in node.get("children") or []:
        rows.extend(walk(child, current))
    return rows


def cmd_inventory(args: argparse.Namespace) -> None:
    payload = request_json(f"/files/{args.file_key}", {"depth": "4"})
    rows = walk(payload.get("document") or {}, [])
    candidates = [row for row in rows if CANDIDATE_RE.search(row["path"])]
    result = {"file_key": args.file_key, "file_name": payload.get("name"), "version": payload.get("version"), "last_modified": payload.get("lastModified"), "candidates": candidates}
    out = pathlib.Path(args.output); out.parent.mkdir(parents=True, exist_ok=True); out.write_text(json.dumps(result, indent=2) + "\n")
    print(f"Figma file: {result['file_name']} version={result['version']} candidates={len(candidates)}")
    if not candidates:
        fail("no Owner Hub structural candidates found")


def cmd_export(args: argparse.Namespace) -> None:
    ids = [item.strip() for item in args.ids.split(",") if item.strip()] or SCREEN_EXPORTS
    payload = request_json(f"/images/{args.file_key}", {"ids": ",".join(ids), "format": "png", "scale": str(args.scale), "contents_only": "true"})
    out_dir = pathlib.Path(args.output_dir); out_dir.mkdir(parents=True, exist_ok=True)
    manifest = {}
    for node_id in ids:
        url = (payload.get("images") or {}).get(node_id)
        if not url:
            fail(f"Figma did not render node {node_id}")
        target = out_dir / f"figma-{node_id.replace(':','-')}.png"
        download(url, target)
        manifest[node_id] = {"file": target.name, "bytes": target.stat().st_size}
        print(f"exported {node_id} -> {target.name}")
    (out_dir / "render_manifest.json").write_text(json.dumps({"nodes": manifest, "scale": args.scale}, indent=2) + "\n")


def cmd_contract(args: argparse.Namespace) -> None:
    ids = list(dict.fromkeys(NODE.values()))
    payload = request_json(f"/files/{args.file_key}/nodes", {"ids": ",".join(ids), "depth": "1"})
    docs = {node_id: entry.get("document") for node_id, entry in (payload.get("nodes") or {}).items() if entry}
    missing = [node_id for node_id in ids if not docs.get(node_id)]
    if missing:
        fail("approved V3 node IDs missing: " + ", ".join(missing))
    n = lambda key: docs[NODE[key]]
    screen, content = n("home_screen"), n("home_content")
    brand = {**relative(n("brand"), screen), **text_style(n("brand"))}
    nav_active, nav_inactive = n("nav_active"), n("nav_inactive")
    oracle = {
        "desktop": {
            "viewport": {"width": box(screen)["width"], "height": box(screen)["height"]},
            "body_bg": solid(screen),
            "sidebar": {"width": box(n("home_sidebar"))["width"], "bg": solid(n("home_sidebar"))},
            "brand": {"x": brand["x"], "y": brand["y"], "font_size": brand["font_size"], "font_weight": brand["font_weight"], "color": brand["color"], "mark_display": "none"},
            "nav": {"item_width": box(nav_active)["width"], "item_height": box(nav_active)["height"], "item_radius": nav_active.get("cornerRadius"), "gap": gap(nav_active, nav_inactive, "y"), "active_bg": solid(nav_active), "active_color": solid(n("nav_active_text")), "active_weight": style(n("nav_active_text")).get("fontWeight"), "inactive_color": solid(n("nav_inactive_text")), "inactive_weight": style(n("nav_inactive_text")).get("fontWeight")},
            "topbar_display": "none",
            "content": {"x": relative(content, screen)["x"], "width": box(content)["width"]},
            "home": {
                "h1": {**relative(n("home_h1"), content), **text_style(n("home_h1"))},
                "lead": {**relative(n("home_lead"), content), **text_style(n("home_lead"))},
                "live": {**geom(n("home_live"), content, with_fill=True, with_radius=True), **text_style(n("home_live_text"))},
                "device": geom(n("home_device"), content, with_fill=True, with_radius=True),
                "quick": {**geom(n("home_quick"), content, with_fill=True, with_radius=True), "gap": gap(n("home_quick"), (n("home_quick_group").get("children") or [None, None])[1], "x")},
                "summary": {**geom(n("home_summary"), content, with_fill=True, with_radius=True), "gap": gap(n("home_summary"), (n("home_summary_group").get("children") or [None, None])[1], "y")},
                "truth": {**relative(n("home_truth"), content), **text_style(n("home_truth"))},
            },
        },
        "companion": {
            "h1": {**relative(n("comp_h1"), n("comp_content")), **text_style(n("comp_h1"))},
            "lead": {**relative(n("comp_lead"), n("comp_content")), **text_style(n("comp_lead"))},
            "state": geom(n("comp_state"), n("comp_content"), with_fill=True, with_radius=True),
            "section_title": {**relative(n("comp_title"), n("comp_content")), **text_style(n("comp_title"))},
            "settings": {**relative(n("comp_settings"), n("comp_content")), "gap": 8},
            "row": geom(n("comp_row"), n("comp_content"), with_fill=True, with_radius=True),
            "advanced": geom(n("comp_advanced"), n("comp_content"), with_fill=True, with_radius=True),
        },
        "mobile": {
            "viewport": {"width": box(n("mobile_shell"))["width"], "height": box(n("mobile_shell"))["height"]},
            "h1": {**relative(n("mobile_h1"), n("mobile_shell")), **text_style(n("mobile_h1"))},
            "device": geom(n("mobile_device"), n("mobile_shell"), with_fill=True, with_radius=True),
            "nav": {"y": relative(n("mobile_nav"), n("mobile_shell"))["y"], "height": box(n("mobile_nav"))["height"], "item_height": box(n("mobile_item"))["height"], "item_radius": n("mobile_item").get("cornerRadius"), "item_font_size": style(n("mobile_item_text")).get("fontSize"), "active_bg": solid(n("mobile_item")), "active_color": solid(n("mobile_item_text"))},
        },
    }
    out = pathlib.Path(args.contract_output); out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps({"file_key": args.file_key, "file_name": payload.get("name"), "version": payload.get("version"), "oracle": oracle}, indent=2) + "\n")
    print(f"approved V3 contract loaded from {len(ids)} live Figma nodes")


def color(value: str) -> tuple[float, float, float, float] | None:
    if not isinstance(value, str):
        return None
    v = value.strip().lower()
    if v.startswith("#") and len(v) == 7:
        return tuple([int(v[i:i+2], 16) for i in (1, 3, 5)] + [1.0])  # type: ignore[return-value]
    m = re.fullmatch(r"rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)(?:\s*,\s*([\d.]+))?\s*\)", v)
    if m:
        return (float(m.group(1)), float(m.group(2)), float(m.group(3)), float(m.group(4) or 1.0))
    return None


def tolerance(path: str) -> float:
    leaf = path.rsplit(".", 1)[-1]
    if leaf in {"x", "y"}:
        return 4.0
    if leaf in {"width", "height"}:
        return 3.0
    if leaf in {"radius", "gap", "item_radius", "item_height", "item_width"}:
        return 1.0
    if leaf in {"font_size", "item_font_size"}:
        return 0.2
    if leaf in {"font_weight", "active_weight", "inactive_weight"}:
        return 1.0
    return 0.01


def compare(expected: Any, actual: Any, path: str, mismatches: list[dict]) -> None:
    if isinstance(expected, dict):
        if not isinstance(actual, dict):
            mismatches.append({"path": path, "expected": expected, "actual": actual, "reason": "type"}); return
        for key, value in expected.items():
            if key not in actual:
                mismatches.append({"path": f"{path}.{key}", "expected": value, "actual": None, "reason": "missing"})
            else:
                compare(value, actual[key], f"{path}.{key}", mismatches)
        return
    ec, ac = color(expected) if isinstance(expected, str) else None, color(actual) if isinstance(actual, str) else None
    if ec is not None:
        if ac is None or any(abs(a-b) > (0.01 if i == 3 else 1.0) for i, (a, b) in enumerate(zip(ec, ac))):
            mismatches.append({"path": path, "expected": expected, "actual": actual, "reason": "color"})
        return
    if isinstance(expected, (int, float)) and not isinstance(expected, bool):
        if not isinstance(actual, (int, float)) or math.isnan(float(actual)) or abs(float(expected)-float(actual)) > tolerance(path):
            mismatches.append({"path": path, "expected": expected, "actual": actual, "tolerance": tolerance(path), "reason": "numeric"})
        return
    if expected != actual:
        mismatches.append({"path": path, "expected": expected, "actual": actual, "reason": "value"})


def cmd_compare(args: argparse.Namespace) -> None:
    figma = json.loads(pathlib.Path(args.contract_output).read_text())
    production = json.loads(pathlib.Path(args.production_contract).read_text())
    mismatches: list[dict] = []
    compare(figma["oracle"], production, "visual", mismatches)
    report = {"figma_version": figma.get("version"), "result": "passed" if not mismatches else "failed", "mismatch_count": len(mismatches), "mismatches": mismatches}
    out = pathlib.Path(args.report_output); out.parent.mkdir(parents=True, exist_ok=True); out.write_text(json.dumps(report, indent=2) + "\n")
    if mismatches:
        for item in mismatches[:50]:
            print(f"MISMATCH {item['path']}: expected={item['expected']!r} actual={item['actual']!r}")
        fail(f"Owner Hub differs from approved live Figma V3 at {len(mismatches)} contract fields")
    print(f"Owner Hub visual contract PASS against Figma version {figma.get('version')}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["inventory", "export", "contract", "compare"])
    parser.add_argument("--file-key", default=DEFAULT_FILE_KEY)
    parser.add_argument("--output", default="artifacts/figma/frame_inventory.json")
    parser.add_argument("--output-dir", default="artifacts/figma/renders")
    parser.add_argument("--contract-output", default="artifacts/figma/figma_contract.json")
    parser.add_argument("--production-contract", default="artifacts/ownerweb/production_contract.json")
    parser.add_argument("--report-output", default="artifacts/figma/visual_report.json")
    parser.add_argument("--ids", default="")
    parser.add_argument("--scale", type=float, default=1.0)
    args = parser.parse_args()
    {"inventory": cmd_inventory, "export": cmd_export, "contract": cmd_contract, "compare": cmd_compare}[args.command](args)

if __name__ == "__main__":
    main()

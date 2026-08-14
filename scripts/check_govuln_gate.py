#!/usr/bin/env python3
"""Fail-closed govulncheck JSON gate with one temporary stdlib exception."""
from __future__ import annotations
import json, os, sys
from pathlib import Path
from typing import Any, Iterable, TextIO

ALLOWED_GO_VERSION = "go1.26.5"
ALLOWED_MODULE = "stdlib"
ALLOWED_FIXED_VERSION = "v1.26.6"

def messages(stream: TextIO) -> Iterable[dict[str, Any]]:
    decoder = json.JSONDecoder(); data = stream.read(); index = 0
    while index < len(data):
        while index < len(data) and data[index].isspace(): index += 1
        if index >= len(data): break
        value, index = decoder.raw_decode(data, index)
        if not isinstance(value, dict): raise ValueError("govulncheck JSON stream contained a non-object message")
        yield value

def is_symbol_reachable(finding: dict[str, Any]) -> bool:
    trace = finding.get("trace") or []
    return bool(trace and isinstance(trace, list) and isinstance(trace[0], dict) and trace[0].get("function"))

def classify(stream: TextIO) -> tuple[str, list[dict[str, Any]], list[dict[str, Any]]]:
    go_version = ""; scan_level = ""; reachable = []
    for message in messages(stream):
        config = message.get("config")
        if isinstance(config, dict):
            go_version = str(config.get("go_version") or ""); scan_level = str(config.get("scan_level") or "")
        finding = message.get("finding")
        if isinstance(finding, dict) and is_symbol_reachable(finding): reachable.append(finding)
    if not go_version: raise ValueError("govulncheck JSON stream did not contain config.go_version")
    if scan_level != "symbol": raise ValueError(f"govulncheck must run at symbol scan level, got {scan_level!r}")
    allowed=[]; blocked=[]
    for finding in reachable:
        vulnerable=(finding.get("trace") or [{}])[0]; module=vulnerable.get("module") if isinstance(vulnerable,dict) else None; fixed=str(finding.get("fixed_version") or "")
        (allowed if go_version==ALLOWED_GO_VERSION and module==ALLOWED_MODULE and fixed==ALLOWED_FIXED_VERSION else blocked).append(finding)
    return go_version, allowed, blocked

def finding_label(finding: dict[str, Any]) -> str:
    trace=finding.get("trace") or []; vulnerable=trace[0] if trace and isinstance(trace[0],dict) else {}
    return f"{finding.get('osv','?')} module={vulnerable.get('module','?')} package={vulnerable.get('package','?')} symbol={vulnerable.get('function','?')} fixed={finding.get('fixed_version') or '<none>'}"

def append_summary(lines:list[str])->None:
    summary=os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary:return
    with Path(summary).open("a",encoding="utf-8") as handle:
        handle.write("\n### Go vulnerability reachability gate\n")
        for line in lines:handle.write(f"- {line}\n")

def main()->int:
    if len(sys.argv)!=2: print("usage: check_govuln_gate.py <govulncheck-json>",file=sys.stderr); return 2
    try:
        with Path(sys.argv[1]).open("r",encoding="utf-8") as handle: go_version,allowed,blocked=classify(handle)
    except (OSError,ValueError,json.JSONDecodeError) as exc: print(f"govuln gate parser failure: {exc}",file=sys.stderr); return 2
    if blocked:
        lines=[f"BLOCKED on {go_version}: {finding_label(item)}" for item in blocked]; append_summary(lines)
        for line in lines: print(line,file=sys.stderr)
        return 1
    if allowed:
        lines=[f"TEMPORARY EXCEPTION on {go_version}: {finding_label(item)}" for item in allowed]; append_summary(lines)
        for line in lines: print(f"::warning::{line}")
        return 0
    append_summary([f"PASS on {go_version}: no symbol-reachable vulnerabilities"]); print(f"govuln gate PASS on {go_version}: no symbol-reachable vulnerabilities"); return 0

if __name__ == "__main__": raise SystemExit(main())

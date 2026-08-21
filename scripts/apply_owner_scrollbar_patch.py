#!/usr/bin/env python3
"""One-shot scrollbar layout remediation for #281. Removed before merge."""
from pathlib import Path

path = Path("backend/internal/ownerweb/dashboard.html")
text = path.read_text(encoding="utf-8")
old = '.main{overflow:auto}'
new = '.main{overflow:auto;scrollbar-width:none}.main::-webkit-scrollbar,html::-webkit-scrollbar{display:none}'
if text.count(old) != 1:
    raise SystemExit(f"expected exactly one main overflow rule, got {text.count(old)}")
text = text.replace(old, new, 1)
old = '*{box-sizing:border-box}html,body{margin:0;min-height:100%;background:var(--bg);color:var(--text);font-family:var(--sans)}'
new = '*{box-sizing:border-box}html{scrollbar-width:none}html,body{margin:0;min-height:100%;background:var(--bg);color:var(--text);font-family:var(--sans)}'
if text.count(old) != 1:
    raise SystemExit(f"expected exactly one root box rule, got {text.count(old)}")
path.write_text(text.replace(old, new, 1), encoding="utf-8")
print("scrollbar layout remediation applied")

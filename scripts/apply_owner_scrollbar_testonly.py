#!/usr/bin/env python3
from pathlib import Path

html = Path('backend/internal/ownerweb/dashboard.html')
text = html.read_text(encoding='utf-8')
repls = [
    ('*{box-sizing:border-box}html{scrollbar-width:none}html,body{', '*{box-sizing:border-box}html,body{'),
    ('.main{overflow:auto;scrollbar-width:none}.main::-webkit-scrollbar,html::-webkit-scrollbar{display:none}.top{display:none}', '.main{overflow:auto}.top{display:none}'),
]
for old, new in repls:
    if text.count(old) != 1:
        raise SystemExit(f'dashboard expected one match: {old!r}, got {text.count(old)}')
    text = text.replace(old, new)
html.write_text(text, encoding='utf-8')

browser = Path('backend/internal/ownerweb/dashboard_browser_test.go')
text = browser.read_text(encoding='utf-8')
old = '"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--window-size=1280,900",'
new = '"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--hide-scrollbars", "--window-size=1280,900",'
if text.count(old) != 1:
    raise SystemExit(f'browser expected one chrome args match, got {text.count(old)}')
text = text.replace(old, new)
browser.write_text(text, encoding='utf-8')

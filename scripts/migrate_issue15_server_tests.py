#!/usr/bin/env python3
from pathlib import Path

path = Path("backend/internal/server/server_test.go")
text = path.read_text()

old_import = '\t"companion-server/internal/agent"\n'
if text.count(old_import) != 1:
    raise SystemExit(f"agent test import count={text.count(old_import)}")
text = text.replace(old_import, "", 1)

name = "func TestExpenseBudgetFullE2EThroughQwenRegistryAndUI"
start = text.find(name)
if start < 0:
    raise SystemExit("legacy Qwen server E2E test not found")
next_func = text.find("\nfunc ", start + len(name))
if next_func < 0:
    raise SystemExit("next test after legacy Qwen E2E not found")
text = text[:start] + text[next_func + 1:]

old_server = "service := New(pipeline.Components{"
if text.count(old_server) != 5:
    raise SystemExit(f"remaining old server constructors={text.count(old_server)} want=5")
text = text.replace(old_server, "service := newTestServer(pipeline.Components{")

old_dial = "websocket.Dial("
if text.count(old_dial) != 4:
    raise SystemExit(f"remaining raw websocket dials={text.count(old_dial)} want=4")
text = text.replace(old_dial, "testWebsocketDial(")

path.write_text(text)

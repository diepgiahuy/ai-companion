#!/usr/bin/env python3
from pathlib import Path

# Remove the legacy Qwen server E2E and imports used only by that test.
server = Path("backend/internal/server/server_test.go")
text = server.read_text()
for imp in [
    '\t"companion-server/internal/agent"\n',
    '\t"companion-server/internal/capability"\n',
    '\tconversationctx "companion-server/internal/conversation"\n',
    '\tconversationprovider "companion-server/internal/providers/conversation"\n',
    '\tresourceprovider "companion-server/internal/providers/resources"\n',
    '\ttoolprovider "companion-server/internal/providers/tools"\n',
]:
    if text.count(imp) != 1:
        raise SystemExit(f"server import guard failed count={text.count(imp)}: {imp!r}")
    text = text.replace(imp, "", 1)
start = text.find("func TestExpenseBudgetFullE2EThroughQwenRegistryAndUI")
end = text.find("func TestOTAManifestPublishAndDeviceCompatibility", start)
if start < 0 or end < 0:
    raise SystemExit("legacy Qwen E2E boundary not found")
text = text[:start] + text[end:]
for stale in ["agent.NewQwen", "Qwen3-4B-Instruct-2507", "conversationprovider.NewSQLite"]:
    if stale in text:
        raise SystemExit(f"stale server Qwen reference remains: {stale}")
server.write_text(text)

# Keep HostToolExecutor tests, but make their argument fixtures test-local and
# remove the obsolete four-tool rollout-set test.
test = Path("backend/internal/adkbridge/host_tools_test.go")
t = test.read_text()
anchor = "type captureTool struct {\n"
fixtures = '''type budgetArgsFixture struct {\n\tPeriod string `json:"period"`\n}\n\ntype timerArgsFixture struct {\n\tTitle        string `json:"title,omitempty"`\n\tDelaySeconds int64  `json:"delay_seconds"`\n}\n\n'''
if t.count(anchor) != 1:
    raise SystemExit("host-tools fixture anchor guard failed")
t = t.replace(anchor, fixtures + anchor, 1)
for old, new in [
    ("BudgetGetArgs{", "budgetArgsFixture{"),
    ("var args BudgetGetArgs", "var args budgetArgsFixture"),
    ("TimerCreateArgs{", "timerArgsFixture{"),
]:
    t = t.replace(old, new)
start = t.find("func TestRepresentativeToolNamesReturnsCopy")
end = t.find("type denyAuthorizer struct{}", start)
if start < 0 or end < 0:
    raise SystemExit("representative rollout test boundary not found")
t = t[:start] + t[end:]
for stale in ["RepresentativeToolNames", "BudgetGetArgs", "TimerCreateArgs"]:
    if stale in t:
        raise SystemExit(f"stale rollout fixture remains: {stale}")
test.write_text(t)

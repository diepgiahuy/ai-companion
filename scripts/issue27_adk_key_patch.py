#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

host = ROOT / "backend/internal/adkbridge/host_tools.go"
text = host.read_text(encoding="utf-8")
old = '''// ToolExecutionKey is stable for an ADK function call and scoped far enough to
// survive device reconnect/reboot turn-id reuse. FunctionCallID is supplied by
// ADK and is the natural idempotency token for tool retries within an
// invocation.
func ToolExecutionKey(turn pipeline.TurnContext, functionCallID, toolName string) string {
	return "adk:" + tupleDigest(
		"user", strings.TrimSpace(turn.UserID),
		"thread", strings.TrimSpace(turn.ThreadID),
		"device", strings.TrimSpace(turn.DeviceID),
		"session", strings.TrimSpace(turn.SessionID),
		"turn", strings.TrimSpace(turn.TurnID),
		"function_call", strings.TrimSpace(functionCallID),
		"tool", strings.TrimSpace(toolName),
	)
}
'''
new = '''// ToolExecutionKey is the durable client key supplied by the ADK adapter to the
// domain idempotency contract. It deliberately excludes device/session/turn
// nonces: the same ADK function call retried after reconnect or process restart
// must address the same durable record. Authenticated actor isolation is
// enforced separately by the ledger. Thread keeps reused call IDs from unrelated
// conversations from sharing a key.
func ToolExecutionKey(turn pipeline.TurnContext, functionCallID, toolName string) string {
	return "adk:" + tupleDigest(
		"thread", strings.TrimSpace(turn.ThreadID),
		"function_call", strings.TrimSpace(functionCallID),
		"tool", strings.TrimSpace(toolName),
	)
}
'''
if text.count(old) != 1:
    raise SystemExit("host_tools.go key scope drifted")
host.write_text(text.replace(old, new), encoding="utf-8")

test = ROOT / "backend/internal/adkbridge/host_tools_test.go"
t = test.read_text(encoding="utf-8")
old_test = '''func TestSessionIdentityAndToolKeyAreReconnectSafe(t *testing.T) {
	turnA := pipeline.TurnContext{UserID: "u", ThreadID: "t", DeviceID: "d", SessionID: "boot-a", TurnID: "1"}
	turnB := turnA
	turnB.SessionID = "boot-b"
	ua, sa := SessionIdentity(turnA)
	ub, sb := SessionIdentity(turnB)
	if ua != ub || sa == sb {
		t.Fatalf("unexpected identities: %q/%q vs %q/%q", ua, sa, ub, sb)
	}
	if ToolExecutionKey(turnA, "call", ToolTimerCreate) == ToolExecutionKey(turnB, "call", ToolTimerCreate) {
		t.Fatal("idempotency key must differ across server/device session nonce")
	}
}
'''
new_test = '''func TestSessionIdentityAndToolKeyAreReconnectSafe(t *testing.T) {
	turnA := pipeline.TurnContext{UserID: "u", ThreadID: "t", DeviceID: "d-a", SessionID: "boot-a", TurnID: "1"}
	turnB := turnA
	turnB.DeviceID = "d-b"
	turnB.SessionID = "boot-b"
	turnB.TurnID = "99"
	ua, sa := SessionIdentity(turnA)
	ub, sb := SessionIdentity(turnB)
	if ua != ub || sa == sb {
		t.Fatalf("unexpected identities: %q/%q vs %q/%q", ua, sa, ub, sb)
	}
	if ToolExecutionKey(turnA, "call", ToolTimerCreate) != ToolExecutionKey(turnB, "call", ToolTimerCreate) {
		t.Fatal("same function call must keep one durable key across device/session/turn changes")
	}
}

func TestToolExecutionKeyLeavesActorScopingToDurableLedger(t *testing.T) {
	a := pipeline.TurnContext{UserID: "user-a", ThreadID: "shared-thread", DeviceID: "device-a", SessionID: "s-a", TurnID: "1"}
	b := pipeline.TurnContext{UserID: "user-b", ThreadID: "shared-thread", DeviceID: "device-b", SessionID: "s-b", TurnID: "2"}
	if ToolExecutionKey(a, "same-client-call", ToolTimerCreate) != ToolExecutionKey(b, "same-client-call", ToolTimerCreate) {
		t.Fatal("client key must not encode actor identity; actor isolation belongs to the durable ledger")
	}
}
'''
if t.count(old_test) != 1:
    raise SystemExit("host_tools_test reconnect contract drifted")
t = t.replace(old_test, new_test)
old_collision = '''func TestToolExecutionKeyCanonicalTupleHasNoDelimiterCollision(t *testing.T) {
	a := pipeline.TurnContext{UserID: "a:b", ThreadID: "c", DeviceID: "d", SessionID: "s", TurnID: "t"}
	b := pipeline.TurnContext{UserID: "a", ThreadID: "b:c", DeviceID: "d", SessionID: "s", TurnID: "t"}
	ka := ToolExecutionKey(a, "call", ToolBudgetGet)
	kb := ToolExecutionKey(b, "call", ToolBudgetGet)
'''
new_collision = '''func TestToolExecutionKeyCanonicalTupleHasNoDelimiterCollision(t *testing.T) {
	a := pipeline.TurnContext{ThreadID: "a:b"}
	b := pipeline.TurnContext{ThreadID: "a"}
	ka := ToolExecutionKey(a, "c", ToolBudgetGet)
	kb := ToolExecutionKey(b, "b:c", ToolBudgetGet)
'''
if t.count(old_collision) != 1:
    raise SystemExit("host_tools_test collision contract drifted")
t = t.replace(old_collision, new_collision)
t = t.replace('ToolExecutionKey(a, "call", ToolBudgetGet)', 'ToolExecutionKey(a, "c", ToolBudgetGet)', 1)
test.write_text(t, encoding="utf-8")
print("ADK tool idempotency key scope patched")

#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

fake = ROOT / "host/companion_software_device/fake_responses.py"
text = fake.read_text(encoding="utf-8")
anchor = '''    "Tier1 memory": {
        "id": "memory",
        "tool": "memory.remember",
        "args": {
            "key": "preferred_language",
            "kind": "semantic",
            "value": "Vietnamese",
            "valid_from": "2026-08-13T15:20:00+07:00",
        },
    },
'''
extra = '''    "Tier1 idempotency original": {
        "id": "idempotency",
        "tool": "expense.log",
        "args": {"items": [{"amount_vnd": 77000, "category": "food", "description": "tier1 idempotency original", "occurred_at": "2026-08-14T04:00:00+07:00"}]},
        "final_text": "Tier-1 idempotency original ok",
    },
    "Tier1 idempotency replay": {
        "id": "idempotency",
        "tool": "expense.log",
        "args": {"items": [{"amount_vnd": 77000, "category": "food", "description": "tier1 idempotency original", "occurred_at": "2026-08-13T21:00:00Z"}]},
        "final_text": "Tier-1 idempotency replay ok",
    },
    "Tier1 idempotency conflict": {
        "id": "idempotency",
        "tool": "expense.log",
        "args": {"items": [{"amount_vnd": 88000, "category": "food", "description": "tier1 idempotency original", "occurred_at": "2026-08-13T21:00:00Z"}]},
        "expect_conflict": True,
        "final_text": "Tier-1 idempotency conflict ok",
    },
    "Tier1 idempotency concurrent": {
        "id": "idempotency_concurrent",
        "tool": "expense.log",
        "args": {"items": [{"amount_vnd": 99000, "category": "food", "description": "tier1 idempotency concurrent", "occurred_at": "2026-08-14T04:05:00+07:00"}]},
        "final_text": "Tier-1 idempotency concurrent ok",
    },
'''
if text.count(anchor) != 1:
    raise SystemExit("fake_responses CASES anchor drifted")
text = text.replace(anchor, anchor + extra)

old_fn = '''def successful_function_output(payload, call_id):
    for item in walk(payload.get("input", [])):
        if not isinstance(item, dict) or item.get("type") != "function_call_output":
            continue
        if item.get("call_id") != call_id:
            continue
        output = item.get("output")
        if not isinstance(output, str):
            continue
        try:
            decoded = json.loads(output)
        except json.JSONDecodeError:
            continue
        if isinstance(decoded, dict) and decoded.get("ok") is True:
            return True
    return False
'''
new_fn = '''def function_output(payload, call_id):
    for item in walk(payload.get("input", [])):
        if not isinstance(item, dict) or item.get("type") != "function_call_output":
            continue
        if item.get("call_id") != call_id:
            continue
        output = item.get("output")
        if not isinstance(output, str):
            continue
        try:
            decoded = json.loads(output)
        except json.JSONDecodeError:
            continue
        if isinstance(decoded, dict):
            return decoded
    return None
'''
if text.count(old_fn) != 1:
    raise SystemExit("fake_responses output helper drifted")
text = text.replace(old_fn, new_fn)

old_dispatch = '''            call_id = f"call_{case['id']}_1"
            if successful_function_output(payload, call_id):
                events = text_events(response_id, f"Tier-1 tool parity ok: {case['id']}")
            else:
                events = function_call_events(response_id, case)
'''
new_dispatch = '''            call_id = f"call_{case['id']}_1"
            output = function_output(payload, call_id)
            if output is None:
                events = function_call_events(response_id, case)
            elif case.get("expect_conflict"):
                if output.get("ok") is not False or "IDEMPOTENCY_CONFLICT" not in str(output.get("error", "")):
                    self.send_error(409, f"expected IDEMPOTENCY_CONFLICT, got {output!r}")
                    return
                events = text_events(response_id, case["final_text"])
            elif output.get("ok") is True:
                events = text_events(response_id, case.get("final_text", f"Tier-1 tool parity ok: {case['id']}"))
            else:
                self.send_error(409, f"unexpected failed tool output: {output!r}")
                return
'''
if text.count(old_dispatch) != 1:
    raise SystemExit("fake_responses dispatch drifted")
fake.write_text(text.replace(old_dispatch, new_dispatch), encoding="utf-8")

run = ROOT / "host/companion_software_device/run_e2e.sh"
r = run.read_text(encoding="utf-8")
anchor = '''# Verify revoke lifecycle after all tool scenarios. The old credential must no
# longer authorize a WebSocket upgrade.
'''
block = r'''# Durable idempotency evidence uses a dedicated authoritative database. The
# same ADK function_call_id is retried across backend restarts, then reused with
# a conflicting semantic payload. Finally two real software-device sessions race
# the same equivalent request concurrently.
IDEM_DB="$TMP/idempotency.db"
IDEM_DEVICE="software-device-idempotency"
IDEM_USER="tier1-idempotency-user"
IDEM_CREDENTIAL=""
IDEM_ORIGINAL="${OUT%.json}-idempotency-original.json"
IDEM_REPLAY="${OUT%.json}-idempotency-replay.json"
IDEM_CONFLICT="${OUT%.json}-idempotency-conflict.json"
IDEM_CONCURRENT_A="${OUT%.json}-idempotency-concurrent-a.json"
IDEM_CONCURRENT_B="${OUT%.json}-idempotency-concurrent-b.json"
IDEM_DB_EVIDENCE="${OUT%.json}-idempotency-db.json"

run_idempotency_turn() {
  transcript="$1"
  expected="$2"
  evidence="$3"
  export MOCK_TRANSCRIPT="$transcript"
  "$BUILD/companion_software_device" \
    --url ws://127.0.0.1:18000/v2/device \
    --device-id "$IDEM_DEVICE" \
    --token "$IDEM_CREDENTIAL" \
    --admin-token "$ADMIN_TOKEN" \
    --expected-text "$expected" \
    --scenario-set tool \
    --evidence "$evidence"
  python3 host/companion_software_device/validate_evidence.py "$evidence"
}

export COMPANION_DATABASE="$IDEM_DB"
export COMPANION_OBSERVABILITY_FILE="${OUT%.json}-idempotency-original-observability.json"
export MOCK_TRANSCRIPT="Tier1 idempotency original"
start_server
IDEM_CREDENTIAL="$(enroll_device "$IDEM_DEVICE" "$IDEM_USER")"
run_idempotency_turn "Tier1 idempotency original" "Tier-1 idempotency original ok" "$IDEM_ORIGINAL"
stop_server

# Restart the backend on the same DB. New session/turn IDs must still address
# the original durable record because the ADK client key is reconnect-stable.
export COMPANION_OBSERVABILITY_FILE="${OUT%.json}-idempotency-replay-observability.json"
start_server
run_idempotency_turn "Tier1 idempotency replay" "Tier-1 idempotency replay ok" "$IDEM_REPLAY"
stop_server

# Same actor/operation/key but different amount must fail before mutation.
export COMPANION_OBSERVABILITY_FILE="${OUT%.json}-idempotency-conflict-observability.json"
start_server
run_idempotency_turn "Tier1 idempotency conflict" "Tier-1 idempotency conflict ok" "$IDEM_CONFLICT"
stop_server

# Two sessions concurrently send one equivalent request using the same durable
# key. Both calls must complete while the DB contains exactly one mutation.
export COMPANION_OBSERVABILITY_FILE="${OUT%.json}-idempotency-concurrent-observability.json"
export MOCK_TRANSCRIPT="Tier1 idempotency concurrent"
start_server
"$BUILD/companion_software_device" --url ws://127.0.0.1:18000/v2/device --device-id "$IDEM_DEVICE" --token "$IDEM_CREDENTIAL" --admin-token "$ADMIN_TOKEN" --expected-text "Tier-1 idempotency concurrent ok" --scenario-set tool --evidence "$IDEM_CONCURRENT_A" &
IDEM_PID_A=$!
"$BUILD/companion_software_device" --url ws://127.0.0.1:18000/v2/device --device-id "$IDEM_DEVICE" --token "$IDEM_CREDENTIAL" --admin-token "$ADMIN_TOKEN" --expected-text "Tier-1 idempotency concurrent ok" --scenario-set tool --evidence "$IDEM_CONCURRENT_B" &
IDEM_PID_B=$!
wait "$IDEM_PID_A"
wait "$IDEM_PID_B"
python3 host/companion_software_device/validate_evidence.py "$IDEM_CONCURRENT_A"
python3 host/companion_software_device/validate_evidence.py "$IDEM_CONCURRENT_B"
stop_server

python3 host/companion_software_device/verify_idempotency_db.py "$IDEM_DB" "$IDEM_DB_EVIDENCE"

'''
if r.count(anchor) != 1:
    raise SystemExit("run_e2e idempotency insertion anchor drifted")
run.write_text(r.replace(anchor, block + anchor), encoding="utf-8")
print("Tier-1 durable idempotency evidence patch applied")

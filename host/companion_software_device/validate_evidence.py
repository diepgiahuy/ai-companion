#!/usr/bin/env python3
import json
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
if data.get("schema_version") != 1:
    raise SystemExit("software-device evidence: bad schema_version")
if data.get("evidence_class") != "tier1_orchestration":
    raise SystemExit("software-device evidence: wrong evidence class")
if data.get("promotion") != "orchestration_only":
    raise SystemExit("software-device evidence: mock run must remain orchestration_only")
if data.get("protocol") != "v2" or data.get("device_fsm") != "production_companion_app":
    raise SystemExit("software-device evidence: wrong protocol/FSM identity")
if data.get("providers") != {"asr": "mock", "agent": "mock", "tts": "mock"}:
    raise SystemExit("software-device evidence: test-provider identity drifted")
if data.get("result") != "passed":
    raise SystemExit("software-device evidence: scenario set failed")
if not re.fullmatch(r"[0-9a-f]{64}", data.get("backend_config_sha256", "")):
    raise SystemExit("software-device evidence: config fingerprint is not SHA-256")
required = {
    "hello_turn_tts", "duplicate_message_id", "barge_in_generation",
    "reconnect_new_session", "config_update_report", "protocol_v1_rejected",
}
scenarios = data.get("scenarios")
if not isinstance(scenarios, list):
    raise SystemExit("software-device evidence: scenarios must be an array")
by_id = {item.get("id"): item for item in scenarios if isinstance(item, dict)}
if set(by_id) != required:
    raise SystemExit(f"software-device evidence: scenario ids {set(by_id)!r} != {required!r}")
for scenario_id, item in by_id.items():
    if item.get("result") != "passed":
        raise SystemExit(f"software-device evidence: {scenario_id} did not pass")
    if not isinstance(item.get("elapsed_ms"), int) or item["elapsed_ms"] < 0:
        raise SystemExit(f"software-device evidence: {scenario_id} missing elapsed_ms")
print("SOFTWARE DEVICE EVIDENCE PASS: Tier-1 orchestration only; no physical/provider promotion")

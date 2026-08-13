#!/usr/bin/env python3
import json
from pathlib import Path

status_path = Path("evidence/status.json")
status = json.loads(status_path.read_text(encoding="utf-8"))
gates = status["gates"]
if "software_device_tier1" in gates or "wokwi_tier2" in gates:
    raise SystemExit("Tier-1/Wokwi evidence gates already exist")
if gates["protocol_v2_negotiation"]["status"] != "unproven":
    raise SystemExit("protocol_v2_negotiation status drifted")

status["updated_at"] = "2026-08-13T15:05:00+07:00"
gates["software_device_tier1"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [
        {
            "kind": "tier1_orchestration",
            "workflow": "software-device-e2e",
            "run_id": 31680332414,
            "source_head": "c1dedaa6e6945d560fb7dc6c990da6ee34734d2a",
            "tested_merge_commit": "f8a6258111c85d11441ba5cd0644bc6774a1a198",
            "artifact_id": 9173249381,
            "artifact_digest": "sha256:82889dae3ca92288495af54a0fe8bb3a675e88f5a9e3ac047da4677aaf4c8bf1",
            "providers": {"asr": "mock", "agent": "mock", "tts": "mock"},
            "promotion": "orchestration_only",
            "assertion": "production CompanionApp + protocol v2 passed hello/turn/TTS, duplicate message_id, barge-in, reconnect/new-session, live config/report, and v1-rejection scenarios against real companiond boundary"
        }
    ],
    "reason": "Tier-1 proves orchestration over the real backend boundary with mock providers; it is deliberately not real-provider or physical-device evidence."
}
gates["wokwi_tier2"] = {
    "status": "unproven",
    "production_ready": False,
    "evidence": [],
    "reason": "No repository Wokwi firmware scenario has produced token-backed simulation evidence yet. Do not infer simulator coverage from host/Tier-1 CI."
}
gates["protocol_v2_negotiation"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [
        {
            "kind": "tier1_orchestration",
            "workflow": "software-device-e2e",
            "run_id": 31680332414,
            "source_head": "c1dedaa6e6945d560fb7dc6c990da6ee34734d2a",
            "tested_merge_commit": "f8a6258111c85d11441ba5cd0644bc6774a1a198",
            "assertion": "v2 session/turn path succeeds and a v1 client is deterministically rejected with unsupported_protocol_version"
        }
    ],
    "reason": "Canonical v2 behavior is now proven at Tier-1 with the production device FSM, but physical-device/network evidence remains pending."
}
status_path.write_text(json.dumps(status, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

readme_path = Path("README.md")
readme = readme_path.read_text(encoding="utf-8")
old_row = "| Canonical protocol v2 | 🟡 implemented | Backend, firmware, host tests and golden fixture use one v2 envelope; physical-device evidence remains pending |"
new_rows = """| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2; six mock-provider orchestration scenarios pass; no provider/physical promotion |
| Canonical protocol v2 | 🟡 partial | Backend, firmware and host share v2; Tier-1 proves v2 session/turn flow plus deterministic v1 rejection; physical-device evidence remains pending |
| Wokwi targeted firmware simulation | ⚪ unproven | Tier-2 is defined in the evidence ladder, but no repository simulation run is recorded yet |"""
if readme.count(old_row) != 1:
    raise SystemExit("README canonical protocol row drifted")
readme = readme.replace(old_row, new_rows, 1)

old_bullet = "- **Physical HIL workflow** — fail-closed ESP-IDF build/flash/serial test using `pytest-embedded`; it never falls back to a mock result and runs only when a maintainer manually selects a trusted ref and explicit device port."
new_bullets = old_bullet + "\n- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; mock-provider evidence is classified `orchestration_only`, never real voice evidence."
if readme.count(old_bullet) != 1:
    raise SystemExit("README HIL bullet drifted")
readme = readme.replace(old_bullet, new_bullets, 1)

old_map = "- [`docs/TEST_PLAN.md`](docs/TEST_PLAN.md) — test tiers and verification plan."
new_map = old_map + "\n- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — Tier 0/1/2/3 evidence classes, promotion limits and current software-device/Wokwi boundaries."
if readme.count(old_map) != 1:
    raise SystemExit("README repository map drifted")
readme = readme.replace(old_map, new_map, 1)
readme_path.write_text(readme, encoding="utf-8")

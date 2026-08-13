#!/usr/bin/env python3
import json
from pathlib import Path

status_path = Path("evidence/status.json")
status = json.loads(status_path.read_text(encoding="utf-8"))
gates = status["gates"]
if gates.get("software_device_tier1", {}).get("status") != "partial":
    raise SystemExit("software_device_tier1 checkpoint drifted")
if gates.get("wokwi_tier2", {}).get("status") != "unproven":
    raise SystemExit("wokwi_tier2 checkpoint drifted")

status["updated_at"] = "2026-08-13T15:18:09+07:00"
latest_tier1 = {
    "kind": "tier1_orchestration",
    "workflow": "software-device-e2e",
    "run_id": 31681331610,
    "source_head": "158f274967601e135d06632b21875947319b343b",
    "tested_merge_commit": "411c969efcf00d39fa6505f59f431e189103df39",
    "artifact_id": 9173619212,
    "artifact_digest": "sha256:914832be799e05ee60e78a580b3efb2369374d65bb47f33a4231c712584dbdba",
    "promotion": "orchestration_only",
}
gates["software_device_tier1"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [
        latest_tier1 | {
            "providers": {"asr": "mock", "agent": "mock", "tts": "mock"},
            "assertion": "production CompanionApp + protocol v2 passed hello/turn/TTS, duplicate message_id, barge-in, reconnect/new-session, live config/report, and v1-rejection scenarios against real companiond boundary",
        },
        latest_tier1 | {
            "providers": {"asr": "mock", "agent": "fake_model", "tts": "mock"},
            "assertion": "production legacy agent + ToolRegistry + SQLite path produced exactly one authoritative expense.create mutation (50000 VND) and returned the tool result through the same device FSM",
        },
    ],
    "reason": "Tier-1 now proves core orchestration plus an authoritative tool/store mutation over the real backend boundary, but mock ASR/TTS and deterministic fake-model evidence cannot promote real-provider or physical-device gates.",
}
gates["protocol_v2_negotiation"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [latest_tier1 | {
        "assertion": "v2 session/turn path succeeds and a v1 client is deterministically rejected with unsupported_protocol_version",
    }],
    "reason": "Canonical v2 behavior is proven at Tier-1 with the production device FSM; physical-device/network evidence remains pending.",
}
gates["wokwi_tier2"] = {
    "status": "blocked",
    "production_ready": False,
    "evidence": [{
        "kind": "wokwi_unavailable",
        "workflow": "wokwi-tier2",
        "run_id": 31681331692,
        "source_head": "158f274967601e135d06632b21875947319b343b",
        "tested_merge_commit": "411c969efcf00d39fa6505f59f431e189103df39",
        "artifact_id": 9173574248,
        "artifact_digest": "sha256:c07a8cc4e3f48b3b0c0653b007490f6b43aa25e89675701914b7fe8e2b286cae",
        "blocker_code": "missing_wokwi_cli_token",
        "simulation_ran": False,
        "promotion": "none",
    }],
    "reason": "Trusted same-repository GitHub Actions probe found WOKWI_CLI_TOKEN unavailable. No simulation ran and no Wokwi PASS is claimed; add the secret before attempting a quota-consuming Tier-2 scenario.",
}
status_path.write_text(json.dumps(status, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

readme_path = Path("README.md")
readme = readme_path.read_text(encoding="utf-8")
replacements = {
    "| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2; six mock-provider orchestration scenarios pass; no provider/physical promotion |":
    "| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2; six core scenarios plus one deterministic agent→tool→SQLite mutation pass; no provider/physical promotion |",
    "| Wokwi targeted firmware simulation | ⚪ unproven | Tier-2 is defined in the evidence ladder, but no repository simulation run is recorded yet |":
    "| Wokwi targeted firmware simulation | ⛔ blocked | Trusted Actions probe records `UNAVAILABLE`: `WOKWI_CLI_TOKEN` is not configured; no simulation ran and no PASS is claimed |",
    "- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; mock-provider evidence is classified `orchestration_only`, never real voice evidence.":
    "- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config plus a deterministic production agent→tool→SQLite mutation, while all mock/fake-provider evidence remains `orchestration_only`.",
}
for old, new in replacements.items():
    if readme.count(old) != 1:
        raise SystemExit(f"README target drifted: {old[:72]}")
    readme = readme.replace(old, new, 1)
readme_path.write_text(readme, encoding="utf-8")

doc_path = Path("docs/TEST_EVIDENCE_LADDER.md")
doc = doc_path.read_text(encoding="utf-8")
old_tier1 = "Current mandatory scenarios are session/turn/TTS, duplicate live-session message identity, barge-in generation cancellation, disconnect/reconnect with a new session, live config/report ordering, and deterministic protocol-v1 rejection. Synthetic microphone PCM and bounded speaker/display sinks require no host audio hardware."
new_tier1 = old_tier1 + " A second deterministic run switches only the model to an OpenAI-compatible fixture and proves the production legacy-agent/ToolRegistry/SQLite path creates exactly one expected `expense.create` mutation; it is still non-production provider evidence."
if doc.count(old_tier1) != 1:
    raise SystemExit("Tier-1 doc paragraph drifted")
doc = doc.replace(old_tier1, new_tier1, 1)
old_wokwi = "No Wokwi PASS exists in this repository yet. The current product composition uses physical I2S/audio/display paths that must not be silently replaced just to manufacture a green simulator result. The first targeted Wokwi PR must either prove a minimal real-firmware boot/network/protocol scenario using a clearly simulation-only board adapter where necessary, or emit `UNAVAILABLE` with the exact token/capability blocker. Wokwi never promotes I2S acoustic, BLE/RSSI/RF, final display timing, current, thermal or enclosure claims."
new_wokwi = old_wokwi + " The trusted same-repository capability probe on workflow run `31681331692` recorded `UNAVAILABLE` with blocker `missing_wokwi_cli_token`; `simulation_ran=false`, so this satisfies only the fail-closed availability branch of #22 and must be replaced by real Tier-2 evidence after the secret is configured."
if doc.count(old_wokwi) != 1:
    raise SystemExit("Wokwi doc paragraph drifted")
doc = doc.replace(old_wokwi, new_wokwi, 1)
doc_path.write_text(doc, encoding="utf-8")

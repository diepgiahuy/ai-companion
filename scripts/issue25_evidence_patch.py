#!/usr/bin/env python3
import json
from pathlib import Path

path = Path(__file__).resolve().parents[1] / "evidence" / "status.json"
data = json.loads(path.read_text(encoding="utf-8"))
assert data.get("schema_version") == 1
gates = data["gates"]
assert "webrtc_real_network" in gates, "expected dead WebRTC gate before migration"
legacy = gates["software_device_tier1"]["evidence"]
assert any((item.get("providers") or {}).get("agent") in {"mock", "fake_model"} for item in legacy), "expected legacy Tier-1 evidence before migration"
assert "observability_contract" not in gates, "observability gate already exists"

data["updated_at"] = "2026-08-13T20:38:39+07:00"

gates["go_1_26_5_race_e2e"] = {
    "status": "passed",
    "evidence": [{
        "kind": "github_actions",
        "run_id": 31705735289,
        "commit": "93b7132dd709d5603b14dec239c3fae69ffee5d2",
        "workflow": "production-e2e",
        "assertion": "exact Go 1.26.5 production dependency E2E passed on the observability PR head",
    }],
}

gates["module_reproducibility"] = {
    "status": "passed",
    "evidence": [
        {
            "kind": "manual_real_environment",
            "environment": "Docker Go 1.26.5 on Apple Silicon",
            "assertion": "go mod tidy produced zero diff after dependency lock; go mod verify passed",
        },
        {
            "kind": "github_actions",
            "run_id": 31705735382,
            "commit": "93b7132dd709d5603b14dec239c3fae69ffee5d2",
            "workflow": "module-lock",
            "assertion": "Go 1.26.5 module tidy/verify gate passed on the observability PR head",
        },
    ],
}

gates["dependency_vulnerability_scan"] = {
    "status": "passed",
    "evidence": [
        {"kind": "manual_real_environment", "tool": "govulncheck v1.6.0", "assertion": "0 called vulnerabilities"},
        {
            "kind": "github_actions",
            "run_id": 31696472492,
            "commit": "7089bdad4a73fcc66ce225fc7a3495bdc542f9ed",
            "workflow": "quality-security",
            "assertion": "post-merge main module reproducibility, vet/race, govulncheck, evidence and diff-hygiene gates passed after the ADK/auth hard cut",
        },
    ],
}

gates["codeql_go"] = {
    "status": "passed",
    "evidence": [{
        "kind": "github_actions",
        "run_id": 31705735256,
        "commit": "93b7132dd709d5603b14dec239c3fae69ffee5d2",
        "workflow": "codeql",
        "assertion": "CodeQL Go traced build and analysis passed with adk,mcp,nolibopusfile production adapter tags",
    }],
}

del gates["webrtc_real_network"]

gates["real_llm_tool_quality"]["reason"] = "Deterministic ADK Responses tests exist but real-model task success, tool selection and argument accuracy have not been measured."
gates["idempotency_payload_conflict"]["reason"] = "Current persistence semantics do not yet prove actor-scoped same-key/same-payload replay, same-key/different-payload conflict detection and concurrency/restart behavior. Issue #27 owns this gate."
gates["postgres_migrations_jobs"]["reason"] = "PostgreSQL/Atlas/pgx/River migration, crash/restart and backup/restore gates are not yet complete on main."
gates["mcp_external_integration"]["reason"] = "Official MCP Go SDK helper/adapter code exists behind the Companion ToolRegistry/policy boundary, but product startup wiring and real external MCP interoperability/security tests are pending in #19."
gates["security_default_deny"]["reason"] = "Database-enrolled device credentials are fail-closed, but deployment origin/TLS policy, explicit privacy defaults and adversarial integration evidence remain owned by #24."
gates["privacy_explicit_consent"]["reason"] = "Explicit voice-retention/long-term-memory opt-in, revocation and deletion behavior still require end-to-end proof in #24."

artifact = {
    "kind": "tier1_orchestration",
    "workflow": "software-device-e2e",
    "run_id": 31705735317,
    "source_head": "93b7132dd709d5603b14dec239c3fae69ffee5d2",
    "tested_merge_commit": "7a2a7f44c75694bc311a9b4520e815f8b47803b8",
    "artifact_id": 9183141099,
    "artifact_digest": "sha256:3192e4927d5a860d0da325eebc02752c45d62fdf6dd89bf39e88aaf1c474995d",
    "promotion": "orchestration_only",
}

gates["protocol_v2_negotiation"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [dict(artifact, assertion="protocol v2 production CompanionApp session/turn flow succeeds and a v1 client is deterministically rejected; physical-device/network evidence remains separate")],
    "reason": "Canonical v2 behavior is proven at Tier-1 with the production device FSM; physical-device/network evidence remains pending.",
}

tier1 = dict(artifact)
tier1.update({
    "providers": {"asr": "mock", "agent": "adk_deterministic_responses", "tts": "mock"},
    "auth": "database_enrolled_per_device",
    "assertion": "production CompanionApp + protocol v2 + real companiond + ADK Responses adapter passed core FSM/reconnect/barge-in/replay/config/v1-rejection, wrong/revoked credential lifecycle, authoritative expense/budget/note/journal/reminder/timer/memory mutations, and bounded observability snapshots with zero deterministic drops",
})
gates["software_device_tier1"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [tier1],
    "reason": "Tier-1 proves canonical orchestration, enrolled-auth lifecycle, representative ADK tool/store parity and telemetry correlation, but mock ASR/TTS plus deterministic model responses cannot promote real-provider or physical-device gates.",
}

obs = dict(artifact)
obs["assertion"] = "typed session/turn/generation correlation, ASR/agent/TTS timing and all seven representative public tool outcomes validated from product recorder snapshots; deterministic Tier-1 observed zero recorder drops and forbids raw transcript/audio/tool-argument/credential fields"
gates["observability_contract"] = {
    "status": "partial",
    "production_ready": False,
    "evidence": [obs],
    "reason": "The bounded non-blocking Companion-owned observability contract is implemented and deterministically exercised, but real-provider latency distributions, deployment exporter/retention policy and physical-device resource telemetry are not production-proven.",
}

path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print("issue25 evidence truth migration applied")

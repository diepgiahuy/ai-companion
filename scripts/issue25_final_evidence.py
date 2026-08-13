#!/usr/bin/env python3
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
path = root / "evidence" / "status.json"
data = json.loads(path.read_text(encoding="utf-8"))
gates = data["gates"]

OLD_RUN = 31705735317
OLD_HEAD = "93b7132dd709d5603b14dec239c3fae69ffee5d2"
OLD_MERGE = "7a2a7f44c75694bc311a9b4520e815f8b47803b8"
OLD_ARTIFACT = 9183141099
OLD_DIGEST = "sha256:3192e4927d5a860d0da325eebc02752c45d62fdf6dd89bf39e88aaf1c474995d"

NEW_RUN = 31708039890
NEW_HEAD = "7ae065f6f8fb73cde8ee71087806d0189634406f"
NEW_MERGE = "d6b8ddcdd24c62d4291d188a20ee9bade1b90dfa"
NEW_ARTIFACT = 9184091869
NEW_DIGEST = "sha256:dca97e0b5623611bd9e0689555b581de94e738def9fb32d81599b1a07891c808"

for gate_name in ("protocol_v2_negotiation", "software_device_tier1", "observability_contract"):
    evidence = gates[gate_name]["evidence"]
    assert len(evidence) == 1, gate_name
    item = evidence[0]
    assert item["run_id"] == OLD_RUN, (gate_name, item["run_id"])
    assert item["source_head"] == OLD_HEAD, gate_name
    assert item["tested_merge_commit"] == OLD_MERGE, gate_name
    assert item["artifact_id"] == OLD_ARTIFACT, gate_name
    assert item["artifact_digest"] == OLD_DIGEST, gate_name
    item.update({
        "run_id": NEW_RUN,
        "source_head": NEW_HEAD,
        "tested_merge_commit": NEW_MERGE,
        "artifact_id": NEW_ARTIFACT,
        "artifact_digest": NEW_DIGEST,
    })

obs_assertion = gates["observability_contract"]["evidence"][0]["assertion"]
assert "forbids raw transcript" in obs_assertion
gates["observability_contract"]["evidence"][0]["assertion"] = (
    "typed session/turn/generation correlation, ASR/agent/TTS timing and all seven representative public tool outcomes validated from product recorder snapshots; "
    "deterministic Tier-1 observed zero recorder drops, pseudonymized client/runtime correlation identifiers before the Recorder boundary, and forbids raw transcript/audio/tool-argument/credential fields"
)

# Refresh exact-head non-provider CI facts already completed on the same source head.
for gate_name, run_id, workflow in (
    ("go_1_26_5_race_e2e", 31708039815, "production-e2e"),
    ("codeql_go", 31708039616, "codeql"),
):
    item = gates[gate_name]["evidence"][0]
    item["run_id"] = run_id
    item["commit"] = NEW_HEAD
    item["workflow"] = workflow

module_items = gates["module_reproducibility"]["evidence"]
ci_items = [item for item in module_items if item.get("kind") == "github_actions"]
assert len(ci_items) == 1
ci_items[0].update({"run_id": 31708039876, "commit": NEW_HEAD, "workflow": "module-lock"})

data["updated_at"] = "2026-08-13T21:05:00+07:00"
path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print("final issue25 evidence provenance applied")

#!/usr/bin/env python3
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
mode = sys.argv[2] if len(sys.argv) > 2 else "core"
expected_tool = sys.argv[3] if len(sys.argv) > 3 else None

data = json.loads(path.read_text(encoding="utf-8"))
if data.get("schema_version") != 1:
    raise SystemExit("observability: bad schema_version")
if data.get("dropped") != 0:
    raise SystemExit(f"observability: deterministic Tier-1 dropped {data.get('dropped')} events")
events = data.get("events")
if not isinstance(events, list) or not events:
    raise SystemExit("observability: events must be a non-empty array")

forbidden_keys = {
    "transcript", "arguments", "credential", "token", "user_id", "device_id",
    "tenant_id", "thread_id", "audio", "pcm", "text", "content", "error",
}

def walk(value, path="root"):
    if isinstance(value, dict):
        for key, child in value.items():
            if key in forbidden_keys:
                raise SystemExit(f"observability: forbidden private/raw field {path}.{key}")
            walk(child, f"{path}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            walk(child, f"{path}[{index}]")

walk(data)

for event in events:
    if not isinstance(event, dict) or event.get("schema_version") != 1:
        raise SystemExit("observability: malformed event")
    duration = event.get("duration_ms")
    if duration is not None and (not isinstance(duration, int) or duration < 0):
        raise SystemExit(f"observability: invalid duration {duration!r}")
    correlation = event.get("correlation") or {}
    if not isinstance(correlation, dict):
        raise SystemExit("observability: correlation must be an object")

names = [event.get("name") for event in events]
if mode == "core":
    required_names = {"session.ready", "turn.start", "turn.stage", "turn.end", "session.end"}
    missing = required_names - set(names)
    if missing:
        raise SystemExit(f"observability: core missing events {sorted(missing)}")
    stages = {event.get("stage") for event in events if event.get("name") == "turn.stage"}
    if "asr" not in stages:
        raise SystemExit("observability: core missing ASR stage")
    if not ({"agent", "agent_stream"} & stages):
        raise SystemExit("observability: core missing agent stage")
    if not ({"tts", "tts_stream"} & stages):
        raise SystemExit("observability: core missing TTS stage")
    turn_events = [event for event in events if event.get("name") in {"turn.start", "turn.stage", "turn.end"}]
    for event in turn_events:
        correlation = event.get("correlation") or {}
        if not correlation.get("session_id") or not correlation.get("turn_id"):
            raise SystemExit("observability: turn event missing session/turn correlation")
        if not isinstance(correlation.get("generation_id"), int) or correlation["generation_id"] <= 0:
            raise SystemExit("observability: turn event missing generation correlation")
elif mode == "tool":
    tool_events = [event for event in events if event.get("name") == "tool.end"]
    if not tool_events:
        raise SystemExit("observability: tool run emitted no tool.end event")
    if expected_tool and not any(event.get("tool_name") == expected_tool and event.get("outcome") == "ok" for event in tool_events):
        raise SystemExit(f"observability: expected successful tool {expected_tool!r} not observed")
    for event in tool_events:
        correlation = event.get("correlation") or {}
        if not correlation.get("session_id") or not correlation.get("turn_id"):
            raise SystemExit("observability: tool event missing turn correlation")
else:
    raise SystemExit(f"observability: unknown validation mode {mode!r}")

print(f"OBSERVABILITY PASS: {mode} snapshot events={len(events)} dropped=0")

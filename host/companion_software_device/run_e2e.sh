#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${COMPANION_EVIDENCE_OUT:-$ROOT/software-device-evidence.json}"
TOOL_OUT="${OUT%.json}-tool.json"
TOOL_DB_OUT="${OUT%.json}-tool-db.json"
CORE_OBS_OUT="${OUT%.json}-observability-core.json"
TMP="$(mktemp -d)"
SERVER_LOG="$TMP/companiond.log"
TOOL_SERVER_LOG="$TMP/companiond-tool.log"
MODEL_LOG="$TMP/fake-responses.log"
SERVER_PID=""
MODEL_PID=""

stop_server() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
  fi
}
cleanup() {
  stop_server
  if [[ -n "$MODEL_PID" ]]; then kill "$MODEL_PID" 2>/dev/null || true; wait "$MODEL_PID" 2>/dev/null || true; fi
  if [[ "${KEEP_SOFTWARE_DEVICE_LOGS:-0}" == "1" ]]; then
    cp "$SERVER_LOG" "$ROOT/software-device-server.log" || true
    cp "$TOOL_SERVER_LOG" "$ROOT/software-device-tool-server.log" || true
    cp "$MODEL_LOG" "$ROOT/software-device-fake-responses.log" || true
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

start_server() {
  local log="$1"
  "${COMPANIOND_BIN:-/usr/local/bin/companiond}" >"$log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 100); do
    if curl -fsS http://127.0.0.1:18000/healthz >/dev/null; then return 0; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then cat "$log" >&2; exit 1; fi
    sleep 0.1
  done
  cat "$log" >&2
  return 1
}

enroll_device() {
  local device_id="$1"
  curl -fsS -X POST \
    -H "Authorization: Bearer $COMPANION_ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"user_id":"default","tenant_id":"tier1","plan":"test"}' \
    "http://127.0.0.1:18000/v1/admin/devices/${device_id}/credential" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("shown_once") is True; print(d["token"])'
}

revoke_device() {
  local device_id="$1"
  curl -fsS -X DELETE \
    -H "Authorization: Bearer $COMPANION_ADMIN_TOKEN" \
    "http://127.0.0.1:18000/v1/admin/devices/${device_id}/credential" >/dev/null
}

expect_unauthorized() {
  local device_id="$1"
  local credential="$2"
  local status
  status="$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "Device-Id: ${device_id}" \
    -H "Authorization: Bearer ${credential}" \
    http://127.0.0.1:18000/v2/device)"
  if [[ "$status" != "401" ]]; then
    echo "expected 401 for device=$device_id, got $status" >&2
    return 1
  fi
}

export COMPANION_PROFILE=test
export COMPANION_ALLOW_MOCK_PROVIDERS=true
export COMPANION_ADDRESS="127.0.0.1:18000"
export COMPANION_ADMIN_TOKEN="tier1-admin-token"
export COMPANION_TIMEZONE="Asia/Ho_Chi_Minh"
export COMPANION_EVIDENCE_COMMIT="${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)}"
export ADK_OPENAI_BASE_URL="http://127.0.0.1:19000/v1"
export ADK_OPENAI_API_KEY="tier1-fake-key"
export ADK_MODEL="tier1-fake-model"

python3 "$ROOT/host/companion_software_device/fake_responses.py" >"$MODEL_LOG" 2>&1 &
MODEL_PID=$!
for _ in $(seq 1 100); do
  if curl -fsS http://127.0.0.1:19000/healthz >/dev/null; then break; fi
  if ! kill -0 "$MODEL_PID" 2>/dev/null; then cat "$MODEL_LOG" >&2; exit 1; fi
  sleep 0.05
done
curl -fsS http://127.0.0.1:19000/healthz >/dev/null || { cat "$MODEL_LOG" >&2; exit 1; }

# Core protocol/orchestration run through the ADK product runtime. ASR/TTS stay
# deterministic Tier-1 fixtures; the agent boundary is the real ADK Responses
# adapter and device authentication is the real SQLite enrollment path.
CORE_DEVICE="software-device-core"
export MOCK_TRANSCRIPT="tier1 transcript"
export COMPANION_DATABASE="$TMP/companion.db"
export COMPANION_EVIDENCE_CONFIG_SHA256
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \
  'auth=database_enrolled' 'asr=mock:tier1 transcript' \
  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
export COMPANION_OBSERVABILITY_FILE="$CORE_OBS_OUT"
start_server "$SERVER_LOG"
CORE_CREDENTIAL="$(enroll_device "$CORE_DEVICE")"
expect_unauthorized "$CORE_DEVICE" "wrong-tier1-credential"
"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
  --url ws://127.0.0.1:18000/v2/device \
  --device-id "$CORE_DEVICE" \
  --token "$CORE_CREDENTIAL" \
  --admin-token "$COMPANION_ADMIN_TOKEN" \
  --scenario-set core \
  --evidence "$OUT"
revoke_device "$CORE_DEVICE"
expect_unauthorized "$CORE_DEVICE" "$CORE_CREDENTIAL"
python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"
stop_server
python3 "$ROOT/host/companion_software_device/validate_observability.py" "$CORE_OBS_OUT" core

# Representative ADK tool parity. The same durable SQLite database and enrolled
# device are reused across process restarts. Restarting only changes MockASR's
# deterministic transcript; the product agent remains ADK and the Responses
# fixture must route every case through the public ToolRegistry definition.
# The note-conflict case intentionally reuses call_note_1 after a restart with
# different canonical arguments, proving the durable SQLite conflict path end to
# end rather than only at repository-unit level.
TOOL_DEVICE="software-device-tool"
export COMPANION_DATABASE="$TMP/tool.db"
TOOL_CREDENTIAL=""
TOOL_CASES=(
  "expense|Tier1 expense 50k|$TOOL_OUT|expense.log|Tier-1 tool parity ok"
  "budget|Tier1 budget weekly|${OUT%.json}-tool-budget.json|budget.set|Tier-1 tool parity ok"
  "note|Tier1 note|${OUT%.json}-tool-note.json|note.create|Tier-1 tool parity ok"
  "note-conflict|Tier1 note conflict|${OUT%.json}-tool-note-conflict.json|note.create|Tier-1 idempotency conflict ok"
  "journal|Tier1 journal|${OUT%.json}-tool-journal.json|journal.create|Tier-1 tool parity ok"
  "reminder|Tier1 reminder|${OUT%.json}-tool-reminder.json|reminder.create|Tier-1 tool parity ok"
  "timer|Tier1 timer|${OUT%.json}-tool-timer.json|timer.create|Tier-1 tool parity ok"
  "memory|Tier1 memory|${OUT%.json}-tool-memory.json|memory.remember|Tier-1 tool parity ok"
)

for spec in "${TOOL_CASES[@]}"; do
  IFS='|' read -r case_id transcript evidence_path expected_tool expected_text <<<"$spec"
  export MOCK_TRANSCRIPT="$transcript"
  COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
    'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \
    'auth=database_enrolled' "asr=mock:${transcript}" \
    'tts=mock' 'protocol=v2' "tool_case=${case_id}" | sha256sum | awk '{print $1}')"
  export COMPANION_OBSERVABILITY_FILE="${evidence_path%.json}-observability.json"
  start_server "$TOOL_SERVER_LOG"
  if [[ -z "$TOOL_CREDENTIAL" ]]; then
    TOOL_CREDENTIAL="$(enroll_device "$TOOL_DEVICE")"
    expect_unauthorized "$TOOL_DEVICE" "wrong-tier1-credential"
  fi
  "${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
    --url ws://127.0.0.1:18000/v2/device \
    --device-id "$TOOL_DEVICE" \
    --token "$TOOL_CREDENTIAL" \
    --admin-token "$COMPANION_ADMIN_TOKEN" \
    --scenario-set tool \
    --expected-text "$expected_text" \
    --evidence "$evidence_path"
  python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$evidence_path"
  stop_server
  if [[ "$case_id" == "note-conflict" ]]; then
    # The ADK fixture only emits the expected text after observing the real
    # IDEMPOTENCY_CONFLICT function output. The observability check therefore
    # validates a safe tool.end snapshot without incorrectly requiring outcome=ok.
    python3 "$ROOT/host/companion_software_device/validate_observability.py" "${evidence_path%.json}-observability.json" tool
  else
    python3 "$ROOT/host/companion_software_device/validate_observability.py" "${evidence_path%.json}-observability.json" tool "$expected_tool"
  fi
done

# Re-open the same authoritative DB only to exercise the final credential
# lifecycle. A revoked credential must fail before WebSocket upgrade.
export MOCK_TRANSCRIPT="Tier1 memory"
export COMPANION_OBSERVABILITY_FILE="$TMP/final-auth-observability.json"
start_server "$TOOL_SERVER_LOG"
revoke_device "$TOOL_DEVICE"
expect_unauthorized "$TOOL_DEVICE" "$TOOL_CREDENTIAL"
stop_server
python3 "$ROOT/host/companion_software_device/verify_tool_db.py" "$TMP/tool.db" "$TOOL_DB_OUT"

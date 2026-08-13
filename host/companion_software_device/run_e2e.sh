#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${COMPANION_EVIDENCE_OUT:-$ROOT/software-device-evidence.json}"
TOOL_OUT="${OUT%.json}-tool.json"
TOOL_DB_OUT="${OUT%.json}-tool-db.json"
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
unset COMPANION_AGENT_RUNTIME COMPANION_DEVICE_TOKEN QWEN_BASE_URL QWEN_API_KEY QWEN_MODEL QWEN_FAST_MODEL QWEN_REASONING_MODEL

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

# Authoritative mutation run: ADK -> complete ToolRegistry -> SQLite. The fake
# Responses endpoint only decides the deterministic function call; ToolRegistry
# policy/schema/idempotency and the real repository execute the mutation.
TOOL_DEVICE="software-device-tool"
export MOCK_TRANSCRIPT="Hôm nay đi chợ 50k"
export COMPANION_DATABASE="$TMP/tool.db"
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \
  'auth=database_enrolled' 'asr=mock:Hôm nay đi chợ 50k' \
  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
start_server "$TOOL_SERVER_LOG"
TOOL_CREDENTIAL="$(enroll_device "$TOOL_DEVICE")"
expect_unauthorized "$TOOL_DEVICE" "wrong-tier1-credential"
"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
  --url ws://127.0.0.1:18000/v2/device \
  --device-id "$TOOL_DEVICE" \
  --token "$TOOL_CREDENTIAL" \
  --admin-token "$COMPANION_ADMIN_TOKEN" \
  --scenario-set tool \
  --evidence "$TOOL_OUT"
revoke_device "$TOOL_DEVICE"
expect_unauthorized "$TOOL_DEVICE" "$TOOL_CREDENTIAL"
python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$TOOL_OUT"
stop_server
python3 "$ROOT/host/companion_software_device/verify_tool_db.py" "$TMP/tool.db" "$TOOL_DB_OUT"

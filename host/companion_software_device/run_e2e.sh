#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${COMPANION_EVIDENCE_OUT:-$ROOT/software-device-evidence.json}"
TOOL_OUT="${OUT%.json}-tool.json"
TOOL_DB_OUT="${OUT%.json}-tool-db.json"
TMP="$(mktemp -d)"
SERVER_LOG="$TMP/companiond.log"
TOOL_SERVER_LOG="$TMP/companiond-tool.log"
MODEL_LOG="$TMP/fake-openai-responses.log"
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
    cp "$MODEL_LOG" "$ROOT/software-device-fake-openai-responses.log" || true
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

# The model fixture speaks the exact OpenAI Responses API SSE subset consumed by
# google/adk-go v2.2.0. It is deterministic test infrastructure, not provider evidence.
python3 "$ROOT/host/companion_software_device/fake_openai_responses.py" >"$MODEL_LOG" 2>&1 &
MODEL_PID=$!
for _ in $(seq 1 100); do
  if curl -fsS http://127.0.0.1:19000/healthz >/dev/null; then break; fi
  if ! kill -0 "$MODEL_PID" 2>/dev/null; then cat "$MODEL_LOG" >&2; exit 1; fi
  sleep 0.05
done
curl -fsS http://127.0.0.1:19000/healthz >/dev/null || { cat "$MODEL_LOG" >&2; exit 1; }

export COMPANION_PROFILE=test
export COMPANION_ALLOW_MOCK_PROVIDERS=true
export COMPANION_ADDRESS="127.0.0.1:18000"
export COMPANION_ADMIN_TOKEN="tier1-admin-token"
export COMPANION_TIMEZONE="Asia/Ho_Chi_Minh"
export ADK_MODEL="tier1-fake-model"
export ADK_OPENAI_BASE_URL="http://127.0.0.1:19000/v1"
export ADK_OPENAI_API_KEY="tier1-fake-api-key"
export COMPANION_EVIDENCE_COMMIT="${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)}"

# Core real-boundary run: production CompanionApp + protocol v2 + enrolled
# per-device auth + production ADK runtime, while ASR/TTS/model remain deterministic.
export MOCK_TRANSCRIPT="tier1 transcript"
export COMPANION_DATABASE="$TMP/companion.db"
export COMPANION_EVIDENCE_CONFIG_SHA256
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \
  'device_auth=enrolled_sqlite' 'asr=mock:tier1 transcript' \
  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
start_server "$SERVER_LOG"
"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
  --url ws://127.0.0.1:18000/v2/device \
  --admin-token "$COMPANION_ADMIN_TOKEN" \
  --scenario-set core \
  --evidence "$OUT"
python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"
stop_server

# Deterministic ADK tool run. The fake Responses model emits expense.create;
# production ADK feeds the call through ToolRegistry and the real SQLite store,
# then returns the authoritative tool result as function_call_output.
export MOCK_TRANSCRIPT="Hôm nay đi chợ 50k"
export COMPANION_DATABASE="$TMP/tool.db"
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \
  'device_auth=enrolled_sqlite' 'asr=mock:Hôm nay đi chợ 50k' \
  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
start_server "$TOOL_SERVER_LOG"
"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
  --url ws://127.0.0.1:18000/v2/device \
  --admin-token "$COMPANION_ADMIN_TOKEN" \
  --scenario-set tool \
  --evidence "$TOOL_OUT"
python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$TOOL_OUT"
stop_server
python3 "$ROOT/host/companion_software_device/verify_tool_db.py" "$TMP/tool.db" "$TOOL_DB_OUT"

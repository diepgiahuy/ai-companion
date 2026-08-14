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
stop_server(){ if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; SERVER_PID=""; fi; }
cleanup(){ stop_server; if [[ -n "$MODEL_PID" ]]; then kill "$MODEL_PID" 2>/dev/null || true; wait "$MODEL_PID" 2>/dev/null || true; fi; if [[ "${KEEP_SOFTWARE_DEVICE_LOGS:-0}" == "1" ]]; then cp "$SERVER_LOG" "$ROOT/software-device-server.log" || true; cp "$TOOL_SERVER_LOG" "$ROOT/software-device-tool-server.log" || true; cp "$MODEL_LOG" "$ROOT/software-device-fake-responses.log" || true; fi; rm -rf "$TMP"; }
trap cleanup EXIT
start_server(){ local log="$1"; "${COMPANIOND_BIN:-/usr/local/bin/companiond}" >"$log" 2>&1 & SERVER_PID=$!; for _ in $(seq 1 100); do if curl -fsS http://127.0.0.1:18000/healthz >/dev/null; then return 0; fi; if ! kill -0 "$SERVER_PID" 2>/dev/null; then cat "$log" >&2; exit 1; fi; sleep 0.1; done; cat "$log" >&2; return 1; }
enroll_device(){ local device_id="$1"; curl -fsS -X POST -H "Authorization: Bearer $COMPANION_ADMIN_TOKEN" -H 'Content-Type: application/json' --data '{"user_id":"default","tenant_id":"tier1","plan":"test"}' "http://127.0.0.1:18000/v1/admin/devices/${device_id}/credential" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("shown_once") is True; print(d["token"])'; }
revoke_device(){ local device_id="$1"; curl -fsS -X DELETE -H "Authorization: Bearer $COMPANION_ADMIN_TOKEN" "http://127.0.0.1:18000/v1/admin/devices/${device_id}/credential" >/dev/null; }
set_memory_consent(){ local user_id="$1"; curl -fsS -X PATCH -H "Authorization: Bearer $COMPANION_ADMIN_TOKEN" -H 'Content-Type: application/json' --data '{"save_voice_audio":false,"long_term_memory_enabled":true,"conversation_retention_days":0,"voice_memo_retention_days":0,"memory_retention_days":0}' "http://127.0.0.1:18000/v1/admin/users/${user_id}/privacy" >/dev/null; }
expect_unauthorized(){ local device_id="$1" credential="$2" status; status="$(curl -sS -o /dev/null -w '%{http_code}' -H "Device-Id: ${device_id}" -H "Authorization: Bearer ${credential}" http://127.0.0.1:18000/v2/device)"; [[ "$status" == "401" ]] || { echo "expected 401 for device=$device_id, got $status" >&2; return 1; }; }

export COMPANION_PROFILE=test COMPANION_ALLOW_MOCK_PROVIDERS=true COMPANION_ADDRESS="127.0.0.1:18000" COMPANION_ADMIN_TOKEN="tier1-admin-token" COMPANION_TIMEZONE="Asia/Ho_Chi_Minh"
test -n "${COMPANION_DATABASE_URL:-}" || { echo "COMPANION_DATABASE_URL is required" >&2; exit 1; }
export COMPANION_EVIDENCE_COMMIT="${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)}" ADK_OPENAI_BASE_URL="http://127.0.0.1:19000/v1" ADK_OPENAI_API_KEY="tier1-fake-key" ADK_MODEL="tier1-fake-model"
python3 "$ROOT/host/companion_software_device/fake_responses.py" >"$MODEL_LOG" 2>&1 & MODEL_PID=$!
for _ in $(seq 1 100); do if curl -fsS http://127.0.0.1:19000/healthz >/dev/null; then break; fi; if ! kill -0 "$MODEL_PID" 2>/dev/null; then cat "$MODEL_LOG" >&2; exit 1; fi; sleep 0.05; done
curl -fsS http://127.0.0.1:19000/healthz >/dev/null || { cat "$MODEL_LOG" >&2; exit 1; }

CORE_DEVICE="software-device-core"; export MOCK_TRANSCRIPT="tier1 transcript" COMPANION_EVIDENCE_CONFIG_SHA256
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' 'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' 'auth=database_enrolled' 'asr=mock:tier1 transcript' 'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
export COMPANION_OBSERVABILITY_FILE="$CORE_OBS_OUT"; start_server "$SERVER_LOG"; CORE_CREDENTIAL="$(enroll_device "$CORE_DEVICE")"; expect_unauthorized "$CORE_DEVICE" "wrong-tier1-credential"
"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" --url ws://127.0.0.1:18000/v2/device --device-id "$CORE_DEVICE" --token "$CORE_CREDENTIAL" --admin-token "$COMPANION_ADMIN_TOKEN" --scenario-set core --evidence "$OUT"
revoke_device "$CORE_DEVICE"; expect_unauthorized "$CORE_DEVICE" "$CORE_CREDENTIAL"; python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"; stop_server; python3 "$ROOT/host/companion_software_device/validate_observability.py" "$CORE_OBS_OUT" core

# Reuse one authoritative PostgreSQL DB and enrolled device across server restarts.
# The note-conflict case deliberately reuses the same ADK function-call key with
# different canonical arguments. The fake model only emits the expected success
# text after observing the real IDEMPOTENCY_CONFLICT tool result.
TOOL_DEVICE="software-device-tool"; TOOL_CREDENTIAL=""
TOOL_CASES=(
  "expense|Tier1 expense 50k|$TOOL_OUT|expense.log|Tier-1 tool parity ok"
  "budget|Tier1 budget weekly|${OUT%.json}-tool-budget.json|budget.set|Tier-1 tool parity ok"
  "note|Tier1 note|${OUT%.json}-tool-note.json|note.create|Tier-1 tool parity ok"
  "note-conflict|Tier1 note conflict|${OUT%.json}-tool-note-conflict.json|note.create|Tier-1 idempotency conflict ok"
  "journal|Tier1 journal|${OUT%.json}-tool-journal.json|journal.create|Tier-1 tool parity ok"
  "reminder|Tier1 reminder|${OUT%.json}-tool-reminder.json|reminder.create|Tier-1 tool parity ok"
  "timer|Tier1 timer|${OUT%.json}-tool-timer.json|timer.create|Tier-1 tool parity ok"
  "memory|Tier1 memory|${OUT%.json}-tool-memory.json|memory.remember|Tier-1 tool parity ok"
  "device-volume|Tier1 volume 42|${OUT%.json}-tool-device-volume.json|device.volume.set|Tier-1 tool parity ok"
)
for spec in "${TOOL_CASES[@]}"; do
  IFS='|' read -r case_id transcript evidence_path expected_tool expected_text <<<"$spec"; export MOCK_TRANSCRIPT="$transcript"; COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' 'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' 'auth=database_enrolled' "asr=mock:${transcript}" 'tts=mock' 'protocol=v2' "tool_case=${case_id}" | sha256sum | awk '{print $1}')"; export COMPANION_OBSERVABILITY_FILE="${evidence_path%.json}-observability.json"; start_server "$TOOL_SERVER_LOG"
  if [[ -z "$TOOL_CREDENTIAL" ]]; then TOOL_CREDENTIAL="$(enroll_device "$TOOL_DEVICE")"; expect_unauthorized "$TOOL_DEVICE" "wrong-tier1-credential"; fi
  if [[ "$case_id" == "memory" ]]; then set_memory_consent "default"; fi
  if ! "${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" --url ws://127.0.0.1:18000/v2/device --device-id "$TOOL_DEVICE" --token "$TOOL_CREDENTIAL" --admin-token "$COMPANION_ADMIN_TOKEN" --scenario-set tool --expected-text "$expected_text" --evidence "$evidence_path"; then
    echo "Tier-1 tool case failed: ${case_id}" >&2
    cat "$TOOL_SERVER_LOG" >&2
    cat "$MODEL_LOG" >&2
    exit 1
  fi
  python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$evidence_path"; stop_server
  if [[ "$case_id" == "note-conflict" ]]; then
    python3 "$ROOT/host/companion_software_device/validate_observability.py" "${evidence_path%.json}-observability.json" tool
  else
    python3 "$ROOT/host/companion_software_device/validate_observability.py" "${evidence_path%.json}-observability.json" tool "$expected_tool"
  fi
done
export MOCK_TRANSCRIPT="Tier1 memory" COMPANION_OBSERVABILITY_FILE="$TMP/final-auth-observability.json"; start_server "$TOOL_SERVER_LOG"; revoke_device "$TOOL_DEVICE"; expect_unauthorized "$TOOL_DEVICE" "$TOOL_CREDENTIAL"; stop_server; "${TIER1_STORE_VERIFIER_BIN:-/usr/local/bin/companion-verify-tier1-store}" --postgres "$COMPANION_DATABASE_URL" --output "$TOOL_DB_OUT"

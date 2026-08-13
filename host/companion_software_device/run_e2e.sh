#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${COMPANION_EVIDENCE_OUT:-$ROOT/software-device-evidence.json}"
TMP="$(mktemp -d)"
SERVER_LOG="$TMP/companiond.log"
SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; fi
  if [[ "${KEEP_SOFTWARE_DEVICE_LOGS:-0}" == "1" ]]; then cp "$SERVER_LOG" "$ROOT/software-device-server.log" || true; fi
  rm -rf "$TMP"
}
trap cleanup EXIT

export COMPANION_PROFILE=test
export COMPANION_ALLOW_MOCK_PROVIDERS=true
export COMPANION_AGENT_RUNTIME=mock
export MOCK_TRANSCRIPT="tier1 transcript"
export COMPANION_ADDRESS="127.0.0.1:18000"
export COMPANION_DATABASE="$TMP/companion.db"
export COMPANION_DEVICE_TOKEN="tier1-device-token"
export COMPANION_ADMIN_TOKEN="tier1-admin-token"
export COMPANION_TIMEZONE="Asia/Ho_Chi_Minh"
export COMPANION_EVIDENCE_CONFIG_SHA256
COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\n' \
  'profile=test' 'allow_mock=true' 'agent=mock' 'asr=mock:tier1 transcript' \
  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"
export COMPANION_EVIDENCE_COMMIT="${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)}"

"${COMPANIOND_BIN:-/usr/local/bin/companiond}" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 100); do
  if curl -fsS http://127.0.0.1:18000/healthz >/dev/null; then break; fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then cat "$SERVER_LOG" >&2; exit 1; fi
  sleep 0.1
done
curl -fsS http://127.0.0.1:18000/healthz >/dev/null || { cat "$SERVER_LOG" >&2; exit 1; }

"${SOFTWARE_DEVICE_BIN:-/usr/local/bin/companion_software_device}" \
  --url ws://127.0.0.1:18000/v2/device \
  --token "$COMPANION_DEVICE_TOKEN" \
  --admin-token "$COMPANION_ADMIN_TOKEN" \
  --evidence "$OUT"
python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"

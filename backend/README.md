# Companion Go backend

Toolchain: **Go 1.26.5** (`go.mod` + container gate).

This server implements the Xiaozhi-compatible subset used by the ESP32 POC:

- JSON control messages: `hello`, `listen`, `stt`, `tts`, `abort`, `error`.
- Binary raw Opus (no Ogg container), one 60 ms packet per WebSocket message.
- Device uplink: 16 kHz mono; server downlink: 24 kHz mono.
- The server decodes Opus to PCM before ASR and encodes PCM after TTS.

## Reproducible test environment

From the repository root:

```bash
docker compose run --build --rm test      # C++ host + budget + Go race + real libopus E2E
docker compose build esp-idf   # ESP32-S3 target compile with managed components
docker compose up backend      # local mock ASR/TTS backend on :8000
```

The Go Opus binding uses CGO and system `libopus`; the supplied Debian container
installs it. Use the `nolibopusfile` build tag because this protocol carries raw
Opus packets and does not read Ogg/Opus files.
- One WebSocket reader and one writer per device.
- One cancellable active turn per device/session, with explicit user + thread identity.
- Bounded outbound queue and an eight-second input limit.
- SQLite WAL storage with user-scoped domain ownership, session-safe idempotency keys and legacy migration.
- Optional OpenAI-compatible `Qwen3-4B-Instruct-2507` provider.
- Provider-neutral `ToolRegistry` and MCP-style `ResourceRegistry`; deterministic ContextRouter preloads application-controlled resources and exposes only relevant tool packs.
- Typed repository ports isolate Expense/Budget/Schedule/Note/Journal/VoiceMemo logic from SQLite; conversation uses a write-through cache + replaceable store, and `agent.Qwen` does not import SQLite.
- Deterministic mock ASR and streaming tone TTS for hardware proof.


## Backend feature checklist

The root [`README.md`](../README.md) is the source of truth for the complete firmware + backend roadmap. Backend work should preserve these priorities:

- 🟡 relative `timer.create(delay_seconds)`, ACK/retry reminder scheduler + user/device-scoped alarm/schedule push are implemented; Go 1.26.5 E2E rerun pending in this sandbox
- 🟡 strongly typed expense/budget/note/journal/schedule CRUD (including timer pause/resume) + bounded authoritative queries are implemented; rerun pending here
- 🟡 voice-note WAV persistence + SQLite metadata + bounded list tool are implemented; rerun pending here
- 🔴 proactive event delivery with quiet-hours/rate-limit policy
- 🔴 production ASR/TTS adapters
- 🔴 long-term memory/RAG, speaker-ID, meeting summaries and external integrations
- 🔴 device enrollment, TLS/WSS, OTA metadata and offline action replay

Do not mark a backend feature complete until its side effects are idempotent and covered by tests.

## Run

```bash
cp config.example.env .env
set -a; . ./.env; set +a
go run -tags nolibopusfile ./cmd/companiond
```

With `QWEN_BASE_URL` empty, the server uses the mock agent. This is the correct
mode for first ESP32 integration because it isolates network/audio failures
from model failures.

## Verify

```bash
go test -tags nolibopusfile -race ./...
```

Real streaming ASR and Vietnamese TTS remain provider-level follow-up work;
they are deliberately not hidden behind fake production claims.

The Qwen tests use a fake OpenAI-compatible HTTP endpoint. Coverage includes
single expense compatibility, `expense.log` batch writes, persisted summaries,
voice-memo WAV save/metadata, and idempotent retries. Capability tests additionally
cover registry discovery/execution and native resource URIs; future MCP adapters must
register into these same ports instead of adding a second agent execution path. Server/store tests also
cover deterministic relative timer creation, reminder claim/recover/fire and device-targeted `alarm` delivery.

This delivery sandbox has Go 1.23.2 while `go.mod` requires Go 1.26.5 and network
toolchain download is blocked, so the modified Go suite must be rerun with the
supplied Docker image or a local Go 1.26.5 + libopus environment before those
features are promoted from 🟡 to ✅ in the root README.

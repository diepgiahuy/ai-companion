# Companion Go backend

Toolchain: **Go 1.26.5** (`go.mod` + container gate).

This server implements the canonical protocol v2 used by the ESP32 POC:

- JSON control uses the typed v2 envelope on `/v2/device`, including
  `session.hello`, `turn.listen`, `transcript.final`, `tts.lifecycle`,
  `turn.abort`, and `protocol.error`.
- Version 1 fails with `unsupported_protocol_version`; a flat v2 control fails
  with `invalid_envelope`. There is no dual-read or dual-write path.
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
- Explicit agent-runtime selector: legacy Qwen compatibility path, opt-in ADK v2 bridge, or deterministic mock.
- The ADK bridge is an anti-corruption layer: ADK types stay out of domain/data packages and host tools delegate back through the authoritative `ToolRegistry`.
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

# Default / rollback runtime. If QWEN_BASE_URL is empty this uses the deterministic mock.
go run -tags nolibopusfile ./cmd/companiond

# CP-SW2 experimental ADK runtime. Requires the production dependency graph.
COMPANION_AGENT_RUNTIME=adk go run -tags "adk,nolibopusfile" ./cmd/companiond
```

Runtime selection is explicit: `COMPANION_AGENT_RUNTIME=legacy|adk|mock`. An
unknown value fails startup rather than silently choosing another provider. The
legacy path remains the default until ADK session/parity gates pass.

The ADK OpenAI adapter targets the **OpenAI Responses API**. `ADK_OPENAI_BASE_URL`
may point at OpenAI or a compatible local server only when that server implements
the Responses API; an endpoint that implements only `/chat/completions` must stay
on the legacy adapter or gain a different `model.LLM` adapter.

The ADK path currently uses ADK's in-memory session runner only as an opt-in
integration seam. It must not become the default before CP-SW4 replaces that
service with durable Companion session storage and the full tool/context parity
suite passes. Host-side authorization, destructive-intent policy, idempotency,
and token quota/usage accounting remain Companion-owned boundaries.

## Verify

```bash
# Restricted/offline compatibility gate
GOTOOLCHAIN=local go test -modfile=go.offline.mod -race -count=1 ./...

# Production ADK gate — exact toolchain required
make backend-adk-gate
```

Real streaming ASR and Vietnamese TTS remain provider-level follow-up work;
they are deliberately not hidden behind fake production claims.

The Qwen tests use a fake OpenAI-compatible HTTP endpoint. Coverage includes
single expense compatibility, `expense.log` batch writes, persisted summaries,
voice-memo WAV save/metadata, and idempotent retries. Capability tests additionally
cover registry discovery/execution and native resource URIs; future MCP adapters must
register into these same ports instead of adding a second agent execution path. Server/store tests also
cover deterministic relative timer creation, reminder claim/recover/fire and device-targeted `alarm.fired` delivery.

This delivery sandbox has Go 1.23.2 while `go.mod` requires Go 1.26.5 and network
toolchain download is blocked, so the modified Go suite must be rerun with the
supplied Docker image or a local Go 1.26.5 + libopus environment before those
features are promoted from 🟡 to ✅ in the root README.

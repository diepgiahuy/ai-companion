# Companion Production v1 — Single-User AI Voice Device

> **Status:** Production rewrite in controlled rollout  
> **Checkpoint:** CP-SW2.2 — hardening checkpoint PASS; CP-SW2 production ADK gate still partial
> **Updated:** 2026-08-12  
> **Source baseline:** `companion-production-v1-cp0-20260812`  
> **Rule:** a feature is never marked ✅ until its stated test gate passes. Research choice ≠ implemented. Source-complete ≠ hardware-proven.

This repository is no longer treated as a throwaway POC. The target is a **single-user production companion device** built around ESP32-S3 with a Go backend, local-first voice/LLM capability, optional cloud realtime voice, durable personal features, secure updates, dynamic UI/assets, and replaceable providers.

The previous README is preserved at `docs/LEGACY_POC_README_20260811.md` for audit/history.

---

## 0. Checkpoint dashboard

**Rollout policy changed on 2026-08-12:** software is implemented first; firmware/hardware work is intentionally deferred until the server/runtime stack is production-shaped and regression-covered.

Legend: ✅ passed gate · 🟡 in progress / partial · 🔴 not started · 🧪 candidate under benchmark · ⚠️ requires external runtime/provider/hardware.

| Checkpoint | Scope | Status | Exit gate |
|---|---|---:|---|
| CP-SW0 | Freeze legacy baseline + production charter | ✅ | Baseline archived; existing host/backend regression passes |
| CP-SW1 | Realtime turn runtime + streaming response foundation | ✅ | Generation-safe barge-in queueing + deterministic sentence segmentation + overlapping model/TTS test + race suite pass |
| CP-SW2 | Go 1.26.5 + Google ADK Go v2.2 integration | 🟡 ⚠️ | ADK model/tool/session seam compiles on exact Go 1.26.5; legacy Qwen path remains rollback fallback until parity suite passes |
| CP-SW3 | PostgreSQL + Ent + Atlas + River | 🔴 | Domain parity, migrations, transactional reminder/job recovery, backup/restore tests pass |
| CP-SW4 | ADK agent/tools/context migration | 🔴 | Custom keyword router/tool loop removed from primary path; typed tools, sessions, skills/context tests pass |
| CP-SW5 | Local AI runtime | 🔴 🧪 | Vietnamese streaming ASR + local LLM + streaming TTS meet quality/latency gates |
| CP-SW6 | Memory/context benchmark | 🔴 🧪 | Vietnamese personal-memory benchmark chooses adapter; temporal conflict/provenance tests pass |
| CP-SW7 | Dynamic package/control platform | 🔴 | Signed package manifest/assets/UI capability model + rollback + remote config/feature flag tests pass |
| CP-SW8 | MCP + observability + security hardening | 🔴 | MCP isolation, OpenTelemetry traces/metrics, auth/secrets/fault-injection gates pass |
| CP-SW9 | Optional cloud native realtime voice | 🔴 | Provider-independent realtime voice adapter + interruption/tool/session parity passes |
| CP-FW1 | ESP-IDF production firmware migration | 🔴 ⚠️ | Exact target build/flash/HIL; Arduino removed from production path |
| CP-FW2 | ESP-SR/WebRTC/LVGL/package runtime | 🔴 ⚠️ | AEC/VAD/WakeNet/WebRTC/UI/package resource budgets and soak tests pass |
| CP-HW1 | Production hardware stabilization/PCB | 🔴 ⚠️ | Power/acoustics/RF/thermal/manufacturing tests pass |
| CP-RC1 | Production RC | 🔴 ⚠️ | 24h+ soak, fault injection, HIL, backup/restore, release checklist all green |

### CP-SW1 evidence — 2026-08-12

**Implemented in code:**

- Added provider-neutral `pipeline.StreamingAgent` + `AgentStreamEvent` so ADK/local/cloud runtimes can stream response deltas without coupling the voice server to one model API.
- Added deterministic `internal/realtime.Segmenter` for Vietnamese/English streamed text. First clause can break on comma after a minimum length; later clauses prefer stronger punctuation; a maximum-rune safety boundary prevents unbounded buffering.
- Added overlapping response path: model generation continues while completed clauses are synthesized sequentially by TTS. The server no longer has to wait for the whole answer before starting speech when the agent supports streaming.
- Added generation-scoped outbound messages. Queued audio/control belonging to an interrupted turn is rejected by the single writer after generation invalidation.
- Added explicit `{type:"turn", state:"interrupted", generation_id:...}` terminal control event so the device can drop any playback already buffered locally.
- Added `generation_id` to turn-scoped protocol control messages. Legacy raw binary Opus framing is deliberately preserved for compatibility in this checkpoint; wire-level audio sequence/header negotiation is deferred to the transport checkpoint.
- Production Go pin changed from 1.25.0 to **1.26.5** in `backend/go.mod`, test container, devcontainer and CI release gate. The offline compatibility test module intentionally remains Go 1.23 so this restricted environment can keep running functional regression tests.

**Tests passed in this environment:**

- `go test -race -count=1 -modfile=go.offline.mod ./...` ✅
- New sentence-segmentation tests ✅
- New deterministic test proving TTS starts before a blocked streaming model finishes ✅
- New generation invalidation test proving stale queued turn output becomes unwritable after interruption ✅
- `scripts/e2e_offline.sh` ✅: host C++ `2/2 PASS`, Opus probe, partition/SRAM design gate, full backend functional E2E + race.

**Not claimed yet:**

- Google ADK is not integrated into the source yet. ADK Go v2.2.0 pins Go 1.26.5; this sandbox only provides Go 1.23.2 and cannot fetch the production dependency graph. CP-SW2 must run on the exact production toolchain/container/CI before it can be marked ✅.
- Current custom Qwen/context/tool implementation remains the active compatibility path until CP-SW2/CP-SW4 parity tests prove the ADK replacement.
- WebSocket audio is still the legacy 60 ms framing contract; 20/40 ms negotiation and WebRTC are deferred to firmware/transport work rather than changed blindly while hardware is intentionally out of scope.

### CP-SW2.1 evidence — 2026-08-12

**Status: PARTIAL — source integration + offline regression are green; exact ADK dependency/toolchain gate is still blocked by this sandbox.**

**Implemented in code:**

- Added `internal/adkbridge` as an anti-corruption boundary. Product/domain/data packages do not import ADK types. Without the `adk` build tag the bridge fails closed with `ErrNotBuilt`.
- Added explicit `COMPANION_AGENT_RUNTIME=legacy|adk|mock`; `legacy` remains the default rollback path and unknown runtimes fail startup.
- Added the official ADK OpenAI/Responses model adapter behind the `adk` build tag and pinned the production module candidates to ADK v2.2.0 / Go 1.26.5.
- Wrapped the first four representative capabilities — `expense.log`, `budget.get`, `timer.create`, `memory.recall` — as typed ADK FunctionTools while reusing the existing `ToolRegistry` JSON schemas. The bridge does **not** duplicate validation, authorization or business semantics.
- ADK `FunctionCallID` participates in a canonical SHA-256 idempotency key together with user/thread/device/session/turn/tool identity; delimiter-collision cases are regression-tested.
- Preserved host destructive-intent policy and host usage quota/metering around the ADK model boundary so opting into ADK does not bypass existing safety/cost controls.
- Added SSE response streaming into the existing `StreamingAgent` surface. ADK partial events are treated as real deltas; a final full snapshot is deduplicated before sentence segmentation/TTS. UI presentations from host tools are queued and emitted sequentially.
- Production E2E now includes the `adk` build tag, and `make backend-adk-gate` refuses any Go version other than 1.26.5.
- Added a clean-tag checkpoint snapshot script that creates/verifies ZIP, Git bundle, SHA256 and restore manifest together so future rollback artifacts cannot drift independently.

**Tests passed in the available environment:**

- focused `internal/adkbridge` + capability/policy unit tests with `-race` ✅
- fail-closed no-ADK-build test ✅
- registry validation and authorization preservation tests ✅
- reconnect-safe/collision-safe idempotency tests ✅
- ADK stream-delta edge-case tests, including repeated suffix chunks ✅
- full `scripts/e2e_offline.sh` host + backend functional/race regression ✅
- offline `go vet` for the modified untagged path ✅

**Gate still open / not claimed:**

- The sandbox has Go 1.23.2 and blocks outbound DNS, so it cannot fetch Go 1.26.5 or the new ADK dependency graph. The tagged ADK compile/API tests and final `go.sum` lock are therefore **not** claimed as passing here.
- ADK's in-memory runner is intentionally temporary. Durable Companion session/event storage and full existing-tool parity remain CP-SW4 gates before ADK can become the default.
- The official ADK OpenAI adapter requires the OpenAI **Responses API** surface. Servers exposing only Chat Completions remain on the legacy adapter until a compatible adapter/endpoint is proven.

Rollback target after this checkpoint is tagged: `CP-SW2.1-20260812`; the previous known-good software point remains `CP-SW1-20260812`.

### CP-SW2.2 evidence — 2026-08-12

**Status: PARTIAL — tool-loop/error hardening + independent static review are green; exact ADK production compile/provider gate remains open.**

- Replaced the single `sentText` terminal assumption with a Companion-owned invocation outcome tracker that correlates function calls/results and distinguishes text emitted before vs after tool execution.
- A committed `write`/`destructive` mutation with no post-tool model text gets a narrow deterministic `OK.` fallback so the user is not encouraged to repeat a side effect that already succeeded. Reads, failures, malformed results, incomplete calls, and mixed unacknowledged tool sequences are never hidden by that fallback.
- Malformed host-tool output now becomes a safe non-retryable `invalid_tool_result` response. Raw host output is not copied into the model context, and a parsed result is only accepted when it contains a boolean `ok` envelope.
- `ToolRegistry.Execute` now contains panics at the common tool boundary and returns a generic structured failure rather than allowing a tool panic/panic payload to escape into the voice process/model.
- Independent static review is now a permanent checkpoint gate. `docs/STATIC_REVIEW_GATE.md` defines the review dimensions, and checkpoint snapshotting refuses to run unless the checkpoint note records `Static review status: PASS`.
- The proposed per-request fire-and-forget usage-meter goroutine was **not** adopted: there is no measured bottleneck yet, and unbounded asynchronous accounting would weaken quota/shutdown reliability. Measure first; if required later, use a bounded/drained design.
- Independent review also exposed a realtime ordering bug outside the original Gemini findings: priority control writes could overtake accepted TTS audio. TTS lifecycle events and audio now share one ordered, turn-scoped **media FIFO**, while abort/config/other urgent control keeps its priority lane. This preserves barge-in responsiveness without a per-sentence drain barrier.
- Media lifecycle enqueue uses bounded backpressure; ordinary audio enqueue remains bounded/non-blocking. Non-streaming media/UI enqueue errors are no longer silently ignored, and streaming cancellation at terminal enqueue propagates instead of being logged as successful completion.
- ADK tool presentation is emitted only after a valid `ok=true` host result, preventing malformed/failed tools from showing a success UI.
- Final static-review stress evidence: expense websocket E2E `50/50` under `-race`, media/stream ordering `30/30`, ADK host/outcome/capability `20/20`, followed by full race/vet/offline E2E.
- Post-review `go test -race`, `go vet`, host tests and offline E2E are green. The exact tagged ADK gate is still blocked by this sandbox's Go 1.23.2/no-dependency-download constraint and is not falsely marked PASS.

Rollback target after this checkpoint is tagged: `CP-SW2.2-20260812`; `CP-SW2.1-20260812` remains the previous known-good point.

### CP-SW1 notes / difficulty / solution / trade-off

| Item | Difficulty/risk | Solution now | Trade-off / follow-up |
|---|---|---|---|
| Barge-in | Cancelling provider context did not guarantee already queued audio disappeared | generation-tag queued turn output; writer drops stale generations; explicit interrupted event | one packet already written to the socket cannot be recalled; device must clear its local playback buffer on interrupt |
| LLM → TTS latency | old `Agent.Respond()` waits for complete text | optional `StreamingAgent`; sentence segmenter feeds TTS while model still generates | legacy agents still work; ADK adapter must implement streaming next |
| Sentence boundaries | token-by-token TTS sounds broken; waiting for full sentences adds latency | deterministic punctuation/min/max rune segmenter | language-specific prosody remains TTS/provider concern; benchmark thresholds later |
| Production Go | environment only has Go 1.23.2 | separate release pin Go 1.26.5 and offline compatibility mod | exact production dependency gate remains pending until CI/container is available |
| Protocol evolution | adding headers to raw Opus would break current firmware immediately | add generation to control plane first; preserve binary frame compatibility | define WSS v2/WebRTC framing later with explicit negotiation |

# 1. Product goal

A desk-size personal companion for **one owner**, with these user-facing capabilities:

### Voice and interaction

- Wake word + push-to-talk fallback.
- Hands-free conversational mode.
- Fast barge-in: user can interrupt while the assistant is speaking.
- Vietnamese + English, with code-switching where providers support it.
- Local/private mode that remains useful without cloud AI.
- Optional cloud live voice mode for more natural low-latency speech-to-speech.
- Live UI states: `IDLE`, `LISTENING`, `TRANSCRIBING`, `THINKING`, `TOOL_RUNNING`, `SPEAKING`, `INTERRUPTED`, `OFFLINE`, `ERROR`.
- Partial transcript and response progress when transport/provider exposes them.

### Personal productivity

- Expenses: create/update/delete/list/query/summary.
- Budgets: daily/weekly/monthly/goal state.
- Notes.
- Journal/diary.
- Reminders.
- Timers/countdowns.
- Voice memos.
- Search/retrieval across personal content.
- Persistent conversation/session continuity.
- Long-term preferences and personal facts, with temporal updates rather than stale overwrite.

### Live/external data

- Market quotes/alerts with source and `as_of` metadata.
- Weather/news/calendar/home/device integrations through controlled external tools/MCP when enabled.
- Current external state is never stored as timeless LLM memory.

### Device/product platform

- Server-driven dynamic UI/theme/assets without full firmware OTA.
- Optional signed local extension packages for small trusted features.
- Remote config / kill switches / feature flags.
- A/B firmware OTA with rollback.
- Signed artifact/package delivery.
- Observability per voice turn.
- Local data ownership and privacy controls.

---

# 2. Non-goals for Production v1

Production v1 is **single-user**, therefore deliberately avoids premature infrastructure:

- No Kubernetes requirement.
- No microservice split by default; keep a modular Go monolith until measurements justify a split.
- No multi-tenant SaaS control plane.
- No Kafka/Redis just because they are common; add only if a measured requirement appears.
- No full Event Sourcing; transactional state remains source of truth.
- No arbitrary code hot-loading into the Go backend.
- No arbitrary Internet access granted directly to the LLM.
- No direct MCP implementation inside ESP32 Wasm modules.
- No ESP32-P4 migration unless S3 fails measured CPU/RAM/display/audio requirements.
- No camera/vision in the core hardware release unless explicitly added as a separate hardware revision.

---

# 3. Hardware production baseline and required changes

## 3.1 Current bench target

| Part | Current target | Production action |
|---|---|---|
| MCU | ESP32-S3, baseline assumes N16R8-class board | Keep S3 unless measured limits fail; verify exact module marking in CP1 |
| Mic | INMP441 I2S | Keep for current revision; prove signal integrity/noise/AEC placement |
| Amp | MAX98357A I2S | Keep if thermal/noise/output tests pass |
| Speaker | **8 Ω / 3 W** enclosed/resonance speaker | Correct old 4 Ω documentation; characterize loudness/distortion/echo |
| Display | Current exact SKU not frozen | Identify exact ST7789/ILI9341/etc. before production driver/UI freeze |
| Input | Physical button available as fallback | Keep as recovery/PTT/mode input even after wake word |
| Power | USB-C bench power | Product power tree/ESD/decoupling/optional battery is a separate CP1 hardware gate |

## 3.2 Hardware requirements before PCB freeze

- Verify 3V3 and 5V/VBUS rails under simultaneous Wi-Fi + display + speaker load.
- Add proper local decoupling around amp/mic/display as required by exact modules/PCB design.
- Use keyed/locking connectors or soldered production interconnects; no loose Dupont contacts in final hardware.
- Keep digital amp speaker output differential: `SPK-` is not ground.
- Physically isolate mic from speaker chamber; characterize enclosure echo path before AEC tuning.
- Define mic port/acoustic membrane and speaker grille/chamber in CAD together with DSP tests.
- Verify USB ESD/protection and power brownout behavior.
- Measure RF performance after enclosure/PCB placement.
- Add manufacturing test points for `3V3`, `5V`, `GND`, I2S clocks/data, boot/reset and serial/JTAG as policy permits.
- Final custom PCB only after bench rev proves simultaneous audio/display/network operation.

## 3.3 Hardware acceptance tests

- Mic raw samples change clearly with speech/clap; no constant `-1/0` stream.
- Speaker tone runs continuously without reset/brownout/thermal issue.
- Simultaneous mic RX + speaker TX works.
- Wi-Fi stress while audio is active does not corrupt I2S stream.
- AEC test set: silent room, near-field speech, music/assistant playback, user interruption, multiple volume levels.
- 30-minute audio stress + later 24-hour integrated soak.

---

# 4. Firmware production stack

## 4.1 Version policy

**Production baseline candidate:** ESP-IDF **5.5.4**, not current IDF 6.x yet.

Reason: ESP-IDF 6.0.x is newer, but ESP-SR 2.4.x explicitly labels IDF 6 support experimental. Production v1 prefers the newest stable combination whose critical audio stack is not experimental. Re-evaluate IDF 6 after ESP-SR/WebRTC compatibility gates turn stable.

### Planned firmware components

```text
ESP-IDF 5.5.4
├── board HAL
├── I2S RX/TX
├── ESP-SR 2.4.7
│   ├── AFE
│   ├── Full-Duplex AEC
│   ├── VADNet
│   └── WakeNet
├── esp-webrtc-solution 1.2.x
│   ├── WebRTC
│   ├── Opus
│   └── data channel
├── LVGL 9.6 XML runtime
├── esp_mmap_assets 2.0.x
├── OTA / rollback
└── optional WAMR extension runtime (feature-gated)
```

## 4.2 Audio frame policy

- Production live capture target: benchmark **20 ms vs 40 ms** frames.
- Do not preserve 60 ms only because legacy Xiaozhi used it.
- Keep codec/transport frame size configurable.
- Fixed-size/bounded buffers first; add pooling only after allocation/GC/profile evidence.

## 4.3 Realtime session state machine

Every turn carries at minimum:

```text
session_id
turn_id
generation_id
audio_seq
```

When user interruption occurs:

1. Cancel current generation context.
2. Increment `generation_id`.
3. Drain queued assistant audio/control items belonging to the old generation.
4. Reject late LLM/TTS/audio events whose generation is stale.
5. Begin new capture/turn without waiting for old provider completion.

This remains required even with native speech-to-speech providers.

---

# 5. Transport architecture

```text
Transport
├── WebRTCTransport        # production primary candidate
└── WSSOpusTransport       # fallback/debug/local compatibility
```

### WebRTC candidate

Use Espressif's `esp-webrtc-solution`; do not implement ICE/DTLS/SRTP/RTP/jitter handling from scratch.

Required benchmark before promotion:

- connect/reconnect time;
- one-way audio latency;
- packet loss / poor Wi-Fi behavior;
- CPU + heap + PSRAM footprint on exact S3;
- data-channel latency for UI/control;
- cancellation/barge-in behavior;
- TURN fallback if remote deployment needs it.

WSS remains a supported fallback because it is simpler to debug and useful for LAN/local deployments.

---

# 6. Backend production stack

## 6.1 Go

Move from Go 1.25 pin to **Go 1.26.x**. Current production baseline target is the latest patched 1.26 release available in CI, not the initial 1.26.0 release.

Reasons:

- current supported stable Go line;
- ADK Go v2 requires Go 1.25+;
- runtime/cgo improvements are useful for native ASR/audio bindings;
- security patch cadence is newer than the existing Go 1.25.0 toolchain pin.

## 6.2 Agent runtime — Google ADK Go

Adopt **Google ADK Go v2.x** as the agent/workflow layer.

It replaces custom framework-like responsibilities where possible:

- custom `ContextRouter` keyword routing;
- custom tool-loop orchestration;
- ad-hoc retry/timeout graph logic;
- custom pause/resume workflow machinery;
- large parts of context/session plumbing.

Do **not** put realtime media transport inside ADK. ADK is the brain/workflow plane; `RealtimeVoiceSession` remains a separate low-latency media plane.

### Model abstraction

```text
ADK model.LLM / ModelGateway
├── Local Qwen family
├── Gemini
├── OpenAI-compatible adapter
└── future model providers
```

No product domain logic may depend on one model name.

Local model baseline is a **benchmark**, not a permanent hard-code. `Qwen3.5-4B` is the first current local candidate because the model card explicitly targets agentic/multimodal use and supports mainstream inference runtimes.

---

# 7. Domain/data layer

## 7.1 Database

Production target: **PostgreSQL 18.x patched minor**.

Local development may still use temporary SQLite fixtures where useful, but production source of truth moves to Postgres.

## 7.2 ORM/schema

Use **Ent** for typed entities/queries/mutations and privacy hooks.

Use **Atlas versioned migrations** for reviewed production schema changes. No automatic production `Schema.Create()` mutation at process startup.

CI must test migrations against at least:

- PostgreSQL 17 current patched minor;
- PostgreSQL 18 current patched minor.

## 7.3 Background jobs

Use **River** for Postgres-backed transactional jobs:

- reminder firing;
- timer jobs;
- retry delivery;
- market alert evaluation scheduling;
- retention cleanup;
- memory consolidation/index refresh;
- package/OTA background work where appropriate.

Enqueue the job in the same transaction as authoritative state when they must be atomic.

## 7.4 What remains custom

Frameworks do not define product semantics. Keep small typed commands such as:

```text
RecordExpense
SetBudget
CreateReminder
SaveNote
SaveJournalEntry
CreateVoiceMemoMetadata
UpdateDeviceConfig
```

These functions own business invariants/idempotency, then call Ent/River. Avoid repository wrappers that only mirror generated CRUD with no product rule.

---

# 8. Tools, MCP and Internet access

## 8.1 Internal tools

Internal product actions are ADK typed function tools calling product commands directly.

```text
ADK FunctionTool
  -> RecordExpense
  -> Ent/Postgres
```

No MCP round-trip is required for internal code.

## 8.2 External integrations

Use the **official MCP Go SDK** for controlled external integrations.

Examples:

- weather;
- calendar;
- Home Assistant;
- market/news connectors;
- future external services.

The LLM never receives a generic unrestricted `http.get(any_url)` tool in production.

---

# 9. Local AI runtime

## 9.1 ASR

First candidate: **sherpa-onnx** with Vietnamese model benchmark set including `sherpa-onnx-zipformer-vi-30M-int8-2026-02-09`.

Required Vietnamese evaluation corpus:

- quiet speech;
- noisy room;
- near/far mic;
- numbers and VND amounts;
- dates/times/reminders;
- English names inside Vietnamese;
- code-switching;
- expense categories;
- user interruptions.

Track WER/CER plus task accuracy; a lower WER is not useful if money/time extraction is worse.

## 9.2 LLM

Candidates must run the same golden tool/eval corpus. Initial local candidate: `Qwen3.5-4B`; keep a fast-model/reasoning-model seam if one model cannot meet both latency and complex reasoning goals.

Metrics:

- tool selection accuracy;
- argument accuracy;
- invalid tool rate;
- hallucinated state rate;
- TTFT;
- tokens/sec;
- memory footprint;
- Vietnamese response quality;
- correction rate.

## 9.3 TTS

First Vietnamese local candidate: **VieNeu-TTS streaming**.

Measure on actual host hardware:

- first audio chunk;
- realtime factor;
- CPU/RAM;
- pronunciation of amounts/dates/names;
- VI/EN code switching;
- chunk-boundary prosody;
- interruption cancellation latency.

Provider-published latency claims are not release guarantees; only our measured result can satisfy the gate.

---

# 10. Cloud native realtime voice

Cloud live voice is an **optional runtime mode**, not a replacement for local/private mode.

```text
RealtimeVoiceProvider
├── Gemini Live adapter
├── OpenAI Realtime adapter
└── future S2S provider
```

Provider capabilities differ, so do not force them through a text-only `model.LLM` interface.

Required common abstraction:

- audio input/output stream;
- turn detection events;
- interruption event;
- function/tool calls;
- session resume/reconnect where supported;
- transcript events where supported;
- provider usage/latency metrics.

Backend/device still maintains its own `turn_id/generation_id` and clears stale output on barge-in.

---

# 11. Context and memory

## 11.1 Context

Replace “keep last N messages” as the core strategy.

Per invocation, assemble bounded working context from:

```text
system/product instructions
+ current session events
+ relevant recent turns
+ authoritative domain state/resources
+ selected long-term memories
+ tool/skill declarations needed for this turn
+ current user request
```

Token budget and relevance determine what enters context.

## 11.2 Memory

Do not choose a memory vendor by marketing claim. Benchmark with Vietnamese, temporal conflicts and long-lived single-user data.

Candidates:

- Graphiti temporal context graph;
- Mem0/self-hosted or another memory adapter if it wins our corpus;
- a simpler Postgres/pgvector implementation if external frameworks add more failure modes than value.

Memory interface remains replaceable.

### Source-of-truth rule

**Postgres:** expense, budget, reminders, timers, notes/journal IDs, device config, permissions, package/OTA state.

**Memory system:** preferences, stable personal facts, episodic recollection, relationships, evolving user context.

Vector/graph search is never authoritative for current money/schedule/config state.

---

# 12. Dynamic UI, assets and extension packages

## 12.1 Dynamic UI

Use **LVGL 9.6 XML runtime** for screens/components/styles/data binding that can change without firmware recompilation.

Built-in/custom native widgets still live in firmware; runtime XML assembles them.

## 12.2 Assets

Use **`esp_mmap_assets` 2.x** for packaged/memory-mapped fonts/images/animations/backgrounds rather than building a new SPIFFS asset packer from scratch.

## 12.3 Signed Companion Package

Planned package:

```text
feature.cap
├── manifest.json
├── ui/*.xml
├── assets/*
├── module/*.wasm      # optional
└── signature
```

Manifest includes:

- package ID/version;
- minimum firmware/host ABI;
- asset/UI entry points;
- required permissions/capabilities;
- optional backend/MCP tool names;
- hashes/signature;
- resource budget declaration.

Install flow:

```text
download -> hash/signature verify -> compatibility check
-> resource/permission check -> stage inactive -> load/health check
-> activate -> rollback on failure
```

## 12.4 WebAssembly

Candidate runtime: **WAMR**.

Use Wasm only for small isolated local extension logic after CP11 proves the RAM/CPU/security budget. It is behind a feature flag and is **not required for the base product**.

Wasm host capability API is allow-listed, e.g.:

```text
ui.*
storage.scoped.*
timer.*
tool_call(allow-listed name)
```

No raw flash, Wi-Fi credential, OTA partition, arbitrary socket or unrestricted device-driver access.

Remote MCP calls remain backend-owned:

```text
Wasm -> host.tool_call -> Go backend -> ADK/MCP -> result -> Wasm/UI
```

Espressif ELF loader may be evaluated for trusted first-party native extensions, but downloaded/untrusted feature packages do not receive native ELF execution in Production v1.

---

# 13. Remote config and feature flags

Use **OpenFeature Go SDK** as the application-facing feature flag API.

Separate concerns:

```text
Remote config != feature flag != entitlement != authorization
```

Examples of flags:

```text
voice.webrtc
voice.cloud_live
voice.local_asr_v2
model.qwen35
memory.graphiti
ui.runtime_xml
extensions.wasm
```

Every risky migration uses shadow/canary/rollback even for one user:

```text
OFF -> internal test -> shadow -> 1-device enabled -> soak -> default ON
```

---

# 14. Observability and performance

Use **OpenTelemetry** for traces + metrics; structured Go logs may stay `slog` until OTel logs leave beta or we have a concrete reason to change.

Each turn trace should contain:

```text
speech_start
vad_start / vad_stop
asr_first_partial
asr_final
agent_start
llm_first_token
tool_start / tool_end
llm_first_speakable_segment
tts_first_audio
transport_first_send
device_first_playback
turn_end / interruption
```

Also track:

- queue depth/drops;
- WebRTC/WSS reconnects;
- packet/audio sequence gaps;
- CPU/heap/PSRAM on device;
- Go goroutines/GC pauses;
- DB latency;
- River job lag/retries;
- model token usage;
- memory retrieval latency;
- package/OTA failures.

### Initial performance targets — **targets, not verified claims**

- No unbounded queue or goroutine growth during 24-hour soak.
- Barge-in stops stale local playback fast enough to feel immediate; numeric SLO is set after CP3/CP4 measurement.
- Local voice first-audio target: < 1.5 s on the selected production host, then optimize lower.
- Cloud live voice first-audio target: < 800 ms under good network when provider supports it.
- No lost reminder after backend restart.
- No duplicate expense/reminder from retransmission/idempotent retry.
- Firmware/image/package updates always have a tested rollback path.

---

# 15. Security and privacy

## Device

- TLS verification mandatory in production.
- Unique device identity/credential; no shared hard-coded cloud secret.
- NVS encryption/secure storage for device credential where appropriate.
- A/B OTA and boot self-test before marking new image valid.
- Secure Boot v2 + Flash Encryption only after recovery/manufacturing flow is tested on sacrificial hardware.
- Anti-rollback/security-version strategy before irreversible eFuse production provisioning.

## Backend

- Secrets from environment/secret store, never source or firmware assets.
- Validate every tool argument host-side.
- Authorization at product command/data boundary, not only prompt/tool metadata.
- Dependency pinning and automated vulnerability/dependency review.
- Backup/restore test for Postgres and user media.

## Personal data

- Audio retention configurable; do not save raw voice by default unless feature requires it.
- Long-term memory opt-out and explicit forget/delete path.
- Current domain state is deletable/exportable independently from conversation memory.
- External providers receive only the minimum context required by the selected mode.

---

# 16. Rollout plan in detail — software first

The order below is binding for the current rewrite. **Do not switch to hardware work until the requested software checkpoints are completed or explicitly reprioritized.** Every checkpoint updates this README with: status, code changed, tests executed, failures, solution, trade-offs, rollback path, next gate, and an **independent static review** performed after implementation but before tagging/snapshotting. A checkpoint cannot close until review findings are fixed or explicitly accepted, relevant tests are rerun, and the review status is recorded as PASS.

## CP-SW1 — Realtime turn runtime + streaming foundation — ✅

Delivered in this checkpoint:

- provider-neutral streaming-agent interface;
- deterministic sentence/clause segmentation;
- overlapping LLM-stream → segment → TTS execution;
- generation-safe stale-output rejection;
- explicit interruption event;
- exact production Go 1.26.5 pin for release environments;
- regression/race/E2E coverage.

Rollback: the existing non-streaming `Agent.Respond/RichAgent` path remains intact.

## CP-SW2 — Google ADK Go v2.2 + model gateway — 🟡

CP-SW2.1 has delivered the reversible adapter/tool/streaming/usage seam. Remaining work is the exact production dependency/compile gate and broader parity.

Plan / remaining gates:

1. Add ADK v2.2 behind `AgentRuntime`/adapter boundary, not directly throughout domain code.
2. Start with the official experimental ADK OpenAI adapter only for OpenAI **Responses API**-compatible endpoints; do not assume a generic `/chat/completions` server is compatible.
3. Add Gemini-native model adapter separately.
4. Map existing read/write capabilities to typed ADK `FunctionTool`s.
5. Preserve existing Qwen implementation behind a rollback feature flag until parity is proven.
6. Add provider matrix tests: fake model, local OpenAI-compatible endpoint contract, tool calls, streaming, cancellation, malformed tool args.
7. Add session isolation tests and a deterministic model/tool replay harness.

Exit gate:

- exact Go 1.26.5 build;
- ADK v2.2 dependency resolved and locked;
- existing expense/budget/note/reminder E2E parity;
- streaming + tool-call + cancellation tests green;
- no domain package imports an ADK package directly.

## CP-SW3 — PostgreSQL + Ent + Atlas + River

Plan:

- model current authoritative domain state in Ent schemas;
- use Atlas versioned migrations;
- migrate SQLite test fixtures/data with parity checks;
- River for reminder/timer/background durable jobs;
- transactionally commit state + job where required;
- idempotency keys on externally retried mutations;
- backup/restore and crash-recovery tests.

Keep SQLite only as a lightweight local/test compatibility adapter if useful; Production v1 server source of truth becomes PostgreSQL after parity.

## CP-SW4 — ADK tools, skills, sessions and context

Plan:

- replace primary custom keyword `ContextRouter`;
- replace custom model tool loop with ADK typed tools/workflows;
- progressive disclosure/skills so unrelated tools are not stuffed into each turn;
- authoritative domain reads stay direct/tool-backed, never memory hallucinations;
- scoped session/event history;
- compaction/token budget tests;
- destructive-action confirmation/policy boundary.

Delete custom framework-like code only after parity tests pass.

## CP-SW5 — Local AI runtime

Benchmark, do not assume:

- Vietnamese streaming ASR candidates;
- Qwen3.5-class local models and alternatives on the actual host hardware;
- streaming Vietnamese TTS candidates;
- partial ASR, first-token, first-segment, first-audio timestamps;
- code-switching and tool-call accuracy.

The selected components remain replaceable providers.

## CP-SW6 — Personal memory/context

Build a Vietnamese evaluation corpus first, then benchmark memory engines/adapters. Requirements:

- preference/fact extraction;
- provenance;
- temporal update/supersession;
- conflict handling;
- semantic + lexical retrieval quality;
- deletion/privacy;
- source-of-truth separation from expense/budget/reminder/device state.

No memory vendor is promoted to production before this benchmark.

## CP-SW7 — Dynamic UI/assets/extension package control plane

Server-side work first:

- signed `Companion Package` manifest and compatibility rules;
- package catalog/install/rollback metadata;
- capability permissions;
- UI schema/XML/assets manifest;
- Wasm extension manifest and host-capability ABI design;
- no arbitrary MCP/network access from device extensions; remote tool calls proxy through backend/ADK.

Firmware execution of LVGL XML/WAMR is deferred until software package semantics and signing are stable.

## CP-SW8 — MCP, OpenFeature, OpenTelemetry, security

- official MCP Go SDK for external integrations;
- OpenFeature for kill switches/rollouts;
- OpenTelemetry traces/metrics for every voice turn and tool execution;
- structured `slog`;
- secret management and outbound allowlists;
- prompt/tool provenance boundaries;
- timeouts/retries/circuit-breaking where external calls exist;
- fault-injection tests.

## CP-SW9 — Optional cloud native realtime voice

Add a separate `RealtimeVoiceProvider` abstraction; do not force native S2S into the text-model `model.LLM` abstraction. Required parity:

- Gemini Live adapter;
- OpenAI Realtime adapter;
- audio interruption;
- tool/function call bridge through backend;
- transcript/session persistence policy;
- fallback to local cascade runtime.

## CP-FW1 / CP-FW2 / CP-HW1 — deferred

Only after the software checkpoints requested above: ESP-IDF migration, ESP-SR AEC/VAD/WakeNet, WebRTC/LVGL/package runtime, then physical power/acoustic/RF/PCB validation. These remain documented in the firmware/hardware sections but are not the current work queue.

## CP-RC1 — production release candidate

Final gate includes software + firmware + hardware integration, long soak, fault injection, backup/restore, security provisioning, OTA rollback and release artifact verification.

# 17. Test layers

```text
L0 static/lint/schema checks
L1 pure unit tests
L2 component tests with fake adapters
L3 backend integration: Postgres/River/ADK fake model
L4 voice pipeline integration with recorded audio
L5 network fault tests WSS/WebRTC
L6 ESP32 HIL
L7 end-to-end real voice -> tool -> TTS -> speaker
L8 soak/fault/security/OTA release tests
```

No checkpoint can skip directly to L7 and be considered production-ready if lower deterministic gates are missing.

---

# 18. Checkpoint update template

Every implementation checkpoint adds an entry here and updates the dashboard.

```markdown
## YYYY-MM-DD — CPx.y <name>

Status: PASS | PARTIAL | BLOCKED | ROLLED BACK

Changed:
- ...

Tests executed:
- command -> PASS/FAIL
- HIL scenario -> PASS/FAIL

Independent static review:
- Static review status: PASS | FAIL
- correctness / state-machine invariants:
- concurrency / cancellation / lifecycle:
- error semantics / retry / idempotency:
- security / privacy / trust boundaries:
- performance / resource bounds:
- maintainability / dependency and architecture boundaries:
- findings fixed or explicitly accepted:
- tests rerun after review:

Measured:
- latency:
- heap/PSRAM:
- CPU:
- binary size:

Problems found:
- ...

Root cause:
- ...

Solution:
- ...

Trade-offs accepted:
- ...

Rollback path:
- ...

Next:
- ...
```

---

# 19. Decision log — current production choices

| Decision | Current choice | Why | Revisit condition |
|---|---|---|---|
| MCU | ESP32-S3 | Already owned; Wi-Fi + PSRAM + audio support; official ESP-SR/WebRTC path | CPU/RAM/display/vision budget fails |
| Firmware | ESP-IDF 5.5.4 | Stable; ESP-SR IDF6 support still experimental | ESP-SR/WebRTC fully stable on IDF6 |
| Audio DSP | ESP-SR 2.4.7 | Official S3 AFE/AEC/VAD/Wake stack | HIL quality/footprint fails |
| Transport | WebRTC candidate + WSS fallback | Official Espressif stack; WSS remains simple/reliable fallback | benchmark decides default |
| Agent | Google ADK Go v2.x | Go-native graph/workflow/tool/session model | local-model compatibility or overhead fails |
| Go | 1.26.x patched | Current supported stable line | normal patch/minor cadence |
| DB | PostgreSQL 18.x | Current supported production major | provider/compatibility issue |
| ORM | Ent | typed Go data model + privacy hooks | complexity outweighs generated value |
| Migration | Atlas versioned | reviewable, deterministic production schema changes | tool cost/ops issue; SQL migration fallback remains possible |
| Jobs | River | transaction-safe Postgres jobs, no extra queue service | requirements exceed Postgres job model |
| MCP | official Go SDK | avoid custom protocol | spec/runtime requirement changes |
| Flags | OpenFeature | vendor-neutral flag API | none unless project becomes unnecessary |
| Telemetry | OpenTelemetry | standard traces/metrics | none |
| Dynamic UI | LVGL 9.6 XML | runtime declarative UI, firmware-native widgets | XML runtime footprint/reliability fails |
| Assets | esp_mmap_assets 2.x | official efficient asset packaging | measured alternative wins |
| Extension runtime | WAMR candidate | sandboxed portable logic | CP11 footprint/security gate fails |
| Local ASR | sherpa-onnx candidate | current Vietnamese model availability + native API | corpus benchmark loses |
| Local TTS | VieNeu candidate | Vietnamese/offline/streaming | benchmark loses |
| Local LLM | Qwen3.5-4B first candidate | modern small model; not locked | golden eval selects another model |
| Memory | benchmark before selection | temporal/personal memory is too important to guess | choose only after CP9 corpus |

---

# 20. Known risks / hard problems

1. **AEC is hardware + acoustics + DSP**, not a library checkbox. Enclosure layout may force hardware revision.
2. **WebRTC footprint on S3** may compete with AEC/LVGL/WAMR; feature budgets must be measured together.
3. **Wasm is optional**, not an architectural dependency. Product must work without it.
4. **Local AI host performance** determines whether “offline-first” feels instant; benchmark on the intended always-on host, not only Mac developer hardware.
5. **Memory extraction noise** can degrade a companion over months. Salience/forget/provenance tests are mandatory.
6. **Cloud live APIs change quickly**. Keep provider adapters and a local fallback.
7. **Security eFuses can be irreversible**. Never enable production fuses before manufacturing/recovery procedure is proven.
8. **Dynamic package/UI update expands attack surface**. Every package is signed, scoped and rollback-capable.
9. **Single-user does not mean no authorization**. Device/tool boundaries still need ownership and capability checks.
10. **README drift is a bug**. Status must be updated in the same change that changes implementation/gates.

---

# 21. Research baseline used for this rewrite

Research refreshed on **2026-08-12** from primary/project documentation. Version pins are candidates until dependency lock + build/test checkpoint proves them.

- Google ADK Go 2.x: graph workflows, conditional routing, retries/timeouts, HITL, Go 1.25+.
- Go 1.26.x current supported line.
- ESP-IDF stable currently has a 6.x line, but Production v1 holds on IDF 5.5.4 while ESP-SR declares IDF 6 experimental.
- ESP-SR 2.4.7: AFE, WakeNet, VADNet, Full-Duplex AEC on ESP32-S3/P4.
- Espressif WebRTC solution 1.2.x: S3 support, Opus, data channels, TURN/NACK and product demos.
- LVGL 9.6 runtime XML.
- Espressif `esp_mmap_assets` 2.0.x.
- WAMR 2.4.x patched line; security fixes make exact version pin important.
- Official MCP Go SDK.
- Ent + Atlas.
- River.
- OpenFeature Go SDK.
- OpenTelemetry Go.
- PostgreSQL 18.x patched minor.
- sherpa-onnx Vietnamese ASR candidates.
- VieNeu-TTS streaming candidate.
- Qwen3.5-4B first local LLM candidate.
- Graphiti/Mem0/simple temporal-vector alternatives to benchmark rather than assume.

---

# 22. Next action

**Current work queue: finish CP-SW2 after CP-SW2.1 reversible seam.**

Implementation sequence:

1. In an environment with outbound dependency access, run exact Go **1.26.5**, `go mod tidy`, `go mod verify`, and commit the resulting `go.sum` lock.
2. Run `make backend-adk-gate` so the tagged ADK fake-model/tool/usage compile tests execute on the exact production toolchain.
3. Add an OpenAI Responses-compatible local endpoint contract test and cancellation/barge-in integration test.
4. Compare representative ADK tool behavior against the existing compatibility agent.
5. Keep `legacy` as default; broader tool/context/session replacement remains CP-SW4 after parity is measured.

Hardware work is intentionally not in the current queue.

# Companion Production v1 — Single-User AI Voice Device

> **Status:** Production rewrite in controlled rollout  
> **Checkpoint:** CP5.1 — model gateway provider seam extracted + regression green
> **Updated:** 2026-08-12  
> **Source baseline:** `esp32-companion-poc-commercial-prod-shaped-20260811`  
> **Rule:** a feature is never marked ✅ until its stated test gate passes. Research choice ≠ implemented. Source-complete ≠ hardware-proven.

This repository is no longer treated as a throwaway POC. The target is a **single-user production companion device** built around ESP32-S3 with a Go backend, local-first voice/LLM capability, optional cloud realtime voice, durable personal features, secure updates, dynamic UI/assets, and replaceable providers.

The previous README is preserved at `docs/LEGACY_POC_README_20260811.md` for audit/history.

---

## 0. Checkpoint dashboard

Legend: ✅ passed gate · 🟡 in progress / partial · 🔴 not started · 🧪 candidate under benchmark · ⚠️ requires physical hardware / external provider.

| Checkpoint | Scope | Status | Exit gate |
|---|---|---:|---|
| CP0 | Freeze baseline + production README + current tests | ✅ | Baseline archived; host + offline backend regression pass; no false production claims |
| CP1 | Physical hardware stabilization | 🟡 ⚠️ deferred | Mic + speaker + power + display SKU proven simultaneously on bench |
| CP2 | Production firmware foundation | 🔴 | ESP-IDF build/flash/HIL passes; Arduino removed from production path |
| CP3 | Audio front-end / hands-free | 🔴 ⚠️ | AFE/AEC/VADNet/WakeNet tuned; measured hands-free barge-in works |
| CP4 | Realtime session + transport | 🔴 | WSS baseline retained; WebRTC benchmark passes; cancel/backpressure/late-frame tests pass |
| CP5 | Go production platform | 🟡 | Go 1.26.x + ADK Go integration + provider/model seam passes tests |
| CP6 | Product domain + data layer | 🔴 | PostgreSQL + Ent + Atlas + River migration passes parity + recovery tests |
| CP7 | Agent/tools/context | 🔴 | ADK workflow replaces custom router/tool loop without feature regression |
| CP8 | Local AI runtime | 🔴 | Vietnamese ASR + local model + streaming TTS meet quality/latency gates |
| CP9 | Memory/context | 🔴 | Memory candidate wins Vietnamese benchmark; temporal conflict tests pass |
| CP10 | Dynamic UI/assets/package runtime | 🔴 | LVGL XML + mmap assets; signed package install/rollback passes |
| CP11 | Optional Wasm extension runtime | 🔴 🧪 | WAMR RAM/CPU/security quotas pass on S3; disabled if budget fails |
| CP12 | Cloud native realtime voice | 🔴 | Provider-independent live voice seam; interruption/tool/session tests pass |
| CP13 | OTA/security/observability hardening | 🔴 ⚠️ | A/B OTA rollback + signing + telemetry + recovery + security provisioning tested |
| CP14 | Production RC | 🔴 ⚠️ | 24h+ soak, fault injection, HIL, backup/restore, release checklist all green |

### CP0 evidence — 2026-08-12

**Verified in this environment:**

- `scripts/check.sh`: host C++ regression `2/2 PASS`.
- Partition/budget gate: `2 x 4 MiB OTA slots`, partition end within 16 MiB flash, current design SRAM budget script passes.
- `scripts/e2e_offline.sh`: host regression passes and backend functional E2E runs with race detection; all exercised backend packages pass.
- Existing source remains intact; only documentation/checkpoint metadata is changed in CP0.

**Not verified in this environment:**

- Production Go toolchain target (current container has Go 1.23.2; Production v1 moves to Go 1.26.x).
- ESP-IDF target compile/flash because `idf.py` is not installed in this container.
- Physical ESP32-S3 HIL from this container.

**Current real hardware bench note:** speaker/MAX98357A path has produced audio; INMP441 raw test is still unresolved (`min=-1 max=-1`) and is CP1's first blocker.

---

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

# 16. Rollout plan in detail

## CP1 — Hardware stabilization

**Implement**

- Fix INMP441 signal/contact issue.
- Verify exact board/module/display SKU.
- Update wiring/BOM to actual 8 Ω speaker and real display.
- Proper wiring/connector/decoupling bench revision.
- Simultaneous mic + speaker I2S test.

**Test**

- raw mic range/RMS/record playback;
- speaker stress;
- simultaneous RX/TX;
- power/brownout;
- Wi-Fi + audio coexistence.

**Trade-off / note**

Do not design PCB/enclosure around an unproven mic/display stack. S3 remains the default until measured constraints prove otherwise.

---

## CP2 — ESP-IDF production firmware

**Implement**

- Move production build to ESP-IDF 5.5.4.
- Component Manager lock/pins.
- Board HAL for exact pins/peripherals.
- Keep host-testable core separated from IDF adapters.
- Reproduce existing speaker/mic/display/network behavior.

**Test**

- `idf.py build`, `size`, `size-components`, `size-files`;
- flash + reboot loop;
- heap/PSRAM instrumentation;
- HIL smoke test.

**Rollback**

Legacy Arduino bench sketch remains a hardware diagnostic only, not a production fallback image.

---

## CP3 — ESP-SR audio front-end

**Implement**

- AFE;
- full-duplex AEC;
- VADNet;
- WakeNet;
- physical button fallback.

**Test**

- false wake / missed wake;
- echo during TTS;
- near/far field;
- user interruption while speaker active;
- multiple speaker volume levels.

**Trade-off**

More DSP costs CPU/RAM. Promote only the configuration that retains safe headroom.

---

## CP4 — Realtime transport/session

**Implement**

- Production `RealtimeVoiceSession` state machine.
- Bounded queues and one outbound audio writer.
- Generation invalidation.
- WSS adapter parity.
- Espressif WebRTC adapter.

**Test**

- packet loss;
- reconnect;
- Wi-Fi roam/drop;
- server restart;
- slow consumer/backpressure;
- stale frame after cancel;
- memory/CPU comparison WSS vs WebRTC.

**Decision gate**

WebRTC becomes default only if measured reliability/latency outweighs its footprint. WSS remains supported.

---

## CP5 — Go 1.26 + ADK platform

**Implement**

- toolchain update;
- ADK v2.x integration;
- model gateway;
- session adapter;
- workflow test harness.

**Test**

- deterministic fake model;
- streaming model events;
- tool calls;
- cancellation;
- retry/timeout;
- session isolation;
- race detector.

---

## CP6 — Postgres/Ent/Atlas/River

**Implement**

- Ent schemas for current domain state;
- versioned Atlas baseline migration;
- parity import from SQLite fixtures;
- River jobs for timers/reminders/cleanup.

**Test**

- migration up from empty DB;
- migration from representative legacy state;
- transaction rollback;
- reminder persistence across restart;
- duplicate/idempotency tests;
- Postgres 17 + 18 CI matrix.

---

## CP7 — ADK agent/tools/context migration

**Implement**

- typed internal FunctionTools;
- finance/product skills;
- remove custom keyword ContextRouter path after parity;
- bounded context assembly using ADK/session processors;
- official MCP adapter for external tools.

**Test**

Golden corpus must cover all existing expense/budget/note/journal/timer/reminder/voice-memo behaviors plus malformed/ambiguous/adversarial tool calls.

**Rollback**

Run new ADK route in shadow mode against old agent until output/tool parity is understood.

---

## CP8 — Local AI

**Implement/benchmark**

- sherpa-onnx Vietnamese ASR candidates;
- Qwen3.5-4B and at least one comparison model/runtime;
- VieNeu streaming TTS.

**Test**

Quality + latency + memory + VI/EN code-switching. Do not select a model solely by public benchmark.

---

## CP9 — Memory

**Implement**

`MemoryPort` adapters and an evaluation harness first; then Graphiti/Mem0/simple Postgres-vector candidates.

**Test corpus**

- “I dislike coffee” -> later “I like coffee now”;
- current vs historical preference;
- Vietnamese paraphrases without exact keyword;
- irrelevant chatter should not become high-salience memory;
- forget/delete;
- provenance;
- 1k/10k memory retrieval latency.

---

## CP10 — Dynamic UI/assets/packages

**Implement**

- LVGL XML runtime;
- `esp_mmap_assets`;
- typed UI subjects/events;
- signed package manifest/install/rollback.

**Test**

- malformed XML;
- incompatible screen/package;
- corrupted asset hash;
- power loss during package install;
- rollback to last-known-good package.

---

## CP11 — WAMR extension runtime

**Implement only if benchmark passes**

- WAMR 2.4.x pinned patched release;
- strict host capability API;
- memory/stack/instruction/time quotas;
- no generic networking/WASI exposure by default;
- signed package requirement.

**Kill condition**

If WAMR materially compromises audio reliability, heap headroom or security simplicity on S3, Production v1 ships LVGL XML/assets without Wasm.

---

## CP12 — Cloud live voice

**Implement**

- `RealtimeVoiceProvider` interface;
- Gemini Live adapter;
- OpenAI Realtime adapter;
- transcript/tool/interruption normalization.

**Test**

- 15+ minute lifecycle/reconnect handling;
- barge-in;
- provider timeout;
- tool call mid-audio;
- network loss;
- fallback to local cascade.

---

## CP13 — Security/OTA/observability

**Implement**

- A/B firmware downloader + pending-verify self-test;
- signed metadata/artifacts;
- OTel traces/metrics;
- backup/restore scripts;
- OpenFeature rollout controls;
- production key/provisioning runbook.

**Hardware security gate**

Secure Boot/Flash Encryption/eFuse changes occur only after recovery path is repeatedly proven on sacrificial boards.

---

## CP14 — Production RC

Required release gates:

- 24h+ device/backend voice soak.
- Repeated Wi-Fi disconnect/reconnect.
- Backend kill/restart during active turn and active reminder.
- Database backup + restore.
- OTA good image, bad hash, bad signature, boot-fail rollback.
- Package good/bad/rollback.
- No unbounded memory/goroutine growth.
- Current golden agent/tool corpus green.
- Hardware power/audio/display stress green.
- README/status exactly matches what has actually passed.

---

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

# 18. Checkpoint log

Every implementation checkpoint adds an entry here and updates the dashboard. Every PASS checkpoint also has a Git tag/snapshot so it can be restored without reconstructing changes manually.

## 2026-08-12 — CP5.1 Model gateway extraction

Status: PASS

Changed:
- Extracted a provider-neutral `ModelGateway` boundary from `agent.Qwen`.
- Moved OpenAI-compatible HTTP request/response handling into `OpenAICompatibleGateway`.
- Kept conversation history, context planning, policy, tool execution, idempotency and usage accounting in the agent/runtime layer rather than the provider adapter.
- Added `WithModelGateway` injection for local/cloud/fake/future ADK model adapters.
- Added deterministic model gateway tests for response policy, tool policy and provider error propagation.
- Hardware work is explicitly deferred while software checkpoints proceed.

Tests executed:
- `GOTOOLCHAIN=local CGO_ENABLED=1 go test -race -count=1 -modfile=go.offline.mod ./internal/agent` -> PASS.
- `bash scripts/e2e_offline.sh` -> PASS. Host C++ simulator `2/2`; partition/SRAM budget PASS; all backend packages exercised under race detector PASS.
- `git diff --check` -> PASS.

Measured:
- No firmware binary/heap change: backend-only checkpoint.
- Deterministic agent package race test: PASS.

Problems found:
- Current execution container is Go 1.23.2 and has no outbound network, so Go 1.26.5 toolchain and ADK v2 dependencies cannot be downloaded/compiled here yet.

Root cause:
- Sandbox network/toolchain availability, not source incompatibility.

Solution:
- Split CP5 into independently reversible sub-checkpoints. CP5.1 lands the provider seam using the existing offline dependency set; actual Go/ADK dependency migration remains a later gate and is not falsely marked complete.

Trade-offs accepted:
- The legacy type name `Qwen` remains temporarily to reduce migration risk; model/provider behavior is no longer hard-coupled to Qwen. Rename/removal can happen when the ADK runner owns orchestration.

Rollback path:
- Git tag `CP5.1-20260812` restores this exact checkpoint.
- Git tag `CP0-20260812` restores the frozen production baseline before the provider seam.

Next:
- CP4.1 software-only realtime session state machine: bounded queues, generation invalidation, stale-frame rejection, cancel/barge-in tests.
- CP5.2 toolchain/ADK integration proceeds when an environment can fetch/pin Go 1.26.5 + ADK Go v2.2.x and run its real compile gate.

## Checkpoint update template

```markdown
## YYYY-MM-DD — CPx.y <name>

Status: PASS | PARTIAL | BLOCKED | ROLLED BACK

Changed:
- ...

Tests executed:
- command -> PASS/FAIL
- HIL scenario -> PASS/FAIL

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

**Software-first rollout is active. Hardware CP1/CP3 are deferred, not waived.** Continue deterministic backend/transport/platform checkpoints that can be fully verified without the physical ESP32. The immediate next checkpoint is CP4.1: introduce the production realtime session state machine with bounded queues, generation invalidation and stale-frame/cancel tests.

Dependency-heavy CP5.2 (Go 1.26.5 + ADK Go v2.2.x) and CP6 (Postgres/Ent/Atlas/River) remain separate reversible checkpoints; they are promoted only in an environment that can fetch the pinned dependencies and execute the real compile/integration gates. Hardware validation resumes later at CP1/CP2/CP3 and remains mandatory before Production RC.

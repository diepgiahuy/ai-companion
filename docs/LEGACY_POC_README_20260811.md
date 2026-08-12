# ESP32-S3 Companion — Hardware POC

Minimal, hardware-first proof of concept matching the Xiaozhi-style breadboard:

- ESP32-S3 DevKitC-1 N16R8
- INMP441 I2S microphone
- MAX98357A I2S amplifier + 4 ohm / 3 watt speaker
- SSD1306 128x32 I2C OLED
- one push-to-talk button

The ESP32 owns audio I/O and the interaction state machine. AI and persistence
sit behind a replaceable `VoiceBackend`. This POC now includes both a
deterministic mock and a real Wi-Fi/WebSocket adapter, plus a self-hosted Go
backend. Start with the mock, then switch one Kconfig option for end-to-end work.

## Proven by the current code

- Independent ESP32-S3 I2S RX and TX controllers.
- INMP441 capture in 16 kHz mono, internally pumped in 20 ms blocks.
- Xiaozhi v1 raw Opus packets: 16 kHz uplink / 24 kHz downlink, mono, 60 ms.
- Mono-to-stereo playback for MAX98357A.
- Debounced push-to-talk button.
- SSD1306 UI: status states plus idle clock/next-reminder layout and alarm screen.
- Replaceable backend boundary; no keyword parser inside firmware.
- Bounded queues, non-blocking audio pump and button barge-in.
- Ordered WebSocket writes so `listen.stop` cannot overtake queued audio.
- Go server with WebSocket E2E tests, SQLite idempotency and Qwen tool calling. Qwen capability execution is registry-based rather than a hard-coded tool switch. A network-isolated `e2e-offline` gate now runs the full functional backend flow with the race detector.
- Host regression tests for streaming, barge-in, Smart VAD, idle state and local alarm beep.
- Two 4 MiB OTA slots for the N16R8 board. Server-side OTA metadata registry now supports board/protocol/security-version compatibility, SHA-256, expiry, metadata versioning and optional Ed25519 verification; device-side firmware download/rollback still requires ESP-IDF/HIL.
- Backend implementations for batch expenses, bounded read tools, reminder dispatch and WAV voice memos are runtime-tested by the offline functional E2E gate. The separate release gate remains pinned to Go 1.25 + production modules.

The mock backend does **not** understand speech. It counts captured audio and
returns a 200 ms confirmation tone plus `SAVED MOCK`. The real backend still
uses mock ASR/TTS by default so network/audio can be proven independently from
model providers.


## Architecture contract: replaceable providers

The backend follows **ports + adapters**. Provider choice belongs at the composition root; core orchestration must not depend on SQLite/Redis/Mem0/Graphiti/Qwen/MCP implementations. Conversation context now has a replaceable durable store plus a bounded hot cache (`memory` or `none` today; Redis is a future adapter). Domain state remains authoritative and must not be reconstructed from chat history. Native and external MCP capabilities now converge behind in-process `ToolRegistry` / `ResourceRegistry` ports; external MCP adapters are the next transport adapter, not a separate agent path. See `docs/ADR-001-REPLACEABLE-PROVIDERS.md`.

**Definition of done for refactors:** update this README/ADR, preserve protocol compatibility, run host regression + backend tests + WebSocket E2E, and only then mark a row green.

## Feature registry / roadmap (source of truth)

> **Keep this section updated whenever a feature is added or removed.** The status below is intentionally explicit so unfinished work is not mistaken for production-ready behavior.

Legend: ✅ implemented/tested · 🟡 partial/in progress · 🔴 planned/not implemented · 🧪 experimental · ⚠️ hardware/provider dependent.

### Production-shaped control plane (2026-08-11)

The POC now keeps **data plane, control plane and event plane** concerns separate while remaining one Go process. Remote config uses a desired/reported device twin with a globally monotonic config generation; resolution order is defaults -> global -> tenant -> plan -> user -> device. Feature Catalog, feature rollout flags, subscription entitlements, user privacy and tool authorization are separate stores/policies rather than one overloaded flag system. A transactional outbox is written atomically with authoritative state; full Event Sourcing is intentionally not used. See `docs/COMMERCIAL_ARCHITECTURE.md` and `docs/PRODUCTION_READINESS.md`.

| Control-plane capability | Status | Current implementation / boundary |
|---|---:|---|
| Device twin + remote config | ✅ | Desired/reported state, last-known-good firmware application, monotonic resolved snapshot version, scoped overrides and live push to connected devices. |
| Feature Catalog + flags | ✅ | Versioned feature manifests, lifecycle, deterministic percentage rollout, admin API; manifests are metadata and never load arbitrary code. |
| Entitlements + tool policy | ✅ | Durable entitlement boundary plus feature/privacy/destructive-intent authorization hooks and host-side tool JSON-schema validation. |
| Privacy / retention | ✅ | Per-user voice/memory/retention policy, memory opt-out, pre-write voice-memo audio gate and deterministic cleanup worker. |
| Transactional outbox | ✅ | Domain/config events persist in the same SQLite transaction as state; worker recovery/retry is idempotency-oriented. |
| LLM usage / quota | ✅ | Per-model/prompt token usage is stored; optional monthly guard prevents runaway LLM spend. |
| Feature code hot-loading | 🔴 | Deliberately not supported in-process. New native code deploys normally; future external modules must cross a policy-controlled MCP/adapter boundary. |


### Identity / ownership invariant

Backend identity is explicit: `user_id` owns expenses, budgets, notes and journal; conversation cache/history is `user_id + thread_id`; timers/reminders are `user_id + target_device_id`; voice memos are `user_id + source_device_id`. Legacy clients can still use the configurable single-user fallback for bench compatibility. In database-auth mode, enrolled device credentials resolve trusted `user_id + tenant_id + plan` claims and ignore client-supplied ownership/plan headers; raw tokens are never stored. Production hardware still needs secure provisioning/storage and TLS verification.

### Context, capability and provider architecture

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Conversation hot cache | 🟡 | Write-through bounded TTL/LRU-ish cache keyed by `user_id + thread_id`; append updates hot history instead of invalidating it. `NoopCache` remains available and Redis is a replaceable future adapter. |
| Durable conversation history | 🟡 | `conversation.Store` port with SQLite adapter, scoped by `user_id + thread_id`. Recent history is working context only; domain stores remain authoritative. |
| Conversation user control | 🟡 | History is append-only for integrity but the current `user_id + thread_id` can be explicitly cleared through the narrowly routed `conversation.clear` tool; clear invalidates the hot cache. |
| ToolRegistry | 🟡 | Qwen no longer executes a hard-coded switch. Native tools register behind a provider-neutral registry; hidden legacy tools remain callable for compatibility. Qwen now also depends on a `TurnResultStore` port rather than concrete SQLite. Full functional runtime E2E passes in the network-isolated harness; the exact Go 1.25 + upstream-module release gate still requires a Go 1.25/Docker environment. |
| ResourceRegistry | 🟡 | MCP-style URI resources are registered by scheme: `expenses://today`, `expenses://week/current`, `expenses://month/current`, `budget://daily`, `budget://weekly`, `budget://monthly`, `reminders://today`, `reminders://upcoming`, `timers://active`, `notes://recent`, `journal://today`, `conversation://recent`. |
| Generic resource tools | 🟡 | `resource.read` / `resource.list` remain callable for MCP/debug compatibility but are hidden from normal model discovery. `ContextRouter` now reads selected resources application-side before the LLM turn. |
| Deterministic ContextRouter | 🟡 | Selects only relevant resource URIs and strongly typed tool packs from the current transcript without a second planner-LLM call; unknown/general chat falls back to the full native pack set. |
| Timer/reminder domain separation | 🟡 | Scheduled rows now persist `kind=timer|reminder`; legacy DBs migrate with `reminder` default so active timers can be queried independently. |
| External MCP tools/resources | 🔴 | Add MCP client adapter implementing the existing registry ports; core agent/tool loop must remain unchanged. |
| Replaceable domain repositories | 🟡 | Typed ports cover Expense/Budget/Schedule/Note/Journal/VoiceMemo including CRUD. SQLite is the current adapter; Qwen/tool/resource code depends on ports, not SQL. |

### Core voice and interaction

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Push-to-talk conversation | ✅ | Button -> I2S mic -> Opus -> WebSocket -> ASR -> Agent -> TTS -> Opus -> speaker. |
| Opus streaming | ✅ | Raw Opus packets, 16 kHz uplink / 24 kHz downlink, mono, 60 ms frames. |
| Button barge-in | ✅ | Press during processing/speaking aborts the active turn and starts capture. |
| Smart VAD / automatic end-of-speech | 🟡 | Basic on-device energy VAD is implemented and host-tested: button starts capture, detected speech + silence automatically ends the turn. `menuconfig` exposes enable/threshold/silence/min-speech knobs for real INMP441 tuning. Manual fallback remains. |
| Wake word / always-listening mode | 🔴 ⚠️ | Planned ESP-SR WakeNet integration on ESP32-S3; should feed the same app/backend turn API rather than bypass it. |
| Acoustic Echo Cancellation (AEC) | 🔴 ⚠️ | Planned ESP-SR AFE/AEC path. Requires speaker playback reference plus physical speaker/mic isolation; do not claim full-duplex until measured on hardware. |
| Noise suppression / audio front-end | 🔴 ⚠️ | Planned ESP-SR AFE preprocessing before VAD/ASR. |
| Voice barge-in while TTS is playing | 🟡 ⚠️ | Button barge-in works; hands-free voice barge-in needs AEC + VAD/wake-word path. |
| Production streaming ASR | 🔴 | Backend interface exists; current default is deterministic MockASR. |
| Production Vietnamese streaming TTS | 🔴 | Backend interface exists; current default is deterministic tone TTS. |

### Notes, journal, expenses and memory

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Notes CRUD | 🟡 | `note.create/list/update/delete` are user-scoped, strongly typed tools over the replaceable NoteRepository port. |
| Journal CRUD | 🟡 | `journal.create/list/update/delete` are user-scoped; range reads stay bounded and authoritative in the repository. |
| Expense CRUD | 🟡 | `expense.log/list/query/summary/update/delete` are user-scoped; batch create is transactional/idempotent and reads use authoritative date/category filters. |
| Budget CRUD | 🟡 | `budget.get/set/delete` supports daily/weekly/monthly and is user-scoped; expense summaries combine authoritative spend totals with the selected daily/weekly/monthly limit. |
| Long-term personal memory / RAG | 🟡 | Temporal facts (`valid_from/valid_to`, provenance/confidence) + hybrid lexical/vector recall are implemented. Vector storage is a rebuildable secondary index; SQLite scan/hash embedding are POC adapters and OpenAI-compatible embeddings are supported. Production pgvector/Qdrant plus live-model quality evaluation remain external gates. |
| Speaker identification / voice print | 🔴 ⚠️ | Future backend embedding pipeline; requires enrollment/privacy controls and should never silently identify guests. |

### Voice notes and meeting capture

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Short Voice Note | 🟡 | `voice_memo.save` persists the current decoded 16-bit mono turn as an atomic valid WAV plus SQLite metadata only when user privacy allows audio persistence. Runtime/privacy tests pass in the offline functional E2E gate; exact Go 1.25/upstream-module release gate remains separate. |
| Voice Memo CRUD | 🟡 | `voice_memo.save/list/delete` is user-scoped and source-device aware; delete removes metadata and the owned WAV file. Audio playback/download API is still planned. |
| Long meeting recording | 🔴 ⚠️ | Separate recording session mode, chunked file writer, duration/storage limits and crash-safe finalization. |
| Meeting transcription + summary | 🔴 | Background ASR/LLM pipeline after recording; extract summary, decisions and action items. |
| Email/Telegram meeting delivery | 🔴 | Integration layer after summary is generated. |

### Reminders, timers and proactive assistant

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Reminder/timer create | 🟡 | Durable/idempotent SQLite write is user-owned and optionally targeted to a device through Qwen. Absolute schedules use `reminder.create`; relative timers use `timer.create(delay_seconds)` so Go—not the LLM—does the clock arithmetic. |
| Reminder/timer CRUD | 🟡 | `reminder.list`, `timer.list/pause/resume`, `schedule.update/cancel/delete`; pause preserves a timer's remaining duration and IDs are resolved against the user-owned schedule repository. |
| Reminder/timer scheduler | 🟡 | Durable delivery state is `pending -> dispatching -> sent -> fired`; ESP32 returns `alarm_ack`, and missing ACKs retry with bounded exponential backoff. `server` depends on a scheduler repository port rather than concrete SQLite. |
| Alarm downlink to ESP32 | 🟡 | User+device-scoped WebSocket hub pushes `{type:"alarm",id,...}` and firmware immediately returns `alarm_ack`; offline runtime E2E asserts final `fired` state, with the Go 1.25 production-module gate kept separate. |
| Alarm UI / sound | ✅ | Firmware alarm state is host-tested, queues alarms during active turns, renders OLED alarm text and plays a local ~880 Hz beep pattern. Proactive TTS alarm remains optional future work. |
| Upcoming reminder query/list | 🟡 | `reminder.list` and `NextReminder` implemented; scheduler pushes the next pending reminder summary only to sessions matching the owning user + target device. |
| Morning report | 🔴 | Scheduled proactive bundle: weather/news/tasks/reminders. |
| Smart proactive nudges | 🔴 | Rules/events -> WebSocket push -> optional TTS; must support quiet hours/rate limits. |

### OLED / companion experience

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Basic status UI | ✅ | CONNECTING / READY / LISTENING / PROCESSING / SPEAKING / IDLE / ALARM / ERROR state handling. |
| SNTP clock sync | 🟡 | Firmware starts SNTP after Wi-Fi and applies configurable POSIX TZ (`ICT-7` default). ESP-IDF target compile + physical sync still pending. |
| Idle clock | 🟡 | App enters idle after 5 s and OLED renders `HH:MM`; host state logic passes, ESP-IDF/OLED hardware verification pending. |
| Upcoming schedule on idle screen | 🟡 | Scheduler sends custom `schedule` downlink and OLED renders a second line. Vietnamese titles are degraded to ASCII for the current 5x7 font. Target E2E pending. |
| Animated eyes/avatar | 🔴 | Sprite frames mapped to ready/listening/processing/speaking/emotion. |
| Emotion engine | 🔴 | Backend may attach `emotion` hint; firmware maps it to local animations only. |
| Idle dashboards | 🔴 | Rotate clock, weather, next reminder/calendar, solar/lunar calendar and optional daily message. |
| Battery status | 🔴 ⚠️ | ADC/fuel-gauge dependent; display battery icon/percentage. |
| Wi-Fi RSSI status | 🔴 | Read RSSI and display signal icon. |

### Device lifecycle and connectivity

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Development Wi-Fi credentials via Kconfig | ✅ | Bench-only path. |
| Wi-Fi provisioning | 🔴 | BLE provisioning or SoftAP captive portal; credentials stored in NVS. |
| Re-provision/reset Wi-Fi | 🔴 | Long-press/button boot gesture plus NVS erase flow. |
| OTA partitions | ✅ | Partition table already reserves two OTA slots. |
| OTA update workflow | 🟡 ⚠️ | Server/control plane: board/channel/protocol/security-version compatibility, SHA-256, metadata expiry/version, optional Ed25519 signature, admin publish + authenticated device lookup. ESP-IDF binary download, pending-verify self-test, rollback/Secure Boot/eFuse HIL are not yet verified. |
| Device enrollment/token rotation | 🟡 | Server-side per-device enrollment/revoke is implemented; raw tokens are shown once, only hashes are stored, authentication uses constant-time comparison, and trusted user/tenant/plan claims come from enrollment. Hardware-backed secure provisioning/rotation remains HIL/security work. |
| TLS/WSS certificate verification | 🔴 | Required before Internet deployment. |
| Offline action queue | 🔴 | Persist selected writes while backend is unavailable, replay idempotently later. |

### AI modes and integrations

| Feature | Status | Current implementation / next step |
|---|---:|---|
| Qwen OpenAI-compatible agent | 🟡 | Optional `Qwen3-4B-Instruct-2507` endpoint; mock agent fallback. |
| Dynamic LLM provider | 🟡 | Current interface isolates the agent; add provider config/registry for Ollama/cloud models. |
| Dify / FastGPT / Coze / Ollama adapters | 🔴 | Implement backend adapters without changing firmware protocol. |
| Persona + voice switching | 🟡 | Device/user remote config carries stable `voice_key`, BCP-47-style locale and IANA timezone into per-turn ASR/LLM/TTS context. Provider voice catalogs/real streaming TTS remain provider-dependent. |
| English speaking tutor | 🔴 | Specialized prompt/tool mode for correction, repetition and scoring hooks. |
| Real-time translator | 🔴 | Dedicated low-latency ASR -> translation -> TTS mode with language pair state. |
| Weather tool | 🔴 | Backend live-data connector. |
| Gold / FX / market-data tools | 🟡 | Live provider registry/cache is implemented for CoinGecko, Twelve Data, Alpha Vantage gold and opt-in PNJ retail gold. Quotes carry source/as-of; retail gold carries bid/ask/unit. Deterministic threshold watches create durable alerts atomically. Internet/provider credentials and commercial data rights remain deployment gates. |
| News tool | 🔴 | Backend live-data connector + short spoken summaries. |
| Home Assistant / MQTT | 🔴 | Explicitly allow-listed device/action tools. |
| n8n / webhook automation | 🔴 | Outbound integration adapter with secrets kept server-side. |
| Telegram integration | 🔴 | Tool for sending user-authorized messages/notifications. |

### Architecture rules that should not regress

1. **Firmware stays thin:** hardware I/O, realtime audio, local UI, local wake/VAD/AEC and transport; semantic/business features remain on the backend.
2. **Backend owns durable data and tools:** notes, journal, expenses, reminders, recordings, integrations and LLM orchestration.
3. **No fake feature flags:** a feature is only marked ✅ when an automated test or physical acceptance test proves the path.
4. **Keep manual push-to-talk as fallback** even after wake word/VAD is added.
5. **Bound every queue, recording, tool result and retry loop** so a bad network/model/provider cannot exhaust ESP32 or backend memory.
6. **Idempotency for every side effect** (expense, note, reminder, integration send, recording metadata).
7. **Privacy by design:** speaker ID, long-term memory and meeting recording need explicit opt-in, deletion and retention controls.
8. **AEC is a system feature, not just a software switch:** speaker reference routing, latency alignment, enclosure layout and measured acoustic performance are part of acceptance.

### Implementation order / current progress

| Order | Milestone | Status |
|---:|---|---:|
| 1 | Reminder scheduler + WebSocket alarm + firmware alarm handling | ✅ offline runtime E2E + firmware host-tested; Go 1.25 production-module gate separate |
| 2 | Multi-item expenses + note/journal/expense/reminder query tools | ✅ implemented + offline runtime E2E; Go 1.25 production-module gate separate |
| 3 | Voice-note WAV persistence + metadata/list tool | ✅ implemented + offline runtime E2E; Go 1.25 production-module gate separate |
| 4 | SNTP + idle clock + next reminder | 🟡 implemented; ESP-IDF/hardware validation pending |
| 5 | Smart VAD with manual fallback | 🟡 basic energy VAD ✅ host-tested; physical tuning pending |
| 6 | ESP-SR AFE/AEC + WakeNet + hands-free barge-in | 🔴 ⚠️ next audio milestone |
| 7 | Wi-Fi provisioning + WSS/TLS + OTA | 🟡 server OTA/control plane implemented; device TLS/provisioning/OTA HIL pending |
| 8 | Long-term memory, speaker ID, proactive reports, integrations and specialized AI modes | 🟡 temporal/hybrid memory + market alerts implemented; speaker ID/integrations/specialized modes remain |

## What this upgrade actually changed

- Added production-shaped Control Plane boundaries: device twin/remote config, versioned Feature Catalog/flags, entitlements, privacy/retention, per-device credentials and signed/versioned OTA metadata.
- Added temporal memory plus replaceable vector secondary index/OpenAI-compatible embeddings; current facts are time-valid/provenanced and domain state remains authoritative SQL.
- Hardened Qwen tool execution with progressive tool packs, host JSON-schema validation, parallel tool-call request support, model routing, per-turn locale/timezone and usage/quota hooks.
- Added live market providers/cache/provenance and atomic threshold-crossing alerts; current retail-gold quote shape supports bid/ask instead of pretending every market has one price.
- Added globally monotonic resolved config versions so global/user override changes are not discarded by offline/reconnecting firmware as stale.
- Added bounded priority WebSocket queues, stage latency logging, transactional outbox and production-shaped GitHub Actions/devcontainer release environments.
- Pinned the backend/test toolchain to **Go 1.25.0** and made the reproducible E2E gate reject the wrong Go line.
- Added explicit `user_id/device_id/thread_id/session_id/turn_id` boundaries, legacy single-user ownership migration, and session-scoped turn idempotency so device reboot cannot reuse stale cached responses.
- Reworked conversation context to a write-through bounded cache over a replaceable durable store; thread history can be explicitly cleared without exposing destructive context tools on normal turns.
- Added deterministic `ContextRouter`, progressive tool packs and application-controlled MCP-style resources; `resource.read/list` stay hidden from normal model discovery.
- Completed strongly typed CRUD ports/tools for expenses, daily/weekly/monthly budgets, notes, journal, schedules and voice memos while keeping SQLite as only the current adapter.
- Added deterministic relative `timer.create(delay_seconds)` and durable reminder delivery `pending -> dispatching -> sent -> fired`, scoped user+device push, ESP32 `alarm_ack`, restart recovery and bounded retry/backoff.
- Emits authoritative UI presentations immediately after tool execution, before final LLM verbalization/TTS, with final-response fallback if the early UI queue is unavailable.
- Added short Voice Memo WAV persistence/metadata/delete, SNTP/TZ idle clock/next-reminder UI, and basic energy Smart VAD while preserving push-to-talk fallback.
- Documented the ESP-SR AFE/AEC/WakeNet integration boundary instead of pretending AEC works without a speaker-reference signal.

**Verification boundary for this ZIP:** C++ host regression is green and the network-isolated full backend functional suite passes with `-race`, including real SQLite/Opus native libraries, RFC6455 transport, Qwen fake endpoint/tool loop, scheduler ACK, UI/TTS and conversation persistence. The sandbox still cannot obtain the exact Go 1.25 toolchain or upstream Go modules because outbound TCP/DNS and Docker are unavailable, so the **release-toolchain gate** remains `make e2e-container`. ESP-IDF/hardware-only AEC/WakeNet remain separate HIL items.

## Refactor/E2E gate

For network-isolated development/sandboxes:

```bash
make e2e-offline
```

This runs host firmware regression plus the full backend suite with `-race` using the sandbox's local Go toolchain and test-only compatibility modules backed by system SQLite/Opus. In this delivery sandbox that toolchain is Go 1.23.2. It validates the complete functional flow, but it does **not** replace the exact Go 1.25 + upstream-module release gate.


Run the same acceptance gate after provider/context refactors:

```bash
make e2e-container
# or inside a Go 1.25 + libopus environment:
make e2e
```

`compose.e2e.yaml` uses the Go 1.25 `Dockerfile.test`. The gate builds host firmware simulation, runs C++ FSM/Opus regression, budget/partition checks, then runs all Go tests with `-race`, including WebSocket conversation/tool-loop/scheduler tests. Real ESP-SR AEC/WakeNet and physical I2S/OLED remain hardware-in-the-loop acceptance items.

## Repository map

```text
components/
  companion_app/       portable state machine, ports, mock backend
  esp32_board/         ESP-IDF I2S, GPIO and SSD1306 adapters
  esp32_network/       Wi-Fi station and bounded WebSocket adapter
main/                  production composition root
host/                  no-hardware simulator and regression tests
backend/               Go WebSocket server, Qwen adapter and SQLite store
wokwi/                 OLED/button wiring simulation
docs/                  wiring, architecture and acceptance tests
scripts/check.sh       host build/tests and optional ESP-IDF build
```

## Verify without hardware

```bash
./scripts/check.sh
printf 'press\ntick 20\ntick 20\npress\ntick 250\nquit\n' | ./build-host/companion_sim
docker compose run --build --rm test
docker compose build esp-idf
```

The check script uses CMake when available and falls back to `g++`.

## Build for ESP32-S3

Requires ESP-IDF 5.2 or newer:

```bash
idf.py set-target esp32s3
idf.py build
idf.py size
idf.py flash monitor
```

For the real backend, run `idf.py menuconfig`, enable
`Companion network POC -> Use real Wi-Fi/WebSocket backend`, and set the
development SSID and server URL. These sdkconfig credentials are for bench use;
production provisioning is a separate milestone.

See [`docs/WIRING.md`](docs/WIRING.md) before applying power,
[`docs/TEST_PLAN.md`](docs/TEST_PLAN.md) for the physical acceptance sequence, and
[`docs/AUDIO_FRONTEND.md`](docs/AUDIO_FRONTEND.md) for the AEC/WakeNet integration contract.
The exact purchase list is in [`docs/BOM.md`](docs/BOM.md).
The before/after repository review is in
[`docs/REPO_AUDIT.md`](docs/REPO_AUDIT.md).

## Honest simulation boundary

Wokwi can prove firmware boot, GPIO40 button handling, OLED rendering and the
state transition wiring. It does not currently provide faithful INMP441 and
MAX98357A acoustic simulation. I2S sound quality, mic bit alignment, amplifier
noise, brownout and acoustic feedback require the physical breadboard.

The voice transport is wire-compatible with the Xiaozhi WebSocket v1 Opus
profile. This POC now has basic button-started Smart VAD/automatic end-of-speech,
but deliberately still omits an **external MCP transport adapter**, hands-free wake word, AEC/full-duplex and the
on-device OTA downloader/rollback acceptance path. See [`docs/XIAOZHI_COMPATIBILITY.md`](docs/XIAOZHI_COMPATIBILITY.md).

## Expense intelligence / conversation context (2026-08-11)

Implemented backend flow for questions such as **"Tuần này tiêu hết bao nhiêu rồi?"**:

`Opus -> ASR -> identity/context -> Qwen -> expense.query -> authoritative repository + budget -> early UI -> Qwen synthesis -> TTS`.

- Conversation history is persisted by `user_id + thread_id`; the bounded hot window is maintained by a write-through cache so appends do not force the next turn back to SQLite.
- Device, user, thread, session and turn identities are separate. Legacy clients remain compatible through a default-user/device-thread fallback, while the production identity resolver is replaceable.
- `budget.get/set/delete` supports `daily`, `weekly` and `monthly` periods. Expense queries combine authoritative spend totals with the matching limit/remaining amount.
- A deterministic `ContextRouter` preloads only relevant application-controlled resources and exposes the matching typed tool packs; generic `resource.read/list` stay hidden from normal model discovery.
- The Qwen second pass receives the actual tool result before composing spoken advice; history is never treated as the source of truth for expenses, budgets, timers or reminders.
- Structured UI presentation is emitted as soon as an authoritative tool result is ready, before final verbalization/TTS. The display-agnostic JSON contract lets SSD1306 use a compact fallback today and ST7789 render richer cards later without changing backend capability logic.
- Conversation history can be explicitly cleared for the current scoped thread, and domain data uses user-scoped typed CRUD ports rather than raw SQL/model-controlled storage access.
- Tests cover context routing, write-through caching, tool selection, SQL/budget results, second-pass synthesis, UI generation, persistence and alarm ACK semantics. Deterministic fake OpenAI-compatible/Qwen endpoints are used for contract E2E; real-model quality evaluation remains a separate environment gate.

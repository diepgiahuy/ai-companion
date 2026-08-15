# Commercial architecture

This document records durable architecture, stable boundaries, and evolution seams for AI Companion. It is **not** a release checklist, live backlog, or branch-status dashboard. Current work belongs in GitHub Issues/PRs; merged code is product truth and `evidence/status.json` owns promoted evidence claims.

## Status language

Use these terms consistently:

- **Implemented:** code exists on merged `main`.
- **Tested:** a named oracle passed in a stated environment/code revision.
- **HIL-tested:** a named physical scenario passed on identified hardware.
- **Production-proven:** defined provider/physical/operational release criteria passed.
- **Planned:** accepted direction without an implementation claim.
- **Candidate:** an option still requiring research, benchmark, or decision.

Implementation is not automatically Production-proven.

## Current system shape

The product is a modular Go monolith plus ESP32-S3 firmware.

```text
ESP32-S3
  -> secure WebSocket / Companion Protocol v2
  -> Go realtime runtime
       -> speech/realtime provider adapters
       -> Google ADK
       -> ToolRegistry + policy
            -> native/domain tools
            -> authenticated device capabilities
            -> optional backend-side external MCP
       -> PostgreSQL / pgx + Atlas
       -> transactional outbox + River
       -> identity/twin/config/privacy/OTA control plane
```

Repository seams:

- `backend/internal/domain/`: domain types and ports.
- `backend/internal/pgstore/`: authoritative PostgreSQL persistence and outbox implementation.
- `backend/internal/store/`: SQLite migration/recovery adapter and isolated tests only.
- `backend/internal/controlplane/`: device identity/twins, scoped config, feature metadata, privacy, entitlements and firmware metadata.
- `backend/internal/protocol/`: Companion device/backend wire contracts.
- `backend/internal/capability/`: ToolRegistry/ResourceRegistry policy and integration boundaries.
- `components/companion_app/`: hardware-independent firmware state and behavior.
- `components/esp32_board/`: ESP32-S3 physical adapters.
- `main/`: physical-device composition root.
- `host/`: software-device and deterministic firmware-core testing.

A future split must preserve these boundaries or replace them through an explicit reviewed decision.

## Sources of truth

PostgreSQL/pgx is the sole authoritative product store and Atlas owns its schema.

- Financial, schedule, conversation, memory metadata, identity, device/control, privacy and similar application records remain Companion-owned state.
- The LLM is a reasoning/composition component, not a database or authorization source.
- Vector/embedding memory is a secondary projection; it cannot authorize or replace current financial, schedule, identity or configuration state.
- Device UI/caches may present or temporarily retain state but do not become backend authority.
- SQLite is retained only for explicit migration/recovery tooling and isolated adapter tests. No product runtime SQLite selector, shadow read, dual write or fallback is allowed.
- River is the implemented durable-job layer after the PostgreSQL hard cut. It is no longer a roadmap candidate.
- Redis/external vector infrastructure remains optional and requires measured operational need before adoption.

## Data, control and event boundaries

The realtime data plane carries latency-sensitive audio, protocol and presentation/capability traffic over Companion Protocol v2/WebSocket. MQTT/UDP/WebRTC are not parallel product transports merely because other projects/providers support them.

The control plane owns enrolled device identity, desired/reported twin state, scoped configuration, feature metadata, privacy policy, entitlement and signed firmware metadata.

The event plane uses the transactional outbox when a state mutation and emitted durable event must commit atomically. Consumers are idempotent because delivery may be at least once. The product does not use full event sourcing.

River owns implemented durable maintenance/retention job execution. A domain operation requiring atomic state + job scheduling must preserve the existing transaction/idempotency boundaries rather than introduce a second in-memory scheduler as authority.

## Device twin, configuration and feature state

Desired and reported state remain separate and versioned. Devices apply compatible desired state and report applied/rejected versions; stale desired/reported messages must not move state backwards.

Remote configuration, feature rollout, entitlement, privacy and authorization remain separate decisions:

- feature/config cannot grant ownership or permission;
- entitlement is not authentication;
- privacy policy may deny a technically available feature;
- firmware/device compatibility may reject a desired config/firmware target.

`FeatureModule` is descriptive metadata for capabilities/tools/resources/UI/config/locales. It must not dynamically load arbitrary executable code.

## Capabilities and integrations

ToolRegistry/policy is the model-facing schema/authorization/execution boundary.

- Durable product/business mutations execute through backend-owned typed tools/repositories.
- Authenticated device capabilities execute over Companion Protocol v2 and are scoped to the authenticated current device/session. The model cannot supply an arbitrary device identity as authorization.
- The current proven software-device example is `device.volume.set`; additional physical controls are added only when product hardware/need is concrete and bounded.
- Optional external integrations use backend-side MCP behind explicit configuration, authentication, schema validation, egress policy, timeout/cancel and auditing.
- The official MCP Go SDK interoperability path is implemented. Firmware does not run MCP and never connects directly to an LLM or external MCP endpoint.
- Arbitrary GPIO, shell, filesystem, credential, firmware-flash and raw-secret access are not generic model capabilities.

## Model, speech and memory adapters

LLM/model transport, ASR, TTS, native-realtime audio, embedding/retrieval and external integrations remain replaceable at Companion-owned boundaries.

Selection requires representative Companion evaluation rather than generic leaderboard claims. Important dimensions include:

- Vietnamese/English task and tool correctness;
- false mutation/schema failure rate;
- first-token/partial/final/first-audio/end-to-end latency;
- cancellation and stale-output behavior;
- reliability, provider failures and rate limits;
- privacy/data egress and retention terms;
- runtime resource use and cost.

Reference adapters/harnesses can be implemented without promoting a Production-v1 provider/model. Measured selection evidence is tracked separately.

Memory remains distinct from authoritative domain state, records provenance/validity where applicable, and supports correction/deletion semantics.

## Voice and media lifecycle

Voice media uses a replaceable blob-storage boundary.

Current voice-mail implementation:

- PostgreSQL owns mailbox metadata/lifecycle/ownership/idempotency;
- the current blob adapter stores **local-filesystem Ogg Opus** media;
- authenticated retrieval validates bounded media metadata/content before playback;
- voice mail never auto-plays;
- privacy lifecycle supports disabled/ephemeral/retained behavior.

An S3-compatible object-store adapter is a future deployment/scaling option, not the current implementation. Any replacement must preserve ownership, checksum/content validation, deletion/retention semantics, backup/restore boundaries and rollback.

"Deleted" distinguishes immediate access revocation from asynchronous object/version/backup retention cleanup.

## Pairing and relationships

Device relationships are backend-authoritative and many-to-many. Proximity is an input, not authorization.

On ESP32-S3, BLE RSSI is only coarse evidence. A secure pairing product flow requires authenticated devices/owners, a short-lived one-time session, bilateral confirmation, expiry, replay/rate-limit protection and durable idempotency. RSSI algorithm/threshold selection requires measured false-accept/false-reject qualification on real hardware/enclosure.

Without a stronger physical signal, describe the feature as **proximity-confirmed pairing**, not secure ranging or a guaranteed physical bump.

## Firmware, audio and presentation

Application state remains in `components/companion_app/`; peripheral/vendor details stay behind firmware board/audio/network/presentation interfaces.

The ESP-SR AFE/WakeNet/VAD/AEC software path is implemented behind a portable audio-front-end boundary. Physical wake/AEC/self-trigger/false-interruption/resource quality remains a Tier-3 acoustic qualification problem.

The current product display remains the existing SSD1306 path until hardware selection and physical benchmark evidence promote a replacement. The reversible color-display candidate uses Espressif `esp_lcd` + `esp_lvgl_port`/LVGL 9 as the baseline; alternatives are benchmark challengers, not simultaneous production stacks.

Frame time, heap/PSRAM, binary size, audio/network coexistence, bus behavior and power are measured budgets. A hard-coded 60 FPS requirement is not an architecture contract.

Typed remote presentation may select allow-listed local semantic states; arbitrary executable UI/animation code is not downloaded into the device runtime.

## Connectivity and OTA boundaries

Wi-Fi + secure WSS remains the current product connectivity path. A future cellular network adapter may share the same IP/WSS product protocol only if a portable product requirement justifies it; it must not create a second MQTT/UDP business protocol.

Signed firmware manifests/control-plane metadata are implemented. Device-side A/B download/apply/health-confirm/rollback remains a separate firmware lifecycle until implemented and physically qualified. Irreversible anti-rollback/eFuse changes require a tested manufacturing and recovery procedure plus explicit approval.

## Security and privacy

- Production transport verifies TLS and uses unique revocable enrolled device credentials.
- Enrolled backend identity, not client-supplied user/tenant/plan headers, controls authorization context.
- ToolRegistry policy, privacy, entitlement and feature/config checks remain explicit boundaries.
- High-impact mutations use scoped confirmation where required; model text alone is not authorization.
- Secrets are not committed or included in normal logs/evidence.
- Raw private audio is not persisted by default outside explicit voice-media/product policy.
- External providers receive only data needed by the selected feature/mode.
- Secure Boot, flash encryption, anti-rollback and eFuse provisioning require a tested recovery/manufacturing procedure before irreversible activation.

## Validation and evidence

Use the evidence ladder, not a blanket "no mocks" or "run everything" rule:

1. deterministic unit/host/contract tests;
2. real implemented database/protocol/integration boundaries;
3. software-device Tier 1 for real Companion orchestration without physical claims;
4. simulation only where it represents the behavior;
5. trusted physical HIL for RF/audio/display/power/input/OTA/peripheral claims;
6. real-provider benchmarks for provider/model claims.

Mocks/fakes are valid at replaceable ports for deterministic logic and failure injection. They never promote provider or physical quality.

Because the repository is public, personal HIL executes only on manually authorized trusted refs. Pull-request code does not automatically execute on the personal self-hosted runner.

## Evolution decisions

A candidate becomes architecture only when the problem is real and the issue/ADR records relevant constraints, measured evidence or a bounded spike, security/privacy impact, migration/rollback and operational ownership.

"Latest" is not a design criterion. Avoid framework, database, transport, cache or infrastructure additions until measured need exceeds their coordination/operational cost.

# Commercial architecture

This document records the current architecture, stable boundaries, and intended
evolution seams for AI Companion. It is not a release checklist and does not turn a
roadmap candidate into an implemented dependency.

## 1. Status language

Architecture and issue documents use these terms consistently:

- **Implemented:** code is present on the referenced branch.
- **Tested:** named checks passed in a stated environment.
- **HIL-tested:** a named physical scenario passed on identified hardware.
- **Production-proven:** defined release and operational criteria passed.
- **Planned:** accepted direction without an implementation claim.
- **Candidate:** an option that still requires a benchmark or decision record.

## 2. Current system shape

The current product is a modular Go monolith plus ESP32-S3 firmware.

```text
ESP32-S3
  -> WebSocket data plane
  -> Go backend
       -> agent/model and speech adapters
       -> typed product commands and domain ports
       -> SQLite authoritative state
       -> transactional outbox
       -> control-plane services
  -> audio, button, and display hardware adapters
```

Current repository seams:

- `backend/internal/domain/`: domain types and ports.
- `backend/internal/store/`: SQLite persistence and outbox implementation.
- `backend/internal/controlplane/`: device identity/twins, config, feature
  metadata, privacy, OTA metadata, and related control-plane behavior.
- `backend/internal/protocol/`: device/backend protocol.
- `components/companion_app/`: hardware-independent firmware state and behavior.
- `components/esp32_board/`: ESP32-S3 peripheral and transport adapters.
- `main/`: composition root for the physical device.
- `host/`: host simulator and deterministic firmware-core tests.

Future process splits must preserve these boundaries or introduce an explicit,
reviewed replacement.

## 3. Sources of truth

Authoritative product state lives in the implemented database adapter.

- Expenses, budgets, reminders, notes, voice metadata, identity, device state,
  permissions, package/OTA state, and similar records are database state.
- The LLM is a reasoning and composition component, not a database.
- Vector or graph memory is a secondary projection and cannot authorize or replace
  current financial, schedule, identity, configuration, or device state.
- Device UI and local caches present or temporarily retain state; they do not become
  the backend source of truth.

SQLite is the current POC store. Postgres, Ent, Atlas, River, Redis, vector stores,
and other production replacements remain candidates or roadmap items until their
adapters, migrations, tests, and rollback paths are implemented.

## 4. Data, control, and event boundaries

The data plane carries latency-sensitive device interaction such as audio, protocol
messages, and UI events. WebSocket is implemented. WebRTC or provider-native
realtime transports are optional candidates and require benchmarked adapters.

The control plane owns identity, device twins, remote configuration, feature
metadata, privacy policy, entitlement, rollout state, and OTA metadata.

The event plane uses durable outbox records when state and an emitted event must be
atomic. Consumers must be idempotent because delivery can be at least once. The
project does not use full event sourcing.

## 5. Device twin and configuration

Desired and reported device state are separate. Updates use monotonic versions;
stale desired updates or stale reports do not advance state. The device retains
last-known-good configuration and reports whether a version was applied or rejected.

Remote configuration, feature rollout, entitlement, and authorization are separate
decisions. A feature flag cannot grant permission, and a config value cannot prove
ownership.

## 6. Features and integrations

`FeatureModule` is descriptive metadata for capabilities, tools, resources, UI
cards, config keys, locales, and implementation adapters. It does not dynamically
load arbitrary executable code into the Go process.

Internal product actions call typed commands and ports directly. They do not require
an MCP round trip. Optional external integrations may use MCP or another adapter
behind backend authentication, authorization, schema validation, egress policy,
timeouts, and auditing. Firmware never connects directly to an LLM, semantic router,
or MCP server.

## 7. Model, speech, and memory adapters

Model, ASR, TTS, and realtime voice providers remain replaceable adapters.
Selection requires representative Vietnamese and product-task evaluation, including
quality, tool accuracy, latency, cost, cancellation, privacy, and failure behavior.

Context assembly may combine current session events, relevant recent turns,
authoritative domain resources, selected long-term memories, and the current
request. Only the minimum provider context required by the selected mode should
leave the backend.

Memory is opt-in where required, records provenance, supports correction and
deletion, and remains separate from authoritative domain state.

## 8. Voice and media lifecycle

Voice media uses a blob-storage boundary. The POC may use local files; a production
adapter may use S3-compatible object storage. Database records contain metadata,
ownership, checksum, codec, size, duration, lifecycle state, object key, timestamps,
and idempotency data rather than making large audio blobs the default relational
payload.

Ogg Opus is the preferred interoperable storage format unless a measured device or
provider constraint requires another format.

Voice-mail policy is explicit:

- `disabled`: creating or receiving voice mail is rejected.
- `ephemeral`: one successful playback consumes the item and schedules deletion.
- `retained`: the item remains until its configured expiry or user deletion.

"Deleted" must distinguish immediate access revocation from asynchronous object,
version, backup, and retention cleanup. State changes that also notify devices use
the outbox where atomicity is required.

## 9. Pairing and relationships

Device relationships are many-to-many and backend-authorized. A proximity signal
alone never authorizes a relationship.

On ESP32-S3, BLE RSSI is only a coarse proximity input. A secure flow uses an
authenticated owner/device, one-time session, nonce, expiry, replay protection,
rate limits, idempotency, and explicit confirmation by both participants. Filtering
and thresholds must be calibrated and benchmarked against false accept/reject
scenarios.

A product claim of physical "bump" requires an additional physical signal such as a
tap/impact sensor, NFC, UWB, or other measured hardware. Without that signal, call
the feature proximity-confirmed pairing.

## 10. Firmware and UI

Application state stays in `components/companion_app/`; peripheral details stay
behind board interfaces. Network, audio, display, input, BLE, LED, and haptic work
must not block one another's real-time paths.

For a color-display upgrade, the default candidate is Espressif `esp_lcd` plus
`esp_lvgl_port` and an appropriate BSP. LovyanGFX remains a benchmark alternative
for procedural sprite workloads. The chosen stack must be justified on target
hardware with audio and networking active.

Frame rate, latency, heap/PSRAM, binary size, bus use, and power are measured
budgets. A fixed 60 FPS claim is not an architecture requirement.

Hardware selection precedes hardware-specific implementation. The decision compares
retrofitting the current board with an integrated board on pin budget, display bus,
audio coexistence, power, enclosure, availability, cost, and recovery/debug access.

## 11. Security and privacy

- Production transport verifies TLS and uses unique revocable device credentials.
- Ownership and authorization are enforced at product-command and data boundaries,
  not only in prompts or UI.
- Secrets are not stored in source, firmware assets, logs, or issue bodies.
- High-impact mutations require an explicit confirmation scope tied to actor,
  operation, arguments, and expiry where appropriate.
- Audio, transcripts, memory, and conversation retention are independently
  configurable and support export/delete behavior.
- Secure Boot, flash encryption, anti-rollback, and eFuse provisioning require a
  tested manufacturing and recovery procedure before irreversible activation.
- External providers receive only data needed for the selected feature and mode.

## 12. Validation and release evidence

Use layered validation:

- static/build/schema checks;
- deterministic unit and host tests;
- implemented database, blob, protocol, and provider integration tests;
- simulation where it represents the relevant peripheral behavior;
- physical HIL for RF, timing, audio, display, power, input, OTA, and peripherals;
- release soak, fault, backup/restore, security, and rollback tests where applicable.

Mocks and fakes are expected at replaceable ports. They do not prove physical or
provider behavior. HIL does not replace deterministic lower-level tests.

Because this repository is public, physical HIL runs only for manually authorized
trusted refs. Pull-request code does not automatically execute on a personal
self-hosted runner.

## 13. Evolution decisions

A roadmap item becomes a fixed architecture decision only when an issue or ADR
records:

- the problem and current limitation;
- evaluated options and primary-source constraints;
- representative benchmark or compatibility evidence;
- migration and rollback plan;
- security/privacy impact;
- operational ownership.

Choose maintained technology that fits the workload. "Latest" is not a design
criterion, and version numbers are pinned only after dependency and compatibility
validation.

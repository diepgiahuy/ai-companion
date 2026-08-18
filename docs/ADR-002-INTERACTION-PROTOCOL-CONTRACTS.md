# ADR-002: Canonical interaction protocol v2

Status: **Accepted, amended by ADR-003 for device-local capabilities**

## Authority

This ADR is authoritative for the Protocol-v2 envelope, session/media boundary, and non-capability interaction message families.

[`ADR-003-DEVICE-CAPABILITY-PLANE.md`](ADR-003-DEVICE-CAPABILITY-PLANE.md) is the **sole source of truth for device capability architecture and capability hardening**. Do not duplicate a second capability design here.

## Context

Gestures, voice mail, pairing, session/turn events, presentation, and device-local actions need one authenticated wire envelope. There are no Product-v1 clients that justify a permanent v1 compatibility path.

## Decision

The system uses one canonical authenticated WebSocket protocol:

- `protocol.Envelope` with `version: 2` for JSON controls;
- binary Opus media on the same authenticated device connection.

There is no flat-message v1 path, dual-read/dual-write transport, or permanent protocol fallback. An unsupported client fails before interaction state mutation.

An Envelope v2 contains:

| Field | Requirement |
| --- | --- |
| `version` | Required integer; exactly `2`. |
| `type` | Required member of the canonical taxonomy. |
| `message_id` | Required opaque transport/session message identity. |
| `correlation_id` | Optional request/result correlation identity. |
| `session_id` | Required after `session.ready`; omitted by initial hello. |
| `turn_id` | Required for turn-scoped controls. |
| `generation_id` | Monotonic turn generation where stale-output invalidation is required. |
| `idempotency_key` | Required for retryable state-changing interactions whose domain contract needs durable mutation identity. |
| `occurred_at` | RFC3339 timestamp where required by the interaction contract. |
| `payload` | Typed bounded JSON object. |

Root and typed payload schemas fail closed. Unknown fields, invalid direction, invalid identity/generation, and malformed payloads are rejected before mutation where the specific contract requires strict decoding.

## Replay and idempotency

`message_id` and `idempotency_key` have different responsibilities.

- `message_id` is transport/session identity. The live WebSocket session can use a bounded replay cache to suppress an equivalent duplicate and reject conflicting reuse while the record remains cached.
- `idempotency_key` is durable domain mutation identity. A stateful mutation that can be retried after reconnect/restart must persist and validate the key at its authoritative domain boundary.

The protocol does **not** claim exactly-once delivery. Session replay suppression is bounded. Durable at-least-once correctness belongs to the authoritative feature/domain implementation.

## Media lane

Binary Opus frames are media, not JSON capability/tool traffic. They never carry interaction metadata, authorization claims, provider credentials, or storage credentials.

Session/turn/generation state determines whether media belongs to the current turn.

## Canonical control taxonomy

### Session, turn, model/output, and presentation

| Type | Direction | Purpose |
| --- | --- | --- |
| `session.hello` | device -> backend | Establish device transport/uplink audio parameters. |
| `session.ready` | backend -> device | Confirm authenticated session/downlink parameters and bootstrap state. |
| `session.ping` / `session.pong` | either | Liveness/correlation. |
| `turn.listen` | device -> backend | Start/stop a listening turn with bounded mode. |
| `turn.abort` | device -> backend | Cancel the current turn with bounded reason. |
| `turn.state` | backend -> device | Current turn lifecycle/interruption state. |
| `transcript.final` | backend -> device | Final bounded transcript text. |
| `tts.lifecycle` | backend -> device | TTS start/sentence/stop lifecycle. |
| `agent.status` | backend -> device | Bounded agent/runtime status. |
| `ui.card` / `ui.state` | backend -> device | Presentation semantics. |
| `alarm.fired` / `alarm.ack` | backend -> device / device -> backend | Alarm event and acknowledgement. |
| `schedule.updated` | backend -> device | Schedule/reminder presentation event. |
| `protocol.error` | either | Stable error code plus bounded message. |

### Device capabilities

Protocol v2 carries the `capability.*` family selected by ADR-003.

This ADR does not restate the capability contract, policy model, current implementation gaps, Xiaozhi reference analysis, or hardening sequence. Read ADR-003 for those details.

The old `config.update` / `config.report` message types are no longer part of the active Product-v1 taxonomy. PR #242 / issue #197 completed the settings cutover to `device.settings_v1` over the canonical capability plane.

### Gesture

`gesture.notification` is a bounded best-effort interaction. The backend authenticates and authorizes the relationship. A gesture, device ID, or proximity observation is never an authorization grant.

### Voice mail

Canonical voice-mail controls remain:

- `voice_mail.available`
- `voice_mail.claim`
- `voice_mail.claimed`
- `voice_mail.playback_result`
- `voice_mail.consumed`
- `voice_mail.expired`

Voice mail never auto-plays. The backend owns authorization, durable lifecycle/media access, and cleanup. The device requires explicit local playback intent. `media_ref` is opaque and is not a storage credential or bearer URL.

### Proximity-confirmed pairing

Canonical pairing controls remain:

- `pairing.session_create`
- `pairing.session_created`
- `pairing.confirmation`
- `pairing.succeeded`
- `pairing.rejected`
- `pairing.expired`

The backend authenticates each participant, validates one-time session/nonce/expiry state, applies rate limits, and creates the relationship atomically. Proximity evidence alone never creates or authorizes a relationship.

## Ownership rules

- One authenticated device transport/session path owns WebSocket control/media framing.
- Network callbacks do not directly render UI, decode audio, execute arbitrary tools, or destroy shared realtime resources.
- Session/turn generation prevents stale audio/control from resurrecting into a later turn.
- Device capability details and policy are owned by ADR-003.
- Durable product effects are authoritative in domain repositories, not in the LLM or transient WebSocket state.

## Consequences

Protocol v2 remains the single Companion device envelope/session/media protocol.

ADR-003 provides the one capability answer. Do not add capability semantics here that can drift from it.

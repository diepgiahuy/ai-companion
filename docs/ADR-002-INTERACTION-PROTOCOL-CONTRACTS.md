# ADR-002: Canonical interaction protocol v2

Status: **Accepted, amended by ADR-003 for device-local capabilities**

## Context

Gestures, voice mail, proximity-confirmed pairing, session/turn events, presentation and device-local actions need one authenticated wire envelope. There are no production v1 clients that justify a permanent v1 compatibility path.

This ADR defines the Protocol-v2 envelope/session/media contract and product interaction message families. **ADR-003** is authoritative for the device-local capability plane selected later by #225.

## Decision

The system uses one canonical authenticated WebSocket protocol: `protocol.Envelope` with `version: 2` for JSON controls, plus binary Opus media on the same authenticated device connection.

There is no flat-message v1 path, dual-read/dual-write transport, or permanent protocol fallback. A v1/unsupported client fails before interaction state mutation.

An Envelope v2 contains:

| Field | Requirement |
| --- | --- |
| `version` | Required integer; exactly `2`. |
| `type` | Required member of the canonical taxonomy. |
| `message_id` | Required opaque transport/session message identity, bounded by the relevant contract. |
| `correlation_id` | Optional request/result correlation identity. |
| `session_id` | Required after `session.ready`; omitted by initial hello. |
| `turn_id` | Required for turn-scoped controls. |
| `generation_id` | Monotonic turn generation where stale-output invalidation is required. |
| `idempotency_key` | Required for retryable state-changing interaction commands whose domain contract needs durable mutation identity. |
| `occurred_at` | RFC3339 timestamp where required by the interaction contract. |
| `payload` | Typed bounded JSON object. |

Root and typed payload schemas fail closed. Unknown fields, invalid direction, invalid identity/generation and malformed payloads are rejected before state mutation where the specific contract requires strict decoding.

## Replay and idempotency

`message_id` and `idempotency_key` have different responsibilities:

- `message_id` is transport/session identity. The live WebSocket session may use a bounded replay cache to suppress/replay an equivalent duplicate and reject conflicting reuse while the record remains cached.
- `idempotency_key` is durable domain mutation identity. A stateful mutation that may be retried after reconnect/restart must persist and validate the key at its authoritative domain boundary.

The protocol therefore does **not** claim exactly-once delivery. Session replay suppression is a bounded optimization; durable at-least-once correctness belongs to the authoritative feature/domain implementation.

## Media lane

Binary Opus frames are media, not JSON capability/tool traffic. They never carry interaction metadata, authorization claims, provider credentials or storage credentials. Session/turn/generation control determines whether media belongs to the current turn.

## Canonical control taxonomy

### Session, turn, model/output and presentation

| Type | Direction | Purpose |
| --- | --- | --- |
| `session.hello` | device -> backend | Establish device transport/uplink audio parameters. |
| `session.ready` | backend -> device | Confirm authenticated session/downlink parameters and allowed bootstrap state. |
| `session.ping` / `session.pong` | either | Liveness/correlation. |
| `turn.listen` | device -> backend | Start/stop a listening turn with bounded mode. |
| `turn.abort` | device -> backend | Cancel current turn with bounded reason. |
| `turn.state` | backend -> device | Current turn lifecycle/interruption state. |
| `transcript.final` | backend -> device | Final bounded transcript text. |
| `tts.lifecycle` | backend -> device | TTS start/sentence/stop lifecycle. |
| `agent.status` | backend -> device | Bounded agent/runtime status. |
| `ui.card` / `ui.state` | backend -> device | Presentation semantics; renderer behavior is owned by the presentation contract, not raw network callbacks. |
| `alarm.fired` / `alarm.ack` | backend -> device / device -> backend | Alarm event and acknowledgement. |
| `schedule.updated` | backend -> device | Schedule/reminder presentation event. |
| `protocol.error` | either | Stable error code plus bounded message. |

### Device capability plane — amended by ADR-003 / #225

Device-local action discovery/invocation uses **Typed Companion Capability RPC** carried by Protocol v2:

| Type | Direction | Purpose |
| --- | --- | --- |
| `capability.advertise` | device -> backend | Exact supported capability name/version pairs. |
| `capability.call` | backend -> device | Correlated bounded invocation of an advertised capability. |
| `capability.result` | device -> backend | Correlated typed success/error result. |
| `capability.cancel` | backend -> device | Cancel a current correlated capability operation when supported. |

This is Companion Capability RPC, **not MCP**. MCP remains an optional backend external-integration boundary. Firmware never connects directly to MCP servers, models or providers.

Physical firmware currently advertises only device capabilities it can execute truthfully, including `device.user_confirmation` v1. Unsupported capability names/versions fail closed. Destructive confirmation still requires a fresh local physical press after the prompt; capability traffic does not grant authorization by itself.

### Legacy configuration transition

`config.update` / `config.report` still exist in current code for historical settings delivery, but they are **legacy transitional controls, not the target device capability architecture**.

Issue #197 owns preserving useful desired/reported semantics (requested/applied/rejected/stale/offline/unknown and monotonic ordering) while rebasing settings onto the #225/ADR-003 capability/state architecture. No new Product-v1 feature should bind to `config.update` / `config.report` as a permanent control path. Once the replacement is proven, the legacy configuration transport is deleted instead of kept as a rollback selector.

## Interaction contracts

### Gesture

`gesture.notification` is a bounded best-effort interaction. The backend authenticates/authorizes the relationship; a gesture, device ID or proximity observation is never an authorization grant.

### Voice mail

Canonical voice-mail controls remain:

- `voice_mail.available`
- `voice_mail.claim`
- `voice_mail.claimed`
- `voice_mail.playback_result`
- `voice_mail.consumed`
- `voice_mail.expired`

Voice mail never auto-plays. The backend owns authorization, durable lifecycle/media access and cleanup; the device requires explicit local playback intent. `media_ref` is opaque and never a storage credential/bearer URL. Ephemeral/retained semantics and durable mutation idempotency remain owned by the voice-mail domain implementation.

### Proximity-confirmed pairing

Canonical pairing controls remain:

- `pairing.session_create`
- `pairing.session_created`
- `pairing.confirmation`
- `pairing.succeeded`
- `pairing.rejected`
- `pairing.expired`

The backend authenticates each participant, validates one-time session/nonce/expiry state, applies rate limits and creates the relationship atomically. Proximity evidence alone never creates or authorizes a relationship.

## Ownership rules

- One authenticated device transport/session path owns WebSocket control/media framing.
- Network callbacks do not directly render UI, decode audio, execute arbitrary tools or destroy shared realtime resources.
- Session/turn generation prevents stale audio/control from resurrecting into a later turn.
- Device capability handlers are typed/allow-listed and cannot override backend authorization identity.
- ToolRegistry/backend policy remains separate from device capability execution.
- Durable product effects are authoritative in domain repositories, not in the LLM or transient WebSocket state.

## Consequences

Protocol v2 remains the single Companion device envelope/session/media protocol. ADR-003 narrows one question that this earlier ADR did not settle cleanly: device-local capability actions use **Typed Companion Capability RPC** rather than MCP or ad-hoc feature-specific permanent transports.

The architecture is intentionally truthful about migration state: legacy settings controls still exist until #197 proves and cuts over their replacement. That temporary implementation fact does not make them the canonical target architecture.

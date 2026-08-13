# ADR-002: Canonical interaction protocol v2

Status: **Accepted**

## Context

Gestures, voice mail, and proximity-confirmed pairing need a shared wire contract.
This POC has no production clients, so retaining a flat v1 path would add obsolete
code and an unnecessary compatibility test matrix.

This ADR defines the protocol contract only. It does not add persistence, blob
storage, BLE behavior, display/audio UX, delivery state, or authorization rules.

## Decision

The system uses one canonical WebSocket protocol: `protocol.Envelope` with
`version: 2`. All JSON control messages, including gesture, voice-mail, pairing,
session, UI, and configuration messages, use this envelope. There is no
`InteractionEnvelope`, flat-message path, dual-read, dual-write, feature-gated
emission, or v1 compatibility behavior.

An Envelope v2 contains the message type, `message_id`, optional correlation and
session fields, `idempotency_key`, RFC3339 `occurred_at`, and a typed JSON-object
payload. Interaction messages require non-empty `message_id`, `idempotency_key`,
and `occurred_at`.

`message_id` and `idempotency_key` have intentionally different responsibilities:

- `message_id` is transport/session identity. The live WebSocket session keeps a
  bounded replay cache so an equivalent duplicate can reuse the recorded outcome
  and conflicting reuse of the same ID is rejected while that ID remains cached.
- `idempotency_key` is domain mutation identity. Any interaction that can be retried
  after reconnect or process restart must persist this key at the authoritative
  domain boundary, scoped by authenticated actor, operation/type, and canonical
  request content. The persistence layer replays the original committed outcome for
  an equivalent retry and rejects the same key with different canonical content.

**Current implementation boundary in PR #14:** only the first bullet exists. The
`message_id` replay cache is scoped to one live WebSocket session and is lost on
reconnect. PR #14 does **not** implement an actor-scoped or durable
`idempotency_key` store, and therefore does not prove reconnect/restart payload-
conflict safety. The actor-scoped rule in the second bullet is a requirement for
stateful domain handlers, not an implementation claim of this protocol migration.
The README gate for idempotency payload-conflict safety must remain `unproven` until
a domain implementation persists and tests that contract.

The protocol layer therefore does **not** claim exactly-once delivery. WebSocket
replay suppression is a bounded optimization; durable at-least-once correctness is
owned by the stateful feature implementation (#5 for voice mail and #7 for pairing).

Envelope decoding uses these stable errors from `envelope.go`:

| Condition | Code |
| --- | --- |
| Version other than 2, including v1 | `unsupported_protocol_version` |
| Unknown or non-interaction message type | `unknown_message_type` |
| Malformed envelope, missing interaction metadata, or invalid payload | `invalid_envelope` |

A v1 client fails before any interaction state change. If the connection can send a
response, it uses the v2 `protocol.error` control message with
`unsupported_protocol_version`; otherwise the server closes the session. There is
no fallback or retry using v1.

The media lane is separate from JSON control but remains within protocol v2: raw
Opus binary WebSocket frames carry audio, while JSON control uses `Envelope v2`.
Binary frames never carry interaction metadata, authorization claims, or storage
credentials.

### Envelope fields

| Field | Requirement |
| --- | --- |
| `version` | Required integer; exactly `2`. |
| `type` | Required member of the canonical taxonomy below. |
| `message_id` | Required opaque transport/session message ID, 1-128 bytes. |
| `correlation_id` | Optional request/event correlation ID. |
| `session_id` | Required after `session.ready`; omitted by the initial hello. |
| `turn_id` | Required for turn-scoped controls. |
| `generation_id` | Optional monotonic turn generation used to discard stale media/control. |
| `idempotency_key` | Required for state-changing interaction commands; durable semantics are implemented by the authoritative domain store. |
| `occurred_at` | Required RFC3339 timestamp for interaction events. |
| `payload` | Required typed JSON object, at most 4 KiB; the envelope is at most 8 KiB. |

Root and payload fields are strict. Unknown fields, invalid direction, and malformed
payloads fail before state mutation.

### Session replay cache

The current WebSocket implementation remembers at most 256 inbound `message_id`
records per live session. FIFO eviction is intentional and is **not** a durable or
actor-scoped idempotency guarantee: after eviction or reconnect, the same transport
message may reach domain handling again. Stateful feature handlers must therefore
rely on the persisted `idempotency_key` contract, not on the session cache, for
correctness.

The cache may replay a deterministic/terminal outcome for an equivalent duplicate.
A retryable infrastructure failure must not be treated as proof that the domain
mutation committed; feature implementations must define retryable versus terminal
outcomes together with durable idempotency records.

### Core control taxonomy

| Type | Direction | Payload |
| --- | --- | --- |
| `session.hello` | device -> backend | transport and uplink audio parameters |
| `session.ready` | backend -> device | transport, downlink audio parameters, and an optional resolved config snapshot; if config is present it must be complete and valid |
| `session.ping` / `session.pong` | either | empty object; pong correlates to ping |
| `turn.listen` | device -> backend | `state` (`start`/`stop`) and start `mode` |
| `turn.abort` | device -> backend | bounded `reason` |
| `turn.state` | backend -> device | state and optional interruption reason |
| `transcript.final` | backend -> device | final `text` |
| `tts.lifecycle` | backend -> device | start/sentence/stop state and sentence text where required |
| `agent.status` | backend -> device | bounded status state |
| `ui.card` / `ui.state` | backend -> device | typed UI card or emotion/tool state |
| `alarm.fired` / `alarm.ack` | backend -> device / device -> backend | alarm identity, text/time, or acknowledgement identity |
| `schedule.updated` | backend -> device | display message and fire time |
| `config.update` / `config.report` | backend -> device / device -> backend | monotonic version, resolved device snapshot, and apply outcome |
| `protocol.error` | either | stable error `code` and bounded message |

## Interaction contracts

All payloads are strictly decoded as typed JSON objects. Unknown fields are
rejected; a schema change is a new v2 message type or a coordinated breaking change.
Delivery may be at least once. WebSocket ordering only applies within one connection;
server resource state and time are authoritative after reconnect. Stateful feature
handlers deduplicate durable mutations with `idempotency_key` before commit.

### Gesture

| Type | Payload | Rules |
| --- | --- | --- |
| `gesture.notification` | `gesture`, `sender_device_id` | Both identifiers are required. Delivery is best effort; a recipient may use message/idempotency identity to suppress duplicate visual or haptic UX. |

The backend authorizes the sender/recipient relationship. A device ID, gesture, or
proximity observation is not an authorization grant.

### Voice mail

| Type | Payload | Rules |
| --- | --- | --- |
| `voice_mail.available` | `voice_mail_id`, `from_device_id`, `media_format`, `duration_ms`, `size_bytes`, `checksum_sha256`, `expires_at`, `policy` | Metadata only. Format is `ogg_opus`; checksum is a SHA-256 hex digest; policy is `ephemeral` or `retained`. |
| `voice_mail.claim` | `voice_mail_id`, `playback_id` | Explicit user-initiated request for a short playback lease. |
| `voice_mail.claimed` | `voice_mail_id`, `playback_id`, `media_ref`, `lease_expires_at` | `media_ref` is an opaque backend reference, never a bearer token, URL, or storage credential. |
| `voice_mail.playback_result` | `voice_mail_id`, `playback_id`, `result`, optional `failure_code` | `result` is `succeeded` or `failed`. |
| `voice_mail.consumed` | `voice_mail_id`, optional `playback_id` | Media is no longer available to the recipient. |
| `voice_mail.expired` | `voice_mail_id` | Media access is revoked. |

Voice audio never auto-plays. The backend owns authorization, persistence, cleanup,
and access checks; the device owns an explicit playback action and UI.

```text
available --claim--> claimed
available --consume--> consumed
available --expire--> expired
claimed --playback succeeded / ephemeral--> consumed
claimed --playback succeeded / retained--> available
claimed --playback failed or lease expired--> available
claimed --consume--> consumed
claimed --expire--> expired
consumed, expired --> terminal
```

### Proximity-confirmed pairing

| Type | Payload | Rules |
| --- | --- | --- |
| `pairing.session_create` | `initiator`, `candidate_device_id`, `proximity_evidence_id` | References an opaque local observation; it does not submit RSSI as authorization input. |
| `pairing.session_created` | `session_id`, `initiator`, `peer`, `expires_at` | Server-created one-time session for distinct participants. |
| `pairing.confirmation` | `session_id`, `participant`, `confirmation_nonce`, `confirmed_at` | Explicit confirmation by one participant; nonce is never logged as a credential. |
| `pairing.succeeded` | `session_id`, `relationship_id`, `initiator`, `peer` | Server-authorized relationship after two valid confirmations. |
| `pairing.rejected` | `session_id`, `reason` | Reason is an enumerated rejection outcome. |
| `pairing.expired` | `session_id` | The server-enforced confirmation window has elapsed. |

The backend authenticates the actor, verifies control of each participant, applies
rate limits, validates nonce/session expiry, and creates a relationship atomically.
Proximity evidence alone never creates a relationship.

```text
awaiting_confirmation --initiator confirms--> initiator_confirmed
awaiting_confirmation --peer confirms--> peer_confirmed
initiator_confirmed --peer confirms--> succeeded
peer_confirmed --initiator confirms--> succeeded
awaiting_confirmation, initiator_confirmed, peer_confirmed --reject--> rejected
awaiting_confirmation, initiator_confirmed, peer_confirmed --expire--> expired
succeeded, rejected, expired --> terminal
```

## Consequences

Backend, firmware, host simulator, and fixtures move together to Envelope v2. The
state helpers reject duplicate and terminal transitions. Live-session replay
suppression remains bounded and session-local; durable feature handlers must
implement the actor-scoped `idempotency_key` contract before any reconnect-safe
mutation is considered proven. Rollback is a Git revert of the coordinated change,
not a protocol fallback.

Issue #5 owns voice-mail persistence, durable mutation idempotency, and media access.
Issue #6 owns device notification and deliberate playback UX. Issue #7 owns pairing
persistence, durable mutation idempotency, proximity observation, nonce verification,
rate limits, and two-device HIL.

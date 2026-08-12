# ADR-002: Interaction protocol contracts for gestures, voice mail, and pairing

Status: **Accepted**

## Context

The WebSocket protocol currently uses the version-1 `protocol.Message` envelope for
assistant audio, UI, and device configuration. It has no shared contract for remote
gestures, voice-mail delivery, or proximity-confirmed pairing. This ADR establishes
the additive contract that issues #5, #6, and #7 must implement against.

This is intentionally a contract-only decision. It adds no database schema, blob
adapter, BLE behavior, display/audio UX, or device-twin delivery state.

## Decision

New interaction traffic uses `protocol.InteractionEnvelope`, alongside the existing
`protocol.Message`. It is encoded as JSON over the current authenticated WebSocket
path and has its own fixed `version: 1`. Existing message names, fields, and
`ValidateHello` behavior are unchanged.

`FeatureModule` may advertise support and a feature flag may control emission, but
neither is authoritative delivery, consent, relationship, mailbox, or pairing state.
The backend authenticates the session and authorizes all user/device relationships;
a device must never infer authorization from a message, device ID, or proximity
observation.

### Envelope

| Field | Required | Limit / behavior |
| --- | --- | --- |
| `type` | yes | One of the documented interaction types below. Unknown types fail closed. |
| `version` | yes | Exactly `1`. Unsupported versions fail closed. |
| `id` | yes | Opaque event/request ID, 1–128 bytes. |
| `idempotency_key` | yes | Opaque key, 1–128 bytes, scoped by authenticated actor and type. |
| `occurred_at` | yes | RFC 3339 timestamp. Server receipt time is authoritative for expiry and ordering. |
| `payload` | yes | Typed JSON object, maximum 4 KiB; whole envelope maximum 8 KiB. |

A receiver persists (or otherwise deduplicates) a successful `idempotency_key` for
at least the lifetime of the affected resource/session, and returns the original
outcome when it receives the same key and equivalent command again. Reuse of a key
with a different actor, type, target, or normalized payload is rejected as a
conflict. State helpers deliberately reject a repeated transition; deduplication
happens before the mutation.

WebSocket preserves order on one connection only. There is no global ordering across
reconnects or devices. Consumers compare server-issued resource versions/timestamps
when an implementation needs ordering. Delivery is at least once, so messages can be
duplicated and reconnecting clients may receive a current snapshot/event again.

Unknown optional JSON fields are ignored to allow additive optional fields. A
required-field or semantic change requires a new interaction version. Unknown types
and versions are errors, not silently treated as a gesture, voice item, or pairing
approval.

## Message contract

### Gesture

| Type | Payload | Rules |
| --- | --- | --- |
| `gesture.notification` | `gesture`, `sender_device_id` | Both required (1–64 and 1–128 bytes). Delivery is best-effort; the recipient uses the envelope idempotency key to suppress duplicate notification UX. No durable acknowledgement is introduced by this contract. |

The sender and recipient relationship is authorized by the server before emission.
Gesture values remain product/UX vocabulary rather than hardware commands.

### Voice mail

| Type | Payload | Rules |
| --- | --- | --- |
| `voice_mail.available` | `voice_mail_id`, `from_device_id`, `media_format`, `duration_ms`, `size_bytes`, `checksum_sha256`, `expires_at`, `policy` | Metadata only. Format is `ogg_opus`; duration is 1–600,000 ms; size is 1–33,554,432 bytes; checksum is a 64-character SHA-256 hex digest. Policy is `ephemeral` or `retained`. |
| `voice_mail.claim` | `voice_mail_id`, `playback_id` | Device requests a short server-assigned playback lease. |
| `voice_mail.claimed` | `voice_mail_id`, `playback_id`, `media_ref`, `lease_expires_at` | `media_ref` is an opaque backend reference (1–256 bytes), not a bearer token, object URL, storage credential, or direct blob endpoint. The future blob adapter resolves it only through the authenticated backend. |
| `voice_mail.playback_result` | `voice_mail_id`, `playback_id`, `result`, optional `failure_code` | `result` is `succeeded` or `failed`. A failure code is forbidden on success and limited to 64 bytes. |
| `voice_mail.consumed` | `voice_mail_id`, optional `playback_id` | Indicates that media is no longer available to this recipient. |
| `voice_mail.expired` | `voice_mail_id` | Indicates expiry; media access must be revoked. |

Audio never auto-plays: a device must explicitly send `voice_mail.claim` only after
its own user-initiated playback action. The delivery lifecycle is separate from
media storage; issue #5 owns persistence, object cleanup, access checks, and
transactional outbox emission, while issue #6 owns notification/playback UX.

Voice state:

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

A failed claim does not change state. A lease expiry or failed playback makes the
same item available again when it has not expired. Retained playback is reported but
does not consume the item; an explicit consume/delete operation does. Expiry wins
over an uncommitted lease according to server time. A reconnect repeats the last
authoritative available/claimed/terminal notification with the same resource
identity and a new envelope ID when necessary.

`disabled` voice policy is enforced by the server before creation/delivery and
therefore has no valid available state. Its rejection uses the existing error
message path until a future typed error envelope is introduced.

### Proximity-confirmed pairing

| Type | Payload | Rules |
| --- | --- | --- |
| `pairing.session_create` | `initiator`, `candidate_device_id`, `proximity_evidence_id` | Device reports an opaque local observation. Raw RSSI/threshold is deliberately not a protocol authorization input. |
| `pairing.session_created` | `session_id`, `initiator`, `peer`, `expires_at` | Server-created one-time session for two distinct participants. |
| `pairing.confirmation` | `session_id`, `participant`, `confirmation_nonce`, `confirmed_at` | Explicit confirmation by exactly one participant. Nonce is 16–256 bytes and is validated server-side; it is never logged as a credential. |
| `pairing.succeeded` | `session_id`, `relationship_id`, `initiator`, `peer` | Server-authorized relationship creation after two valid confirmations. |
| `pairing.rejected` | `session_id`, `reason` | Reason is `user_declined`, `authorization_denied`, `invalid_nonce`, or `rate_limited`. |
| `pairing.expired` | `session_id` | Session has passed its server-enforced expiry. |

A participant consists of opaque `owner_user_id` and `device_id` identifiers, each
1–128 bytes. These identify the bilateral consent record; they are neither
credentials nor permission grants. The server confirms that the authenticated actor
controls the participant it claims, resolves the candidate, applies rate limits,
checks session uniqueness/expiry, and creates the many-to-many relationship
atomically. RSSI or any other proximity evidence alone never creates a relationship.

Pairing state:

```text
awaiting_confirmation --initiator confirms--> initiator_confirmed
awaiting_confirmation --peer confirms--> peer_confirmed
initiator_confirmed --peer confirms--> succeeded
peer_confirmed --initiator confirms--> succeeded

awaiting_confirmation, initiator_confirmed, peer_confirmed
  --reject--> rejected
  --expire--> expired

succeeded, rejected, expired --> terminal
```

A repeated confirmation from the same role is a duplicate and cannot advance state.
The backend uses the idempotency key to return the original confirmation outcome;
a different replay or nonce is rejected. Reconnect does not reopen a terminal
session. The client must display server time/expiry behavior as authoritative.

## Validation and compatibility

`backend/internal/protocol/interaction.go` validates every required field and
size limit before serialization and after decoding. It exposes sentinel errors for
unknown types and unsupported versions. The existing `Message` remains the
compatibility path and was deliberately not modified; a legacy `ui_state` JSON
round-trip is covered by test.

The interaction implementation is safe to roll back by disabling its feature flags
and ceasing new-message emission. Existing `Message` traffic and device-twin
configuration are unaffected. Already-created future resources must be cleaned up
by their owning implementation issue, not by this contract.

## Consequences and follow-up ownership

- Issue #5 implements voice-mail authorization, persistence, media references,
  expiry/deletion, outbox events, and idempotency storage.
- Issue #6 implements device notification, deliberate playback controls, and
  acknowledgement UX; it must not auto-play `voice_mail.available`.
- Issue #7 implements BLE/proximity observations, pairing session persistence,
  nonce verification, rate limits, relationship authorization, and two-device HIL.
- No physical HIL is required for this ADR or its pure protocol/state helpers.
  Pairing RF behavior, media playback, and display/audio behavior remain hardware
  gates for their owning issues.

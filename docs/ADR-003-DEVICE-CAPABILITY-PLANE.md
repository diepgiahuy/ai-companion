# ADR-003: Typed Companion device capability plane

Status: **Accepted**  
Decision issue: **#225**  
Amends: **ADR-002** for device-local capability/control actions

## Context

Product-v1 needs a small device action boundary for operations such as owner-approved destructive confirmation, future device-local settings/actions, and capability discovery. The device already has an authenticated Companion WebSocket/session/media transport. Reusing external MCP terminology for a custom stateful ESP32 control protocol would make lifecycle, authorization and compatibility ambiguous.

The decision must preserve these boundaries:

- Companion owns device identity, credentials, session/media transport and authorization;
- the Go backend owns model/tool policy and owner/device isolation;
- firmware never receives arbitrary remote-code execution, unrestricted Internet access, provider credentials or direct model/MCP authority;
- media/audio remains a separate lane from capability RPC;
- privileged/destructive actions are explicit, typed and policy-gated;
- there is one canonical Product-v1 device capability path, not parallel permanent transports.

## Decision

Select **Typed Companion Capability RPC** over the authenticated Protocol-v2 device connection.

The canonical capability messages are:

| Type | Direction | Purpose |
| --- | --- | --- |
| `capability.advertise` | device -> backend | Advertise the exact typed capability names/versions actually supported by the connected firmware. |
| `capability.call` | backend -> device | Invoke one advertised bounded device-local capability with correlation, turn/generation context where relevant, arguments and deadline semantics. |
| `capability.result` | device -> backend | Return a correlated typed success/error result. |
| `capability.cancel` | backend -> device | Cancel a current correlated capability operation when that operation supports cancellation. |

Capability names are versioned and allow-listed. Unsupported names/versions fail closed. Payloads remain bounded Protocol-v2 JSON objects and inherit authenticated session/device identity; a payload cannot override authorization identity.

Current implemented physical-device capability:

- `device.user_confirmation` v1 — backend-triggered destructive-action confirmation. The firmware shows the bounded prompt through the presentation boundary and approval still requires a fresh physical press after the prompt. Capability traffic uses the single WebSocket control/reassembly owner.

A capability must not be advertised until the exact firmware can produce a truthful local effect or result. For example, `device.volume.set` may exist in backend/software-device contract tests, but physical firmware must not advertise it until a real bounded local volume effect exists.

## MCP boundary

MCP remains an **optional backend external-integration boundary** behind Companion policy, authorization and egress controls. Firmware does not connect directly to MCP servers and Companion device capability RPC is not called MCP.

This keeps external tool interoperability separate from the stateful embedded device/session/media contract.

## Relationship to Protocol v2

Protocol v2 remains the authenticated transport/envelope/session/media contract. `capability.*` is the canonical device-action capability family carried by that transport.

Existing Protocol-v2 controls that are intrinsically session/media/product events remain Protocol-v2 controls (for example session/turn/TTS/alarm/pairing/voice-mail/presentation events). They are not mechanically converted into capabilities merely to reduce the number of message types.

### Legacy configuration transition

`config.update` / `config.report` still exist in the current codebase for historical desired/reported settings delivery. They are **transitional legacy controls, not the target capability architecture**.

Issue **#197** owns preservation of the useful requested/applied/rejected/stale/offline desired/reported semantics while rebasing settings onto the selected #225 capability/state architecture. No new Product-v1 feature should bind itself to `config.update` / `config.report` as the permanent device-control boundary. After #197 proves the replacement path, the obsolete configuration transport must be deleted rather than retained as a permanent fallback.

## Security and lifecycle rules

- advertise only exact supported name/version pairs;
- validate bounded arguments with strict schemas and reject unknown fields where the capability contract requires it;
- correlate call/result/cancel and reject wrong session/turn/generation context;
- destructive capability approval is never inferred from proximity, model output, previous button state or presentation text;
- reconnect invalidates session-scoped pending capability state and requires fresh advertisement;
- cancellation/timeouts cannot resurrect a stale operation;
- capability handlers enqueue/coordinate through owned runtime boundaries; network callbacks do not directly render, decode audio or destroy shared realtime resources;
- device capabilities do not grant backend/tool authorization and backend authorization does not imply local physical confirmation.

## Rejected alternative

**MCP semantics as the device-facing canonical protocol** are rejected for Product-v1.

Useful MCP ideas and external MCP integrations may be reused at the backend boundary, but the ESP32 device control plane remains deliberately named and specified as Companion Capability RPC. No hidden MCP fallback or second permanent device capability transport is retained.

## Verification / evidence

The selected boundary is verified with the smallest relevant oracles:

- protocol/golden tests for message taxonomy and bounded envelopes;
- host tests for capability dispatch/version/unsupported behavior;
- logical software-device capability scenarios where a local effect can be modeled truthfully;
- exact-head ESP32-S3 firmware compile for physical firmware wiring;
- Tier-1 when a change crosses backend and device boundaries;
- physical HIL only for properties that require a real device/effect.

## Consequences

The repository has one explicit answer to “how does the backend request a device-local action?”: **Typed Companion Capability RPC over Protocol v2**.

MCP stays backend-only. Settings migration remains truthful and incomplete until #197 lands; this ADR records the target architecture without claiming that legacy `config.update` / `config.report` have already been removed.

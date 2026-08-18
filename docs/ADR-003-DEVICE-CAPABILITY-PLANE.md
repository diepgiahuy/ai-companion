# ADR-003: Typed Companion device capability plane

Status: **Accepted**  
Decision issue: **#225**  
Implementation owner: **#246 / PR #247**  
Baseline entering capability hardening: **`main@bc47f855e5d69d02ecfc0f8286821b196ad35894`**  
Last verified: **2026-08-18**

## Authority

This file is the **sole Product-v1 architecture source of truth for device capabilities**.

Other documents must point here instead of defining a second capability architecture. GitHub issues and PRs own live work state. Merged `main` owns implementation truth.

A later review must separate:

1. **Locked decisions** — change only through new evidence and an explicit architecture decision.
2. **Implementation facts** — update when merged code changes.
3. **Future work** — must not be presented as current behavior.

---

## Locked decisions

### One device capability boundary

Companion uses **Typed Companion Capability RPC** over the authenticated Protocol-v2 WebSocket connection.

The message family is:

| Type | Direction | Purpose |
| --- | --- | --- |
| `capability.advertise` | device -> backend | Advertise exact capability identities supported by this authenticated session. |
| `capability.call` | backend -> device | Invoke one accepted and advertised bounded device-local capability. |
| `capability.result` | device -> backend | Return the correlated success or error result. |
| `capability.cancel` | backend -> device | Cancel a correlated operation only when its contract supports cancellation. |

Protocol v2 remains the transport, envelope, session, turn, generation, and media contract. Binary Opus media remains separate from capability JSON.

There is no second Product-v1 device capability protocol.

### Backend authority

A device advertisement reports support. It does not grant authority.

The backend owns:

- authenticated user/device/session identity;
- accepted capability contracts;
- exact version compatibility;
- model-facing descriptions and visibility;
- ToolRegistry validation, policy, authorization, observability, and execution;
- entitlement and feature policy;
- trusted input and successful-result schemas;
- deadlines and other server limits.

A payload or model argument cannot override authenticated device identity.

The device does not define model instructions, risk, owner authorization, or model policy.

### Exact identity and Product-v1 kind

The device advertises only:

```text
name
version
kind
```

Product-v1 accepts only:

```text
kind = command
```

`kind: "read"` fails closed at the descriptor boundary. A future read contract requires a new reviewed architecture/contract decision with concrete state authority and tests.

The backend uses exact opaque `name@version` matching.

An incompatible contract change gets a new exact version. Product-v1 does not use semver ranges, `>=1`, `1.x`, schema negotiation, or `schema_hash`.

The device does not send model descriptions or JSON Schema as authority.

### ToolRegistry remains the model-tool authority

The existing backend `ToolRegistry` remains the single server-owned model-tool definition, validation, authorization, observability, and execution boundary.

Device discovery does not create a second model-tool execution path.

For a model-visible device capability:

- `ContractCatalog` owns the accepted device RPC contract;
- the ToolRegistry definition derives from that contract instead of duplicating the input schema;
- `DeviceToolset` controls per-invocation model exposure;
- execution still goes through `ToolRegistry.Execute()`.

**Visibility is not authorization.** Execution rechecks context availability, arguments, and policy.

### MCP boundary

MCP remains an optional **backend external-integration boundary**.

Firmware does not become an MCP client or server. Companion Capability RPC is not MCP.

### Desired/reported state

`device.settings_v1@1` carries device settings apply/report traffic over the capability plane.

PostgreSQL remains authoritative for desired/reported state, revision ordering, and reconciliation.

A sent command is not proof that a setting was applied.

---

## Implemented Product-v1 architecture

```text
                         BACKEND

      ContractCatalog                 ToolRegistry
   accepted device RPC contract   validation/policy/execution
             │                           │
             │                    static non-device tools
             │                           │
             ├──────────────┐            │
             │              │            │
             ▼              ▼            ▼
   SessionCapabilitySet  DeviceToolset  Google ADK
             │              │            ▲
             │              └────────────┘
             │        current-device exposure
             ▼
       devicecap.Router
             │
      capability.call
             │
═════════════╪════════════════════════════════
             │
           DEVICE
             │
             ▼
     CapabilityRegistry
             │
       exact call lookup
             │
       typed local parser
             │
       owned execution
             │
      capability.result
```

### Backend `ContractCatalog`

`backend/internal/devicecap/contracts.go` owns one accepted catalog for the current device contracts.

Generic runtime metadata is intentionally small:

```text
name
version
kind
scope               session | turn
cancelable          true | false
input schema
result schema
audience            model | internal | policy
optional ToolDefinition
```

The current contracts are:

| Capability | Audience | Scope | Cancelable | Effect/retry rule |
| --- | --- | --- | --- | --- |
| `device.volume.set@1` | model | turn | no | Absolute bounded set. Repeating the same absolute value is logically idempotent. Physical effect is not claimed until physical firmware implements and advertises it. |
| `device.user_confirmation@1` | policy | turn | yes | Fresh local intent is required for each correlated request. Old approval is never replay authority. |
| `device.settings_v1@1` | internal | session | no | The same desired revision may be reasserted on reconnect. Exact duplicate apply converges idempotently. PostgreSQL desired/reported state remains authoritative. |

Do not add generic effect/retry fields until generic runtime behavior needs them. Current effect semantics remain explicit in capability code and tests.

### Input and result validation

The capability plane reuses the existing bounded backend schema validator.

Do not add a second JSON Schema engine for device capabilities.

Flow:

```text
model/internal caller
  -> ContractCatalog exact lookup
  -> trusted input validation
  -> ToolRegistry policy when model-callable
  -> capability.call
  -> device
  -> capability.result
  -> trusted successful-result validation
  -> caller/model
```

A successful device result that violates the accepted result schema is a local contract violation. It is not exposed as success and is not relabeled as a fabricated remote-device error.

The model-visible volume ToolRegistry definition uses the same input schema owned by its device contract.

### Session capability set

`capability.advertise` accepts 1..32 descriptors and rejects duplicate `name@version` entries.

A valid fresh advertisement atomically replaces the prior accepted capability set for that authenticated session.

An invalid advertisement fails before replacement. It does not partially merge into or erase the prior valid set.

Unknown name/version/kind fails closed.

Session teardown:

- marks capability state closed;
- clears the accepted advertisement set;
- clears pending calls;
- closes pending waiters;
- unregisters the device endpoint.

Reconnect therefore requires a fresh advertisement.

### Call scope, result correlation, and cancellation

`Contract.scope` owns generic call scope.

A session-scoped call must not carry a turn ID.

A turn-scoped call must match the exact active `turn_id` and current generation before the backend sends a frame.

A result must match the pending:

```text
correlation_id
turn_id
generation_id
```

A stale or mismatched result cannot satisfy another pending operation.

`Contract.cancelable` decides whether backend timeout/context cancellation sends `capability.cancel`.

The cancel wire message does not contain a capability name. Generic cancellation resolves the active pending operation by correlation and then verifies turn/generation before invoking that operation's cancel path.

```text
capability.call
  -> pending operation keyed by correlation_id

capability.cancel
  -> find pending correlation_id
  -> verify exact turn/generation
  -> cancel only when current + cancelable
```

### Firmware `CapabilityRegistry`

Physical firmware uses a bounded, no-heap capability registry.

The registry:

- rejects duplicate `name@version` entries;
- generates advertisement from enabled definitions;
- performs exact call lookup;
- keeps small typed per-capability parsers;
- returns unsupported for unknown name/version;
- does not interpret device-side JSON Schema;
- keeps shared runtime/hardware ownership outside network parsing.

The current physical firmware advertises:

- `device.settings_v1@1`;
- `device.user_confirmation@1` when confirmation is enabled.

It does **not** advertise `device.volume.set@1`.

Therefore physical volume effect remains unproven.

### ADK `DeviceToolset`

Google ADK Go `v2.2.0` provides dynamic toolsets through:

```go
type Toolset interface {
    Name() string
    Tools(ctx agent.ReadonlyContext) ([]Tool, error)
}
```

Static ADK tool construction excludes `Pack: "device"`.

For each invocation, `registryDeviceToolset` exposes only device tools whose ToolRegistry definition is available for the trusted current invocation context.

The current model-visible projection is effectively:

```text
authenticated TurnContext.DeviceID
∩ current router/session support
∩ known ContractCatalog contract
∩ registered device ToolRegistry definition
∩ context-availability guard
```

The returned adapter calls the same `ToolRegistry.Execute()` path. ToolRegistry rechecks availability, argument schema, policy, and authorization at execution time.

A device-pack tool without an explicit `ContextAvailability` guard fails closed for both exposure and execution.

`device.settings_v1@1` stays model-hidden.

`device.user_confirmation@1` stays model-hidden and policy-owned.

`device.volume.set@1` is not a static ADK tool. It appears only for a current device that supports the accepted contract.

### Software-device Tier-1 oracle

The production C++ software device is a Tier-1 protocol/runtime oracle. It advertises `device.volume.set@1` and `device.settings_v1@1` for cross-boundary testing.

For turn-scoped capability results, it echoes the exact incoming turn and generation. This proves:

```text
model
 -> DeviceToolset
 -> ToolRegistry
 -> devicecap.Router
 -> authenticated session
 -> capability.call
 -> software device
 -> capability.result
 -> result validation
 -> model/runtime
```

This is software evidence. It is not physical volume evidence.

---

## Settings and wake configuration

Settings reconciliation intentionally reasserts every non-zero desired revision on each new authenticated session.

An exact duplicate revision is idempotent on the device.

Current settings RPC accepts only fields that the current firmware can safely apply through the selected runtime owners.

`wake_model` is intentionally excluded from the current `device.settings_v1@1` contract.

Issue **#198** owns:

- exact packaged/supported wake choices;
- safe model/threshold reconfiguration through the selected Audio owner;
- deterministic wake-disabled -> PTT fallback;
- previous-good or disabled fallback on failure;
- truthful applied/rejected reporting;
- stale/duplicate revision handling;
- exact firmware resource/provenance evidence.

Issue **#17** remains the physical acoustic evidence owner.

---

## Deferred features

### Read capabilities

Product-v1 rejects `kind: "read"`.

Do not add read execution semantics until a concrete capability requires them.

A future read contract must define:

- authoritative state source;
- mutation prohibition;
- scope;
- result schema;
- exact version compatibility;
- failure semantics;
- tests.

### Manifest paging

Do not add paging now.

Companion advertises small identity descriptors and already has bounded Protocol-v2 envelopes.

Add a versioned paging design only after a measured payload problem exists.

---

## Xiaozhi reference

`78/xiaozhi-esp32` is a reference project, not a compatibility target.

The useful patterns retained from its MCP implementation are:

- one local registry;
- duplicate-name rejection;
- typed local properties;
- generic lookup;
- separate privileged/user-only concepts;
- bounded discovery;
- argument type/range checks;
- scheduled/owned execution outside protocol parsing when required.

Do not copy:

- MCP/JSON-RPC as Companion's device wire protocol;
- device-authored model authority;
- unrestricted device descriptions/schemas;
- broad shell/filesystem/GPIO surfaces;
- heap-owned registry design solely because the reference uses it;
- its result envelope as Companion's result contract.

The retained principle is:

```text
registry -> typed local contract -> bounded discovery -> generic lookup -> owned execution
```

Reference review used upstream `78/xiaozhi-esp32` `main@8e2899dbc9249d9961b6dafc0c59f7bd7e72644d` from 2026-08-15.

---

## Required invariants

- one Product-v1 device capability transport;
- one authenticated identity source;
- one accepted backend device-contract catalog;
- one ToolRegistry model-tool policy/execution path;
- one current capability set per authenticated session;
- Product-v1 capability kind is command-only;
- exact name/version contract matching;
- no raw device metadata becomes model authority;
- no model-visible device tool without current-device support;
- no model visibility is treated as authorization;
- no turn-scoped call can target a non-current turn;
- no stale result crosses session/turn/generation boundaries;
- no generic cancel is routed by a guessed capability name;
- no network callback directly owns unrelated realtime resources;
- desired/reported settings semantics remain unchanged;
- MCP remains backend-only.

## Non-goals

This architecture does not:

- reopen the MCP decision;
- add a second protocol;
- add remote code execution;
- add arbitrary third-party firmware capabilities;
- add `schema_hash`;
- add semver negotiation;
- add device-authored model schemas/descriptions;
- add a second tool policy/execution registry;
- implement `read` without a real capability and architecture change;
- add manifest paging without a measured payload problem;
- change desired/reported state authority;
- claim physical effect from software-device evidence.

## Verification references

Backend:

- `backend/internal/protocol/capability.go`
- `backend/internal/devicecap/contracts.go`
- `backend/internal/devicecap/router.go`
- `backend/internal/server/device_capabilities.go`
- `backend/internal/server/hub.go`
- `backend/internal/capability/tool.go`
- `backend/internal/capability/validator.go`
- `backend/internal/adkbridge/runtime_adk.go`
- `backend/internal/adkbridge/runtime_device_toolset_adk.go`
- `backend/internal/pipeline/pipeline.go`

Firmware/software device:

- `components/companion_app/include/companion/capability_dispatch.hpp`
- `components/esp32_network/src/websocket_pairing.cpp`
- `components/esp32_network/src/websocket_confirmation.cpp`
- `host/companion_software_device/websocket_backend.cpp`
- `host/tests/capability_dispatch.cpp`

Version-sensitive external reference:

- Google ADK Go `v2.2.0`: `tool.Toolset` / `agent.ReadonlyContext`.

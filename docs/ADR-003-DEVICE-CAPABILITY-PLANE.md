# ADR-003: Typed Companion device capability plane

Status: **Accepted**  
Decision issue: **#225**  
Current implementation baseline: **`main@877e7969302afcbf0e526a6002307108d75fae0b`**  
Last verified: **2026-08-18**

## Authority

This file is the **sole Product-v1 architecture source of truth for device capabilities**.

Other documents must point here instead of defining a second capability architecture.
GitHub issues and PRs own live work state. Current `main` owns implementation truth.

To prevent review drift, this ADR separates three things:

1. **Locked decisions** — change only through new evidence and an explicit architecture decision.
2. **Verified current facts** — update when `main` changes.
3. **Implementation plan** — may adapt to non-material code drift, but must preserve the locked decisions.

A later review must not turn an implementation gap into a protocol redesign unless the locked decision is shown to be wrong.

---

## Locked decisions

### One device capability boundary

Companion uses **Typed Companion Capability RPC** over the authenticated Protocol-v2 WebSocket connection.

The message family is:

| Type | Direction | Purpose |
| --- | --- | --- |
| `capability.advertise` | device -> backend | Advertise the exact capability identities supported by this authenticated session. |
| `capability.call` | backend -> device | Invoke one advertised bounded device-local capability. |
| `capability.result` | device -> backend | Return the correlated success or error result. |
| `capability.cancel` | backend -> device | Cancel a correlated operation when that contract supports cancellation. |

Protocol v2 remains the transport, envelope, session, and media contract. Binary Opus media stays separate from capability JSON.

There is no second Product-v1 device capability protocol.

### Backend authority

A device advertisement is support information. It is not authorization.

The backend owns:

- authenticated user/device/session identity;
- accepted capability contracts;
- model-facing descriptions and visibility;
- ToolRegistry policy and authorization;
- entitlement and feature policy;
- destructive-action policy;
- trusted input and result contracts;
- deadlines and other server limits.

A payload cannot override authenticated identity.

The device does not define model instructions, risk, owner authorization, or policy.

### Contract identity and versioning

The device advertises only:

```text
name
version
kind
```

The backend uses an exact `name@version` contract match.

An incompatible contract change gets a new version. Product-v1 does not use semver ranges or schema negotiation.

Do not add `schema_hash` to the wire contract.

Do not send device-authored JSON Schema or natural-language model descriptions to the backend/model.

### ToolRegistry remains the model-tool authority

The existing backend `ToolRegistry` remains the server-owned definition, argument-validation, authorization, observability, and execution boundary for model-callable tools.

Device discovery must not create a second model-tool execution path.

For a model-visible device capability:

- `ContractCatalog` owns the accepted device contract;
- the registered `ToolRegistry` definition uses that contract instead of a duplicate schema;
- `DeviceToolset` controls per-invocation model exposure;
- actual execution still goes through `ToolRegistry.Execute()` and its authorizer.

**Visibility is not authorization.** Every tool call is revalidated and reauthorized at execution time.

### MCP boundary

MCP remains an optional **backend external-integration boundary**.

Firmware does not become an MCP client or server. Companion Capability RPC is not MCP.

### Desired/reported state

`device.settings_v1@1` carries device settings apply/report traffic over the capability plane.

PostgreSQL remains authoritative for desired/reported state and version ordering.

A sent command is not proof that a setting was applied.

---

## Verified current facts

These facts were checked against `main@877e7969302afcbf0e526a6002307108d75fae0b`.

### Wire contract

`backend/internal/protocol/capability.go` currently defines:

```text
CapabilityDescriptor = name + version + kind
```

`capability.advertise` accepts 1..32 descriptors and rejects duplicate `name@version` entries.

The descriptor parser accepts `command` and `read`. Product-v1 currently activates only command contracts. `read` is therefore **reserved wire vocabulary**, not an implemented Product-v1 capability class.

`capability.call` requires object arguments and a 50..5000 ms deadline.

The result envelope has a bounded typed error vocabulary, but a successful generic `value` is currently checked only for valid JSON. Generic server-owned result-schema validation is still missing.

### Server session state

`backend/internal/server/device_capabilities.go` currently accepts only these concrete version-1 command descriptors:

- `device.volume.set@1`;
- `device.user_confirmation@1`;
- `device.settings_v1@1`.

A valid new advertisement already replaces the previous session capability map atomically under the session-state lock.

An invalid advertisement fails before that replacement. It does not partially merge into the previous set.

Session teardown already:

- marks capability state closed;
- clears advertised capabilities;
- clears pending calls;
- closes pending waiters;
- unregisters the device endpoint.

Reconnect therefore requires a fresh advertisement.

### Authenticated device scope

The device model-tool execution path already derives `DeviceID` from trusted server/session state.

`handleDevice` authenticates the device before WebSocket upgrade. Database-authenticated identity replaces client-controlled ownership headers. `processTurn` then copies `s.deviceID` into `pipeline.TurnContext`, and ADK receives that context.

Existing security tests prove forged ownership headers do not replace enrolled identity. Existing device-tool tests prove a turn for Device A does not call Device B.

Future `DeviceToolset` tests must preserve this property, but the identity origin itself is no longer an unknown architecture question.

### ToolRegistry and ADK exposure

`device.volume.set` is currently registered in the process-wide `ToolRegistry` with `Pack: "device"`.

`ToolRegistry` already provides `Definition()`, `DefinitionsForPacks()`, argument validation, authorization, and execution.

The current ADK bridge calls `buildRegistryTools(cfg.Tools)` once during runtime construction and exports all current registry definitions as static ADK tools.

This means the current model can see `device.volume.set` even when the connected physical firmware does not advertise it. Execution still fails closed through the trusted current DeviceID and `Endpoint.Supports()`, so this is a **model-truth/exposure mismatch**, not evidence of a cross-device authorization bypass.

### Physical firmware

The current physical firmware advertises:

- `device.settings_v1@1`;
- `device.user_confirmation@1` when confirmation is enabled.

It does **not** advertise `device.volume.set@1`.

Firmware capability dispatch is still name-specific. `capability.call` selects confirmation/settings branches explicitly.

`capability.cancel` has no capability name/version in its wire payload. It identifies the operation through envelope correlation/turn/generation state. Current firmware cancellation is confirmation-specific.

### Existing settings semantics

Settings reconciliation intentionally reasserts every non-zero desired revision on a new authenticated session.

An exact duplicate revision is expected to be idempotent on the device.

This is a capability-specific semantic contract. It does not require a generic distributed-idempotency framework.

---

## Target architecture

The target is **Generic Typed Companion Capability RPC**.

Generic means generic registration, contract lookup, session discovery, call dispatch, and truthful model exposure. Generic does not mean generic trust or remote code execution.

```text
                         BACKEND

      ContractCatalog                 ToolRegistry
   accepted device contract      schema/policy/execution
             │                           │
             │                    static non-device tools
             │                           │
             ├──────────────┐            │
             │              │            │
             ▼              ▼            ▼
   SessionCapabilitySet  DeviceToolset  ADK
             │              │            ▲
             │              └────────────┘
             │        dynamic device exposure
             │
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

The catalog is the accepted contract list for device RPC. It is not a second ToolRegistry.

A minimal contract needs only metadata that generic runtime code actually uses:

```text
name
version
kind
scope               session | turn
cancelable          true | false
input schema
result schema
audience            model | internal | policy
optional model ToolDefinition
```

Do not add generic fields for hypothetical semantics unless runtime code needs them.

For a model-visible contract, the optional `ToolDefinition` uses the same input contract and becomes the registered ToolRegistry definition. Do not maintain a second hand-written model schema.

Per-capability effect/retry semantics remain explicit code + tests unless a later real capability requires generic runtime behavior.

Current semantic ownership is:

| Capability | Audience | Scope | Retry/effect rule |
| --- | --- | --- | --- |
| `device.settings_v1@1` | internal | session | Same desired revision can be reasserted; PostgreSQL desired/reported state is authoritative. |
| `device.user_confirmation@1` | policy | turn | Approval is bound to fresh physical intent and current correlation/turn/generation; old approval is never replay authority. |
| `device.volume.set@1` | model | turn | Absolute bounded set. It remains model-visible only for a current device that advertises the accepted contract. Physical effect is unproven until firmware implements and advertises it. |

### Input and result validation

Reuse the existing bounded backend schema validator.

Do not add a second JSON Schema engine for device capability contracts.

Flow:

```text
model/internal caller
  -> ContractCatalog lookup
  -> trusted input validation
  -> ToolRegistry authorization when model-callable
  -> capability.call
  -> device
  -> capability.result
  -> trusted result validation
  -> caller/model
```

A successful device result that violates the accepted result schema is a local contract failure. Do not expose it as success and do not relabel it as a fabricated remote-device error.

Use deterministic shared contract vectors where backend and firmware both own the same value bounds.

### Session capability set

Keep the current single bounded advertisement.

A valid advertisement atomically replaces the prior set for that authenticated session.

Unknown name/version/kind fails closed. Preserve current transactional behavior: an invalid replacement does not partially mutate the accepted current set.

Do not add diagnostic-only acceptance of unknown capabilities unless a real compatibility requirement is approved later.

Do not add manifest paging until measured payload size requires it.

### Call scope and cancellation

Remove capability-name special cases from the generic server path.

`Contract.scope` decides whether a call requires current turn/generation state or can run at authenticated session scope.

`Contract.cancelable` decides whether backend timeout/context cancellation sends `capability.cancel`.

Do not add a capability name to `capability.cancel`.

Generic firmware cancellation resolves the active operation by `correlation_id`, then verifies the stored turn/generation before invoking that operation's cancel path.

```text
capability.call
  -> pending operation keyed by correlation_id when needed

capability.cancel
  -> find correlation_id
  -> verify turn/generation
  -> cancel only if that operation is cancelable/current
```

This preserves the current wire shape and prevents cancellation from being routed through an unrelated capability handler.

### Firmware `CapabilityRegistry`

Use a bounded embedded registry. Do not copy Xiaozhi's heap-owned `std::vector<McpTool*>` implementation.

Each compiled definition needs the data required for local dispatch, for example:

```text
name
version
kind
call handler
optional cancel handler/state
```

The registry:

- rejects duplicate `name@version` entries;
- generates the truthful advertisement from registered/enabled definitions;
- performs exact call lookup;
- keeps small typed per-capability parsers;
- returns unsupported for unknown name/version;
- does not interpret device-side JSON Schema.

Network parsing must not directly take ownership of shared realtime resources. A handler that changes shared runtime/hardware state must execute through the existing owner boundary.

### ADK `DeviceToolset`

Google ADK Go `v2.2.0` supports dynamic toolsets:

```go
type Toolset interface {
    Name() string
    Tools(ctx agent.ReadonlyContext) ([]Tool, error)
}
```

Use this seam for **model exposure**, not as a new authorization/execution registry.

The current static ADK tool build must exclude device-pack tools.

For each invocation, `DeviceToolset.Tools(ctx)` exposes only model-audience device contracts that:

```text
match authenticated TurnContext.DeviceID
∩ are advertised by that current session
∩ are known by ContractCatalog
∩ have a registered ToolRegistry definition
```

The returned ADK adapter must call the same `ToolRegistry.Execute()` path used today. ToolRegistry argument validation and authorizer remain mandatory at execution time.

`device.settings_v1@1` stays model-hidden.

`device.user_confirmation@1` stays model-hidden and policy-owned.

`device.volume.set@1` stops being a static ADK tool when `DeviceToolset` lands. Its ToolRegistry definition remains registered so schema, policy, execution, and observability still have one owner.

---

## Reserved and deferred features

### `read`

Keep `read` as reserved descriptor vocabulary.

Product-v1 has no active `read` contract today. The server catalog therefore accepts no read capability.

Do not build read execution semantics until a concrete capability requires them. A future read contract must define state authority, mutation prohibition, scope, result schema, and tests before activation.

### Manifest paging

Do not add paging now.

Companion advertises small identity descriptors and already has bounded Protocol-v2 envelopes. Xiaozhi needs paging because its discovery payload includes longer model descriptions and full input schemas.

Add a versioned paging design only after a measured Companion payload problem exists.

---

## Xiaozhi reference

`78/xiaozhi-esp32` is a reference project, not a compatibility target.

This review used upstream `main@8e2899dbc9249d9961b6dafc0c59f7bd7e72644d` from 2026-08-15.

Useful patterns in its current MCP implementation:

- one local tool registry;
- duplicate-name rejection;
- typed local properties;
- generic lookup;
- separate user-only tools;
- bounded discovery;
- argument type/range checks;
- scheduled/owned execution for operations that must leave the protocol parse path.

Do not copy:

- MCP/JSON-RPC as Companion's device wire protocol;
- device-authored model authority;
- unrestricted device-provided descriptions/schemas;
- broad shell/filesystem/GPIO surfaces;
- its exact payload threshold;
- heap ownership/storage solely because Xiaozhi uses it;
- its result envelope as Companion's result contract.

Primary references:

- <https://github.com/78/xiaozhi-esp32/blob/8e2899dbc9249d9961b6dafc0c59f7bd7e72644d/main/mcp_server.h>
- <https://github.com/78/xiaozhi-esp32/blob/8e2899dbc9249d9961b6dafc0c59f7bd7e72644d/main/mcp_server.cc>

The retained principle is:

```text
registry -> typed local contract -> bounded discovery -> generic lookup -> owned execution
```

---

## Implementation plan

Do not implement this as one large rewrite.

### Slice 1 — Backend contract foundation

Goal: create one accepted device-contract source without changing the wire protocol.

Scope:

- add `ContractCatalog` for the three current known contracts;
- add trusted input/result schemas;
- add scope/audience/cancelable metadata used by generic runtime code;
- reuse/refactor the existing bounded schema validator for successful results;
- make the model-visible volume ToolRegistry definition derive from the same contract input schema;
- add exact name/version/kind and contract-vector tests.

Keep current external behavior unless a malformed result/input already violates the accepted contract.

### Slice 2 — Generic server session activation

Goal: remove capability-name special cases from server acceptance/call scope.

Scope:

- replace `allowedDeviceCapability()` with exact catalog lookup;
- preserve atomic advertisement replacement;
- preserve invalid-advertisement no-partial-mutation behavior;
- use contract scope instead of `call.Name == device.settings_v1` special casing;
- send cancel only for cancelable contracts;
- preserve correlation, deadline, stale-generation, reconnect, and endpoint isolation behavior.

### Slice 3 — Firmware `CapabilityRegistry`

Goal: remove hard-coded call dispatch and hard-coded advertisement construction.

Scope:

- add bounded registry storage;
- register current settings and optional confirmation definitions;
- generate advertisement from enabled registry entries;
- perform exact generic call lookup;
- keep typed capability parsers;
- route cancel through current pending correlation state, not capability name;
- preserve current runtime ownership rules;
- add host tests for duplicate, unknown, version mismatch, malformed args, cancel, and reconnect/reset state.

### Slice 4 — ADK `DeviceToolset`

Goal: make model-visible device tools truthful per current authenticated device without bypassing ToolRegistry policy.

Scope:

- exclude `Pack: "device"` definitions from static ADK tools;
- add `DeviceToolset` through ADK `Toolsets`;
- use trusted `pipeline.CurrentTurn(ctx).DeviceID`;
- project current session advertisement + model-audience contract into ADK tools;
- adapt the existing ToolRegistry definition;
- execute through existing `ToolRegistry.Execute()`;
- prove Device A/B isolation and no device tool when unsupported/offline;
- prove internal/policy contracts never appear to the model.

### Slice 5 — Integrated hardening

Goal: prove the complete boundary before release qualification continues.

Required scenarios:

- valid settings reconciliation;
- duplicate settings revision retry;
- confirmation approve/reject/cancel;
- malformed input and malformed success result;
- unknown name/version/kind advertisement;
- advertisement replacement;
- disconnect/reconnect fresh advertisement;
- timeout/cancel race;
- stale/duplicate/unknown result correlation;
- Device A/B model exposure and execution isolation;
- physical firmware does not expose volume until it truthfully implements and advertises it.

Run the nearest host/Go contract tests first, then exact-head firmware compile and Tier-1 because the final slices cross backend/device boundaries.

---

## Required invariants

- one Product-v1 device capability transport;
- one authenticated identity source;
- one accepted backend device-contract catalog;
- one ToolRegistry model-tool policy/execution path;
- one current capability set per authenticated session;
- exact name/version contract matching;
- no raw device metadata becomes model authority;
- no model-visible device tool without current-device advertisement;
- no model visibility is treated as authorization;
- no network callback directly owns unrelated realtime resources;
- no stale result crosses session/turn/generation boundaries;
- desired/reported settings semantics remain unchanged;
- MCP remains backend-only.

## Non-goals

This hardening does not:

- reopen the MCP decision;
- add a second protocol;
- add remote code execution;
- add arbitrary third-party firmware capabilities;
- add `schema_hash`;
- add semver negotiation;
- add device-authored model schemas/descriptions;
- add a second tool policy/execution registry;
- implement `read` without a real capability;
- add manifest paging without a measured payload problem;
- change desired/reported state authority.

## Verification references

Companion:

- `backend/internal/protocol/capability.go`
- `backend/internal/server/device_capabilities.go`
- `backend/internal/server/hub.go`
- `backend/internal/devicecap/router.go`
- `backend/internal/capability/tool.go`
- `backend/internal/capability/validator.go`
- `backend/internal/adkbridge/runtime_adk.go`
- `backend/internal/pipeline/pipeline.go`
- `components/companion_app/include/companion/capability_dispatch.hpp`
- `components/esp32_network/src/websocket_confirmation.cpp`

External version-sensitive reference:

- Google ADK Go `v2.2.0`: <https://github.com/google/adk-go/blob/v2.2.0/tool/tool.go>

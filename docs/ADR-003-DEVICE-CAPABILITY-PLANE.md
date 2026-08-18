# ADR-003: Typed Companion device capability plane

Status: **Accepted**  
Decision issue: **#225**  
Current implementation baseline: **`main@877e7969302afcbf0e526a6002307108d75fae0b`**  
Last verified: **2026-08-18**

## Authority

This file is the **sole Product-v1 architecture source of truth for device capabilities**.

Other architecture, protocol, plan, issue, PR, and reference documents must point to this file instead of restating a separate capability design. If code and this file disagree, refresh live repository truth first and update this file in the same focused change.

This ADR defines the accepted boundary and the current hardening target. It does not make unfinished work complete.

## Decision

Companion uses **Typed Companion Capability RPC** over the authenticated Protocol-v2 WebSocket connection.

The canonical message family is:

| Type | Direction | Purpose |
| --- | --- | --- |
| `capability.advertise` | device -> backend | Replace the current authenticated session capability set with exact supported name/version/kind descriptors. |
| `capability.call` | backend -> device | Invoke one advertised bounded device-local capability. |
| `capability.result` | device -> backend | Return the correlated success or error result. |
| `capability.cancel` | backend -> device | Cancel the correlated operation when cancellation is supported. |

Protocol v2 remains the transport, envelope, session, and media contract. Binary Opus media stays separate from capability JSON.

MCP remains an optional **backend external-integration boundary**. Firmware does not connect directly to MCP servers, models, or providers. Companion Capability RPC is not MCP.

## Security ownership

A device advertisement states **what the exact connected firmware says it can execute**. It does not grant authority.

The backend owns:

- authenticated user/device/session identity;
- capability contract acceptance;
- model-facing descriptions;
- model visibility/audience;
- risk classification;
- entitlement and feature checks;
- destructive-action policy;
- input and result schema trust;
- deadlines and policy limits.

The device must not decide risk, model visibility, owner authorization, or destructive classification.

Unknown capability names or versions fail closed. Unknown capabilities remain hidden from the model.

A payload cannot override authenticated user/device identity.

## Current repository truth

The current Protocol-v2 capability descriptor contains only:

```text
name
version
kind
```

`capability.advertise` accepts 1..32 descriptors. The current wire payload does not contain a schema, schema hash, model description, risk, or audience.

The current protocol vocabulary accepts `command` and `read`, but the server accepts only version `1` command descriptors from its concrete allow-list. `CapabilityCallPayload.Validate()` also validates calls as command capabilities. Therefore `read` is **not implemented end to end**.

The current server allow-list contains:

- `device.volume.set@1`;
- `device.user_confirmation@1`;
- `device.settings_v1@1`.

The current firmware dispatch is still capability-specific. It has explicit branches for `device.user_confirmation@1` and `device.settings_v1@1` and returns unsupported for other calls.

The current capability result envelope is typed, but a successful generic `value` is only checked for valid JSON. Generic result-schema enforcement does not exist yet. Specific callers may perform their own strict decoding.

PR #242 / issue #197 removed the legacy `config.update` / `config.report` product transport. `device.settings_v1@1` now carries the desired/reported settings apply result over the canonical capability plane. The useful desired/reported semantics remain PostgreSQL-authoritative and unchanged.

Issue #228 is complete. Network/runtime ownership still follows its safety rule: protocol callbacks parse, validate, and enqueue work through owned runtime boundaries. They do not directly take ownership of unrelated realtime resources.

## Target architecture

The target is **Generic Typed Companion Capability RPC**.

Generic means generic registration, discovery, lookup, dispatch, and model exposure. Generic does **not** mean generic trust.

```text
Firmware CapabilityRegistry
            │
            ▼
  capability.advertise
            │
            ▼
SessionCapabilitySet
            │
            ├───────────────┐
            ▼               ▼
   ContractCatalog      TurnContext
            │               │
            └───────┬───────┘
                    ▼
               DeviceToolset
                    │
                    ▼
                   ADK
                    │
                    ▼
             devicecap.Router
                    │
             capability.call
                    │
                    ▼
        Firmware CapabilityRegistry
                    │
              runtime owner
                    │
                    ▼
             local handler
```

### Firmware `CapabilityRegistry`

Compiled firmware handlers register one bounded definition:

```text
name
version
kind
handler
```

The registry rejects duplicate `name@version` entries.

The transport parser performs generic exact lookup. It validates the bounded envelope and capability-specific arguments. It then enqueues execution through the correct runtime owner. It does not run hardware or shared realtime teardown work directly inside the network callback.

Do not add a general JSON-Schema interpreter to firmware for Product-v1. Capability handlers can keep small typed parsers and shared contract test vectors.

Use bounded embedded storage. Do not copy an unbounded dynamic registry solely because a reference project uses one.

### Backend `ContractCatalog`

The backend owns the accepted contract for each known `name@version`:

```text
name
version
kind
input schema
result schema
model description
audience
risk
policy limits
```

For Product-v1 first-party firmware, the device advertises contract identity. The backend defines contract semantics.

Do not transmit device-authored natural-language descriptions to the model. This prevents a compromised or incorrect firmware image from injecting model instructions through tool metadata.

Do not add `schema_hash` to the wire contract. An incompatible schema change requires a new capability version.

### Session capability set

Each authenticated session stores only the capabilities advertised by that session.

A fresh advertisement atomically replaces the previous session set. Reconnect requires fresh advertisement. Session close removes the set and invalidates pending work.

Keep the current bounded single advertisement for Product-v1. Do not add manifest paging until measured payload size requires it.

### ADK `DeviceToolset`

Device-specific model tools must not mutate the process-global `ToolRegistry`.

The current `ToolRegistry` is process-wide, and the current ADK bridge converts registry definitions to static ADK tools during runtime construction. That is correct for global product tools. It is not the correct scope for device-specific availability.

Google ADK Go `v2.2.0` provides `tool.Toolset`:

```go
type Toolset interface {
    Name() string
    Tools(ctx agent.ReadonlyContext) ([]Tool, error)
}
```

Use this supported extension seam for device-specific tools.

For each model invocation, `DeviceToolset.Tools(ctx)` computes the intersection of:

```text
authenticated TurnContext.DeviceID
∩ current SessionCapabilitySet
∩ ContractCatalog
∩ server-owned audience/risk/policy
∩ entitlement/feature state
```

Only the result is exposed to the model.

`device.settings_v1@1` remains internal and model-hidden.

`device.user_confirmation@1` remains policy-owned and model-hidden.

`device.volume.set@1` can be model-visible only when the authenticated current device advertises the accepted version and backend policy allows it.

### Schema validation

Use one backend schema validation implementation for capability input and result values where possible.

Validate model/tool input before sending `capability.call`.

Validate successful device results against the server-owned result schema before exposing the value to callers or the model.

A result-schema violation is a contract failure. Do not turn it into fabricated device success.

Use shared deterministic contract fixtures for backend and firmware host tests. Do not use a wire `schema_hash` as a substitute for versioning and tests.

## Deferred features

### `read`

Do not implement `read` only because the enum currently accepts it.

Add end-to-end `read` semantics only when a concrete Product-v1 capability requires them. Before that change, define its observational, mutation, deadline, state-authority, and test semantics.

### Manifest paging

Xiaozhi uses bounded `tools/list` pages because each discovered tool includes a description and full input schema. Companion currently advertises small identity descriptors and already has Protocol-v2 payload bounds.

Do not copy Xiaozhi's 8 KB page size or cursor protocol without measured need. If a future manifest cannot fit the Companion envelope, add a focused versioned paging design with atomic replacement semantics.

## Xiaozhi reference: patterns to keep

Current `78/xiaozhi-esp32` is a **reference project, not a compatibility target**.

Useful patterns verified in its current MCP implementation:

- one device-local tool registry;
- duplicate-name rejection;
- typed property metadata;
- generated input schema from local metadata;
- separate user-only tools;
- bounded discovery with cursor paging;
- generic tool lookup;
- argument type/range checks;
- scheduling tool execution through `Application::Schedule(...)` instead of executing the tool directly in the protocol parse path.

Primary references:

- <https://github.com/78/xiaozhi-esp32/blob/main/main/mcp_server.h>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/mcp_server.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/mcp-protocol.md>

Do **not** copy:

- MCP as Companion's device wire protocol;
- device-owned model authority;
- unrestricted device-authored model descriptions;
- broad shell/filesystem/GPIO capability surfaces;
- its exact payload-size constant;
- its return format as Companion result-schema design;
- its lifecycle as a replacement for Companion session/turn/generation safety.

The retained principle is:

```text
registry -> typed metadata -> bounded discovery -> generic lookup -> owned execution
```

Companion adds authenticated session scoping, server-owned policy, turn/generation safety, cancellation, typed result contracts, desired/reported state, and reconciliation.

## Implementation order

Implement the hardening in small reviewable slices.

1. **Backend contract catalog and result validation**
   - add server-owned input/result contracts;
   - reuse the existing bounded schema validator where suitable;
   - add shared contract fixtures;
   - keep the wire descriptor unchanged.

2. **Firmware capability registry**
   - replace the capability-specific dispatch switch with bounded registry lookup;
   - keep typed per-capability parsers;
   - enqueue handler work through the runtime owner;
   - add duplicate, unknown, cancel, and malformed-argument host tests.

3. **Session capability reconciliation**
   - replace the concrete server allow-list with `ContractCatalog` lookup;
   - keep atomic session advertisement replacement;
   - fail closed for unknown name/version/kind;
   - keep reconnect/session invalidation.

4. **ADK dynamic device toolset**
   - keep global product tools in the global `ToolRegistry`;
   - add `DeviceToolset` through ADK `Toolsets`;
   - derive device scope only from trusted turn/session context;
   - remove global registration of device-specific model tools after parity tests pass.

5. **Integrated hardening**
   - test cross-device isolation;
   - test reconnect replacement;
   - test unknown capability;
   - test input and result schema rejection;
   - test internal/policy tool hiding;
   - test cancellation, timeout, stale generation, duplicate/unknown correlation;
   - run the relevant host, Go, firmware compile, Tier-1, and exact-head CI gates.

## Required invariants

- one Product-v1 device capability transport;
- one authenticated identity source;
- one server policy authority;
- one capability contract authority;
- one current session capability set per authenticated device session;
- no raw device metadata becomes model authority;
- no device-specific capability definition leaks into another device's model view;
- no network callback directly owns unrelated realtime resources;
- no stale result crosses session/turn/generation boundaries;
- desired/reported settings semantics remain unchanged;
- MCP remains backend-only.

## Non-goals

This hardening does not reopen the MCP decision, add a second protocol, add remote code execution, add arbitrary third-party firmware capabilities, change desired/reported state semantics, implement `read` without a real use case, or add manifest paging without a measured payload problem.

## Verification references

- Companion current protocol: `backend/internal/protocol/capability.go`
- Companion current session routing: `backend/internal/server/device_capabilities.go`
- Companion current device router/model tool bridge: `backend/internal/devicecap/router.go`
- Companion current firmware dispatch: `components/companion_app/include/companion/capability_dispatch.hpp`
- Companion current ADK runtime: `backend/internal/adkbridge/runtime_adk.go`
- Google ADK Go `v2.2.0` Toolset interface: <https://github.com/google/adk-go/blob/v2.2.0/tool/tool.go>

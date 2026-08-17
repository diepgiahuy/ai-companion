# Architecture

The production-v1 codebase is a **modular Go monolith + ESP32-S3 firmware**. Product state is authoritative in domain repositories; the LLM/agent composes language and tool calls but is not a database.

## Device/runtime boundary

The current product path is intentionally singular:

```text
ESP32-S3 hardware
  -> Wi-Fi
  -> WebSocket protocol v2
     - typed JSON control envelopes
     - binary Opus media
     - Typed Companion Capability RPC for bounded device-local actions
  -> Go session/turn runtime
  -> ASR boundary
  -> Google ADK v2 agent
  -> ToolRegistry / ResourceRegistry
  -> authoritative repositories
  -> TTS boundary
  -> WebSocket v2
  -> firmware UI/audio
```

There is no product transport selector, custom Qwen runtime selector, shared device-token fallback, or product mock-agent fallback. Future replacements must prove their target gate and then replace the current path; they must not create indefinite dual product paths.

## Session and turn runtime

Each connected device has one WebSocket control/reassembly owner and one writer. Protocol v2 control messages and binary Opus media remain separate. Turns are generation-scoped so barge-in/cancellation can invalidate stale model/TTS output without leaking it into a new turn.

`message_id` replay suppression is intentionally session-local. Durable domain mutations use persisted idempotency semantics in their authoritative repository/tool implementation.

## Device identity and authentication

Database-enrolled per-device credentials are the only product device-auth mechanism. The server fails closed before WebSocket upgrade when the authenticator is unavailable or when `Device-Id`/credential validation fails. Revocation makes the previously issued credential unusable.

Enrollment-owned user/device/tenant/plan claims are trusted; client headers cannot override them. Admin credential provisioning is a control-plane operation and the raw device credential is shown once, while storage keeps its digest.

## Device capability plane

ADR-003 selects **Typed Companion Capability RPC** as the one Product-v1 boundary for bounded device-local capabilities over the authenticated Protocol-v2 connection:

- `capability.advertise` — device advertises exact supported capability name/version pairs;
- `capability.call` — backend invokes one advertised bounded capability;
- `capability.result` — device returns a correlated typed success/error result;
- `capability.cancel` — backend cancels a current correlated operation where supported.

Firmware never exposes arbitrary remote execution and never connects directly to an MCP server, model, or provider. Capability payload identity cannot override authenticated device/owner authorization. Privileged/destructive actions remain policy-gated and may additionally require local physical confirmation.

The currently implemented physical capability is `device.user_confirmation` v1. It preserves the fresh-press rule: approval requires a new physical press after the bounded prompt becomes active. A capability is not advertised by physical firmware until the exact build can perform a truthful local effect/result.

MCP remains an optional **backend external-integration** boundary behind Companion policy and egress controls; the embedded capability protocol is deliberately not called MCP.

`config.update` / `config.report` are legacy transitional settings controls. Issue #197 owns preserving desired/reported requested/applied/rejected/stale/offline semantics while rebasing settings onto the selected capability/state architecture. No new Product-v1 feature should treat the legacy config messages as the permanent device capability path; after the replacement is proven, the old path is deleted rather than kept as a fallback.

## Capability/context architecture

```mermaid
flowchart TD
    Turn["ASR transcript + user/device/thread/turn identity"] --> Conversation["Conversation service"]
    Conversation --> History["PostgreSQL conversation store"]
    Turn --> Agent["Google ADK v2"]
    History --> Agent
    Agent --> Tools["ToolRegistry - canonical schemas/auth/execution"]
    Tools --> NativeTools["Native tools"]
    Tools --> MCPTools["Optional external MCP tools"]
    NativeTools --> Resources["ResourceRegistry"]
    Agent --> Resources
    Resources --> Domain["PostgreSQL domain repositories"]
    NativeTools --> Domain
```

Rules:

- `ToolRegistry` is the one tool definition/schema/authorization/execution boundary. ADK FunctionTools are generic adapters over public registry definitions, not duplicated product implementations.
- Hidden/internal tools without public definitions are never exported to the model.
- `ResourceRegistry` routes typed resources without forcing internal product reads through MCP.
- Conversation history is bounded conversational context; expenses, budgets, timers, reminders, notes and journal stay authoritative in domain storage.
- Timers/reminders may share scheduler mechanics but retain separate domain semantics.
- External MCP remains behind backend policy/egress controls; firmware never connects directly to MCP or an LLM.

## Agent runtime and durable conversation semantics

Google ADK v2 is the sole product agent runtime. `companiond` requires explicit ADK model/base-URL configuration and fails startup when it is absent.

Companion's conversation service/store remains authoritative for durable conversational continuity. ADK's in-memory session service is an execution cache only. On first use of a recreated ADK session, the bridge rehydrates bounded recent Companion history before running the turn. User and successful assistant messages are persisted through the Companion conversation service, while durable tool effects are stored by domain repositories.

This avoids introducing a second ADK-owned product database while preserving restart/reconnect continuity.

## Persistence rule

PostgreSQL/pgx is the sole product database implementation and Atlas owns Companion schema migrations. `companiond` has no SQLite fallback, selector, shadow read or dual write. SQLite remains only in explicit cutover/recovery tooling and isolated tests. River owns a separate schema and executes durable retention cleanup through the same PostgreSQL pool; schema migration stays an explicit admin operation.

## Display migration rule

SSD1306 is the sole current product display. Issue #8 may physically prove a new board/display stack and #9 may implement it; once the replacement gate passes, remove the SSD1306 product adapter rather than retaining a permanent runtime display switch.

## Output latency rule

Tool presentations are emitted after authoritative tool execution so UI can reach the device before final model verbalization. Streaming model output is sentence-segmented for TTS and cancellation is generation-scoped.

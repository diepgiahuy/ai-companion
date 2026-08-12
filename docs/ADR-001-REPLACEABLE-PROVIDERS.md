# ADR-001: Replaceable providers, context cache and MCP-compatible capabilities

Status: **Accepted**

## Decision

Core orchestration must depend on ports/interfaces, not concrete infrastructure. SQLite, in-memory cache, Redis, Mem0, Graphiti, Qwen, Ollama, cloud LLMs, native tools and MCP tools are adapters selected at the composition root.

The architecture intentionally borrows the useful separation from `AnimeAIChat/xiaozhi-server-go` (`interfaces/providers/transport/mcp/function`) without copying its larger server framework.

## Boundaries

| Port | POC adapter | Replaceable adapters |
|---|---|---|
| LLM | OpenAI-compatible Qwen / mock | Ollama, other OpenAI-compatible/cloud/local |
| TurnResultStore | SQLite store implementation | Redis/Postgres/other idempotency store |
| ASR | mock | Whisper/local/cloud |
| TTS | mock | local/cloud Vietnamese TTS |
| ConversationStore | SQLite adapter | Postgres etc. |
| Session/Context Cache | bounded in-memory TTL cache | Redis |
| MemoryProvider | planned SQLite | Mem0, Graphiti, Qdrant |
| Domain repositories | typed `domain` ports backed by SQLite `store.Store` | Postgres/other durable stores |
| ToolRegistry | provider-neutral registry + native adapter | native + external MCP |
| ResourceRegistry | provider-neutral URI registry + native resource adapter | native URI resources + MCP resources |

## Context rules

1. Conversation history is **working context**, not authoritative domain state.
2. Hot recent conversation is read through `conversation.Service`: cache hit first, durable store on miss; successful durable writes update the bounded `user_id + thread_id` cache (write-through).
3. Expenses, budgets, reminders, timers, notes and journal are authoritative domain resources and must be queried from repositories/tools.
4. Long-term memory is a separate provider and must be user-readable/updateable/deletable.
5. The deterministic `ContextRouter` injects only relevant application-controlled resources and exposes only the matching tool packs; do not add a second planner-LLM round trip for this POC.
6. Provider choice belongs in the composition root/config, never in LLM business logic.
7. External MCP capabilities must enter the same logical capability registry as native tools/resources; core logic must not care which transport implements them.

## Current refactor checkpoint

Implemented in this checkpoint:
- `conversation.Store` + `conversation.Cache` ports with bounded memory/noop adapters.
- SQLite conversation adapter under `internal/providers/conversation`.
- provider-neutral `capability.ToolRegistry`; Qwen discovers definitions and executes through the registry instead of a hard-coded `switch`.
- native tool adapter under `internal/providers/tools`; legacy `expense.create` is callable but hidden from discovery.
- provider-neutral `capability.ResourceRegistry` routing stable URI schemes to resource providers.
- native resource adapter under `internal/providers/resources` exposing authoritative expense/budget/reminder/timer/note/journal/conversation reads.
- generic `resource.read` / `resource.list` tools remain callable for internal/MCP/debug use but are hidden from normal model discovery; native and future MCP resources still enter the same capability registry.
- scheduled-data migration adds `kind=timer|reminder`, allowing `timers://active` to remain semantically distinct from future reminders.
- typed `domain` ports cover Expense, Budget, Schedule, Note, Journal and VoiceMemo repositories; native tools/resources depend on these interfaces rather than concrete SQLite.
- `agent.Qwen` depends on `TurnResultStore`, `conversation.Service` and `ToolRegistry`; it no longer imports the SQLite store or native tool provider.
- composition root constructs conversation cache/store, domain adapter, native resources and native tools and injects the registries/router into Qwen.
- deterministic `ContextRouter` selects relevant resource URIs and tool packs before each LLM turn.
- ownership is explicit across domain repositories, and legacy single-user rows are migrated to the `default` owner.
- scheduler delivery uses `pending -> dispatching -> sent -> fired`, device ACK and bounded retry/backoff through a repository port.
- authoritative tool presentations can be emitted to the UI immediately, before final LLM verbalization/TTS completes.
- conversation history remains append-only but supports explicit thread clear; destructive context tools are only exposed on narrowly matched clear-history requests.
- turn idempotency keys include the server session nonce so firmware turn counters can safely restart after reboot/reconnect.
- backend acceptance is pinned to Go 1.25.0 in `go.mod` and the container gate.

Still intentionally not claimed:
- Redis/Mem0/Graphiti adapters.
- external MCP client/provider.

### Replaceability invariant

Adding an MCP provider, Redis cache, Postgres domain store or different model must not require edits to `agent.Qwen`'s tool loop. Provider selection belongs in `cmd/companiond` (or a future composition package), and every new adapter needs contract/integration tests before README status can become green.

## 2026-08 refactor invariants

- Identity is explicit (`user_id`, `device_id`, `thread_id`, `session_id`, `turn_id`); device identity is not the durable user identity.
- Conversation cache is write-through and replaceable; cache miss falls back to durable conversation storage.
- Native resources are application-controlled context. Generic resource tools are hidden from default model discovery.
- Tool definitions are grouped into packs and `ContextRouter` exposes only relevant packs when a domain is recognized.
- Domain CRUD stays strongly typed even though registration is dynamic. Do not replace it with one untyped generic CRUD tool.
- Scheduler delivery uses a repository port plus durable ACK/retry semantics.
- Proactive delivery and device acknowledgements are scoped by both owning user and target device.
- Domain adapters validate invariants too; JSON/tool schemas are not the only validation boundary.

- Timer lifecycle operations include pause/resume in the typed `ScheduleRepository`; paused timers preserve remaining duration and never enter the due scheduler until resumed.

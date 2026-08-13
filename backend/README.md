# Companion Go backend

Toolchain: **Go 1.26.5**. The only dependency graph is `go.mod` + `go.sum`.

## Wire/runtime contract

The server implements the canonical device protocol v2:

- JSON control uses typed `protocol.Envelope` v2 on `/v2/device`.
- v1 is rejected with `unsupported_protocol_version`; there is no flat-message compatibility parser.
- Raw Opus remains WebSocket binary media, separate from JSON control.
- Current device uplink is 16 kHz mono; server downlink is 24 kHz mono.
- One WebSocket reader and one writer serve each connected device session.
- Active turns are cancellable/generation-scoped; outbound queues are bounded.
- `message_id` replay suppression is session-local only. Durable reconnect-safe feature mutations implement persisted `idempotency_key` semantics at their authoritative domain store.

WebSocket is the only current backend/device realtime transport. A future transport change must be justified by measurements and replace the current path instead of living beside it indefinitely.

## Device authentication

Database-enrolled **per-device credentials are the sole product device-auth path**. `/v2/device` fails closed when no authenticator is configured, when `Device-Id` or the Bearer credential is absent, when the credential is wrong, or after it is revoked. There is no shared device-token fallback.

The admin credential endpoint emits a raw credential once; SQLite stores its SHA-256 digest with trusted user/device/tenant/plan ownership claims. Client transport headers cannot override those enrolled claims.

## Persistence

SQLite is the sole current authoritative database. Repository ports keep domain code independent from the concrete store, but that seam does not imply multiple stores are active.

PostgreSQL/Ent/Atlas/River remain a future migration. When implemented, the migration must prove data/job/backup/restart semantics and then hard-cut authoritative state rather than leave permanent dual-read/dual-write compatibility.

## Capability boundary

`ToolRegistry` is the canonical source for product tool definitions, JSON schemas, authorization and execution. Native tools and optional external MCP tools register through that boundary. Agent frameworks adapt to it instead of maintaining their own product semantics.

`ResourceRegistry` exposes application-controlled resources. Authoritative financial, schedule, note/journal, identity and device state stays in domain storage rather than conversation memory.

## Agent runtime

**Google ADK v2 is the sole product agent runtime.** `companiond` requires explicit `ADK_OPENAI_BASE_URL` and `ADK_MODEL`; missing agent configuration fails startup instead of selecting a legacy or mock runtime. The current OpenAI-compatible ADK adapter uses the Responses API.

The ADK bridge exposes every public `ToolRegistry.Definitions()` entry through generic FunctionTool adapters. ToolRegistry still owns JSON Schema validation, authorization, idempotency, execution and presentation; hidden/internal tools are not exported to the model.

Companion conversation storage remains the durable conversational source of truth. ADK's in-memory session service is a non-authoritative execution cache: when an ADK session is recreated after reconnect/process restart, bounded recent Companion history is rehydrated into it. Domain/tool state is always read from authoritative repositories.

Tier-1 software-device evidence runs the production `CompanionApp` and ADK Responses adapter with deterministic ASR/TTS/model fixtures, database-enrolled credentials and real SQLite mutations. Mock/fake dependencies in this harness are **orchestration-only** evidence and do not prove provider or physical-device quality.

Mocks remain valid test doubles. They are not product fallback modes.

## Reproducible environment

From the repository root:

```bash
# Canonical containerized host + Go integration/race regression
make e2e-container

# ESP32-S3 compile on the repository firmware toolchain
make esp-idf
```

Direct backend quality gates:

```bash
cd backend
go mod verify
go vet -tags "adk,mcp,nolibopusfile" ./...
go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
```

There is intentionally no checked-in dependency shim graph, secondary offline backend E2E path, custom Qwen runtime, or runtime agent selector. Git history/data restore is the rollback mechanism.

## Current product-provider limitations

Real streaming ASR and real Vietnamese/English TTS remain unproven provider work. The current deterministic ASR/TTS implementations are useful for tests/POC flow only and must not be reported as production voice evidence.

Real LLM quality is also unproven by deterministic Tier-1 fixtures. The ADK OpenAI model adapter requires an OpenAI Responses API-compatible endpoint; provider-specific transport details stay inside the adapter boundary.

## Source of truth

The root [`README.md`](../README.md) is the current human-readable architecture/status source of truth. `evidence/status.json` is the machine-verifiable production-claim backing. Historical checkpoint documents record past migration states; they are not instructions to preserve old runtime paths.

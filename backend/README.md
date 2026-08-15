# Companion Go backend

Toolchain: **Go 1.26.6**. The only dependency graph is `go.mod` + `go.sum`.

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

The admin credential endpoint emits a raw credential once; PostgreSQL stores its SHA-256 digest with trusted user/device/tenant/plan ownership claims. Client transport headers cannot override those enrolled claims.

## Persistence

PostgreSQL is the sole authoritative product database. `companiond` requires `COMPANION_DATABASE_URL`, opens a bounded pgx pool, and verifies the exact completed Atlas revision plus required outbox triggers before runtime initialization. It never creates or migrates schema.

There is no SQLite/PostgreSQL selector, fallback, shadow read, or dual write. SQLite remains only in the explicit `companion-migrate` cutover/recovery command and isolated tests. River uses the same pgx pool for durable retention jobs, validates its separately owned schema at startup, and never runs migrations or reindex DDL from the application role.

Run `companion-river-migrate up` with the migration owner after Atlas, then run `ops/postgres/configure_runtime_role.psql` to create/refresh the non-DDL application role. `companiond` rejects superusers and roles that can create databases, roles, or objects in `public`; River reindexing is disabled because the application role owns no DDL.

## Voice mail

Voice mail is separate from personal `voice_memos`. PostgreSQL stores mailbox metadata, policy, durable idempotency, leases, lifecycle and outbox state. Ogg Opus bytes are stored through the local filesystem `BlobStore` rooted at `COMPANION_VOICE_MAIL_DIR` (default `data/voice-mail`); production deployment must mount and back up this path.

Authenticated device operations use the enrolled `Device-Id` + Bearer credential on `/v1/voice-mail`. The flow is create metadata, `PUT /v1/voice-mail/{id}/media`, complete, recipient list/claim, authenticated media stream, then playback result or explicit delete. Protocol-v2 devices may send `voice_mail.claim` and `voice_mail.playback_result` over `/v2/device`; durable `idempotency_key` remains distinct from live-session `message_id` suppression. Media references never contain credentials, and voice mail never auto-plays.

## Capability boundary

`ToolRegistry` is the canonical source for product tool definitions, JSON schemas, authorization and execution. Native tools and optional external MCP tools register through that boundary. Agent frameworks adapt to it instead of maintaining their own product semantics.

`ResourceRegistry` exposes application-controlled resources. Authoritative financial, schedule, note/journal, identity and device state stays in domain storage rather than conversation memory.

## Agent runtime

**Google ADK v2 is the sole product agent runtime.** `companiond` requires explicit `ADK_OPENAI_BASE_URL` and `ADK_MODEL`; missing agent configuration fails startup instead of selecting a legacy or mock runtime. The current OpenAI-compatible ADK adapter uses the Responses API.

The ADK bridge exposes every public `ToolRegistry.Definitions()` entry through generic FunctionTool adapters. ToolRegistry still owns JSON Schema validation, authorization, idempotency, execution and presentation; hidden/internal tools are not exported to the model.

Companion conversation storage remains the durable conversational source of truth. ADK's in-memory session service is a non-authoritative execution cache: when an ADK session is recreated after reconnect/process restart, bounded recent Companion history is rehydrated into it. Domain/tool state is always read from authoritative repositories.

Tier-1 software-device evidence runs the production `CompanionApp` and ADK Responses adapter with deterministic ASR/TTS/model fixtures, database-enrolled credentials and real PostgreSQL mutations across `companiond` restarts. Mock/fake dependencies in this harness are **orchestration-only** evidence and do not prove provider or physical-device quality.

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

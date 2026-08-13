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
- `message_id` replay suppression is session-local only. Durable reconnect-safe feature mutations must implement persisted `idempotency_key` semantics at their authoritative domain store.

WebSocket is the only current backend/device realtime transport. The speculative WebRTC bridge was removed; a future transport change must be justified by measurements and replace the current path instead of living beside it indefinitely.

## Persistence

SQLite is the sole current authoritative database. Repository ports keep domain code independent from the concrete store, but that seam does not imply multiple stores are active.

PostgreSQL/Ent/Atlas/River remain a future migration. When implemented, the migration must prove data/job/backup/restart semantics and then hard-cut authoritative state rather than leave permanent dual-read/dual-write compatibility.

## Capability boundary

`ToolRegistry` is the canonical source for product tool definitions, JSON schemas, authorization and execution. Native tools and optional external MCP tools register through that boundary. Agent frameworks must adapt to it instead of maintaining their own product semantics.

`ResourceRegistry` exposes application-controlled resources. Authoritative financial, schedule, note/journal, identity and device state stays in domain storage rather than conversation memory.

## Agent runtime migration

There is one known temporary architecture violation tracked by #15: both the custom Qwen runtime and the ADK bridge still exist.

The selected final product runtime is **ADK only**, but the hard cut is blocked until ADK has:

- complete current `ToolRegistry` coverage rather than only representative tools;
- preserved validation, authorization, idempotency and presentation behavior;
- durable Companion session/conversation semantics instead of relying on the temporary in-memory ADK runner for required continuity;
- expense/budget/note/journal/reminder/timer/memory/tool-loop parity;
- cancellation, race and E2E regression.

After that gate, delete `COMPANION_AGENT_RUNTIME`, the custom Qwen runtime and the product-selectable mock-agent path in the same migration. Git history is the rollback mechanism.

Mocks remain valid **test doubles**. They are not product fallback modes and do not count as provider evidence.

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

There is intentionally no `go.offline.mod`, checked-in dependency shim graph, or secondary offline backend E2E path.

## Current product-provider limitations

Real streaming ASR and real Vietnamese/English TTS remain unproven provider work. The current deterministic ASR/TTS implementations are useful for tests/POC flow only and must not be reported as production voice evidence.

The ADK OpenAI model adapter requires an OpenAI Responses API-compatible endpoint. Provider-specific transport details stay inside the adapter boundary.

## Source of truth

The root [`README.md`](../README.md) is the current human-readable architecture/status source of truth. `evidence/status.json` is the machine-verifiable production-claim backing. Historical checkpoint documents record past migration states; they are not instructions to preserve old runtime paths.

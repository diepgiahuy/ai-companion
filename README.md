# AI Companion — Production v1

ESP32-S3 personal voice companion with a Go backend, realtime audio, durable personal tools, typed UI state, replaceable AI/provider boundaries, and evidence-driven production gates.

> **Stable checkpoint:** [`CP-SW2.3-20260812`](https://github.com/diepgiahuy/ai-companion/tree/CP-SW2.3-20260812)  
> **Active development:** protocol v2 (#14) plus single-path cleanup (#15)  
> **Updated:** 2026-08-13  
> **Truth rule:** this README describes the current architecture. `evidence/status.json` backs production claims. Test doubles prove logic only; they do not prove providers or hardware.

This repository is still under active development, so **breaking cleanup is preferred to backward-compatibility code**. Git history/tags are the rollback mechanism. Do not keep an old runtime, protocol, transport, dependency graph, or hardware implementation active merely as an in-process fallback.

## Current production evidence

| Gate | Status | Evidence / requirement |
|---|---:|---|
| Exact Go 1.26.5 + race/E2E | ✅ passed | Clean GitHub Actions + Docker production gate |
| Module reproducibility | ✅ passed | `go mod tidy` zero-diff + `go mod verify` at the verified checkpoint |
| Dependency vulnerability reachability | ✅ passed | `govulncheck` — 0 called vulnerabilities at the verified checkpoint |
| CodeQL | ✅ passed | GitHub CodeQL traced Go build on verified PR head |
| Mic raw signal | ✅ passed | Real ESP32-S3 + INMP441 peak/RMS responds to sound |
| Canonical protocol v2 | 🟡 implemented | Backend/firmware/host use Envelope v2 on `/v2/device`; final PR regression is still required |
| Physical ESP32-S3 HIL | ⚪ unproven | Requires a maintainer-authorized run on the dedicated board runner |
| Real ASR → LLM → TTS voice E2E | ⚪ unproven | Requires real providers and real device/network run |
| Real LLM tool quality | ⚪ unproven | Requires real-model task-success/tool-argument benchmark |
| Prompt regression quality | ⚪ unproven | Requires versioned real-model eval/red-team suite |
| Destructive confirmation UX | ⚪ unproven | Scoped authorization core exists; durable confirmation service/UX still needs proof |
| Idempotency payload-conflict safety | ⚪ unproven | Live-session `message_id` replay suppression is not durable actor-scoped idempotency |
| PostgreSQL + Atlas/Ent + River | ⚪ unproven | Migration/job/backup/restart gates pending; SQLite remains the sole current store |
| External MCP interoperability | ⚪ unproven | SDK bridge compiles; real external MCP contract test pending |
| Security default-deny | ⚪ unproven | Requires deployment-default and adversarial integration evidence |
| Privacy explicit consent | ⚪ unproven | Retention/memory consent and deletion need end-to-end proof |
| 24h real-device soak | ⚪ unproven | Physical soak/fault evidence pending |

The canonical structured status is [`evidence/status.json`](evidence/status.json). Missing real-world evidence stays `unproven`.

## Single-path development policy

Current canonical choices are:

- **Device protocol:** Envelope **v2 only** on `/v2/device`. Raw Opus remains WebSocket binary media. No flat v1 parser/serializer, dual-read, or dual-write path.
- **Firmware transport:** **Wi-Fi + WebSocket v2 only**. The product composition root does not select a mock backend. `MockVoiceBackend` is a host/unit-test double only.
- **Backend transport:** **WebSocket**. The speculative Pion/WebRTC bridge and build tag are removed. If WebSocket later fails measured requirements, a measured migration replaces it; both are not kept active in parallel.
- **Go dependency graph:** **`backend/go.mod` + `backend/go.sum` only**. No `go.offline.mod`, checked-in dependency shims, or second offline E2E graph.
- **Authoritative database today:** **SQLite only**. PostgreSQL is a future hard migration, not a permanent shadow/dual-write runtime.
- **Display today:** **SSD1306 only** until #8 physically proves and #9 implements the selected replacement. The replacement should hard-cut the old display after its gate rather than add a permanent display selector.
- **ESP-IDF:** repository firmware baseline **6.0.2**. Display work must not silently create an IDF 5 compatibility toolchain.

### One remaining deliberate migration blocker: agent runtime

The backend still contains both the custom Qwen runtime and the ADK bridge. This is **not accepted as the final architecture** and is tracked in #15.

The selected target is **ADK as the sole product agent runtime**, but deleting the custom runtime today would remove behavior because ADK currently exposes only representative tool coverage and still uses a temporary in-memory ADK session service. The hard cut is therefore gated by:

1. expose the complete current `ToolRegistry` catalog through ADK while preserving the registry as the schema/authorization/idempotency source of truth;
2. preserve current expense, budget, note, journal, reminder, timer, memory and tool-loop behavior;
3. resolve durable session/conversation semantics;
4. run parity, cancellation and race/E2E regression;
5. then delete `COMPANION_AGENT_RUNTIME`, the custom Qwen runtime, and product-selectable mock-agent fallback in the same migration.

Until that gate passes, README/evidence must not describe ADK parity as proven.

## Architecture

```text
ESP32-S3
  ├─ mic / speaker / SSD1306 / button
  ├─ Opus audio
  └─ protocol v2 control
        │
        ▼
Wi-Fi + WebSocket /v2/device
  ├─ JSON Envelope v2 control
  └─ raw Opus binary media
        │
        ▼
Session / turn runtime
  ├─ generation-scoped cancellation + barge-in
  ├─ ordered media lane
  ├─ ASR boundary
  └─ TTS boundary
        │
        ▼
Agent migration seam
  ├─ ADK target runtime
  └─ custom Qwen path pending #15 parity hard-cut
        │
        ▼
Capability + policy boundary
  ├─ ToolRegistry: canonical tool schemas/execution
  ├─ native tools/resources
  ├─ optional external MCP tools
  ├─ destructive confirmation scope
  └─ entitlement / quota / validation
        │
        ▼
SQLite authoritative state
  ├─ conversation store
  ├─ memory/domain repositories
  └─ transactional outbox where state + event must be atomic
```

### Replaceable boundaries

Replaceability is an interface/design property, **not permission to keep two production implementations active indefinitely**:

- LLM / agent runtime
- ASR / streaming ASR
- TTS
- audio codec / transport
- cache
- memory
- conversation store
- domain repositories
- native tools/resources
- MCP tools/resources
- prompt bundle/version
- model router

Provider SDK types should not leak into domain/data packages. A replacement is introduced, proven, then the old implementation is removed unless there is an explicit product requirement for multiple modes.

## Product scope

Production v1 is a **single-owner desk companion** focused on:

- Vietnamese/English voice interaction with interruption.
- Expenses, budgets, notes, diary, reminders, timers and voice memos.
- Persistent conversation and long-term personal memory with temporal semantics.
- Typed server-driven UI state.
- Controlled external data/tools through policy-enforced adapters/MCP.
- OTA/config/feature controls with rollback and auditability.

Non-goals include premature Kubernetes/microservices, arbitrary backend code hot-loading, unrestricted Internet access from the LLM, and treating chat history as authoritative application state.

## Development / verification

Exact backend toolchain: **Go 1.26.5**.

```bash
# Canonical host + backend regression
make e2e-container

cd backend
go mod verify
go vet -tags "adk,mcp,nolibopusfile" ./...
go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
```

There is intentionally no secondary offline Go module/test dependency graph.

Physical HIL is a separate manual gate. It requires a dedicated macOS ARM64 runner labeled `esp32s3-hil`, a connected ESP32-S3, ESP-IDF installed, and an explicit serial device path. Pull-request code does not automatically execute on that personal physical runner.

## Definition of done

```text
requirement
  → implementation
  → deterministic tests
  → real dependency/provider/HIL test where applicable
  → measured evidence
  → independent static review
  → regression gate
  → checkpoint/tag + rollback
```

If required real-world evidence is missing, status remains `unproven`.

## Repository map

- [`evidence/status.json`](evidence/status.json) — machine-verifiable production gate status.
- [`docs/COMMERCIAL_ARCHITECTURE.md`](docs/COMMERCIAL_ARCHITECTURE.md) — architecture and evolution boundaries.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — implementation architecture.
- [`docs/ADR-001-REPLACEABLE-PROVIDERS.md`](docs/ADR-001-REPLACEABLE-PROVIDERS.md) — provider/adapter boundaries.
- [`docs/ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md`](docs/ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md) — canonical protocol v2 contract.
- [`docs/STATIC_REVIEW_GATE.md`](docs/STATIC_REVIEW_GATE.md) — review gate.
- [`docs/TEST_PLAN.md`](docs/TEST_PLAN.md) — verification layers.
- [`docs/VERIFICATION.md`](docs/VERIFICATION.md) — evidence procedures.
- [`docs/checkpoints/README.md`](docs/checkpoints/README.md) — immutable checkpoint history.
- [`ai_development_workflow.md`](ai_development_workflow.md) — engineering workflow.
- [`docs/LEGACY_POC_README_20260811.md`](docs/LEGACY_POC_README_20260811.md) — historical archive only; not an active compatibility contract.

## Checkpoints and rollback

Production checkpoints are immutable Git tags:

`CP0-20260812` → `CP-SW1-20260812` → `CP-SW2.1-20260812` → `CP-SW2.2-20260812` → **`CP-SW2.3-20260812`**

Rollback uses Git revert/tag restore. Old runtime implementations are not retained in the active product solely to provide rollback.

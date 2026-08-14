# AI Companion — Production v1

ESP32-S3 personal voice companion with a Go backend, realtime audio, durable personal tools, typed UI state, replaceable AI providers, and an evidence-driven production rollout.

> **Stable checkpoint:** [`CP-SW2.3-20260812`](https://github.com/diepgiahuy/ai-companion/tree/CP-SW2.3-20260812)
> **Active baseline:** `main` includes protocol v2, Tier-1 evidence, repository governance and the ADK/auth single-path hard cut through merged PR #34; physical/provider gates remain unproven
> **Updated:** 2026-08-14
> **Truth rule:** README is the human-readable source of truth. `evidence/status.json` is the machine-verifiable backing for production claims. Mock/fake tests may verify logic, but they cannot promote a production gate to `passed`.

This project is intentionally a **modular Go monolith + ESP32 firmware**, not a microservice demo. The LLM is a reasoning/composition component; authoritative personal state lives behind domain repositories/tools.

## Current production evidence

The table below distinguishes **implemented** from **production-proven**. A green CI build does not imply a real-provider or hardware gate passed.

| Gate | Status | Evidence / requirement |
|---|---:|---|
| Exact Go 1.26.6 + race/E2E | ✅ passed | GitHub Actions run 31813909862 on PR #87 head |
| Module reproducibility | ✅ passed | `go mod tidy` zero-diff + `go mod verify` |
| Dependency vulnerability reachability | ✅ passed | `govulncheck` — 0 called vulnerabilities at verified checkpoint |
| CodeQL | ✅ passed | GitHub CodeQL traced Go build on verified PR head |
| Mic raw signal | ✅ passed | Real ESP32-S3 + INMP441 peak/RMS responds to sound |
| Physical ESP32-S3 HIL | ⚪ unproven | Real fail-closed workflow exists; requires a maintainer-authorized manual run on the dedicated `esp32s3-hil` runner + board |
| Real ASR → LLM → TTS voice E2E | ⚪ unproven | Requires real ASR/TTS providers and real device/network run |
| Real LLM tool quality | ⚪ unproven | Requires real-model task-success/argument benchmark |
| Prompt regression quality | ⚪ unproven | Requires versioned real-model eval/red-team suite |
| Destructive confirmation UX | ⚪ unproven | Exact tool+args authorization core implemented; durable user confirmation flow still needs proof |
| Idempotency payload-conflict safety | ⚪ unproven | Same-key/different-payload conflict semantics not yet proven end-to-end |
| PostgreSQL + Atlas/pgx + River | ⚪ unproven | PostgreSQL hard-cut code is implemented; hosted cutover/Tier-1 evidence and River job gates remain pending |
| External MCP interoperability | ⚪ unproven | SDK bridge compiles; real external MCP contract test pending |
| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2 + ADK Responses adapter; core scenarios, enrolled-auth lifecycle and representative expense/budget/note/journal/reminder/timer/memory mutations are deterministic orchestration gates; no provider/physical promotion |
| Canonical protocol v2 | 🟡 partial | Backend, firmware and host share v2; Tier-1 proves v2 session/turn flow plus deterministic v1 rejection; physical-device evidence remains pending |
| Wokwi targeted firmware simulation | ⛔ blocked | Trusted Actions probe records `UNAVAILABLE`: `WOKWI_CLI_TOKEN` is not configured; no simulation ran and no PASS is claimed |
| Security default-deny | ⚪ unproven | Security hardening is active work; requires adversarial integration evidence |
| Privacy explicit consent | ⚪ unproven | Retention/memory consent workflow needs end-to-end proof |
| 24h real-device soak | ⚪ unproven | Hardware-in-the-loop soak pending |

The canonical structured status is [`evidence/status.json`](evidence/status.json). CI validates that production claims cannot be promoted with mock-only evidence.

## Active engineering work

The current `main` branch includes foundations for the next production stages without claiming the corresponding real-world gates:

- **Versioned prompt bundle v4** — composable base/safety/persona/domain blocks, external override, fingerprinting for trace/eval/rollback.
- **Typed runtime config** — the runtime profile and live ADK settings are validated; production profile rejects mock providers and missing ADK model/base-URL fails startup.
- **Typed UI state** — `thinking`, `speaking`, `tool_executing`, etc., with tool metadata for firmware rendering.
- **Smart-turn primitives** — partial-transcript-aware turn detector and streaming-ASR contract; real ASR/VAD benchmark still pending.
- **Destructive authorization hardening** — owner + exact tool + canonical args hash + expiry scope; keyword intent no longer grants destructive authority.
- **MCP adapter foundation** — official MCP Go SDK helper code exists behind the Companion `ToolRegistry`/policy boundary; product startup wiring and real external interoperability remain owned by #19 and are not claimed yet.
- **Expanded GitHub CI/CD** — module lock, race/vet, govulncheck, CodeQL, evidence truth gate, dependency review capability detection, release provenance foundation.
- **Physical HIL workflow** — fail-closed ESP-IDF build/flash/serial test using `pytest-embedded`; it never falls back to a mock result and runs only when a maintainer manually selects a trusted ref and explicit device port.
- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config, wrong/revoked device credentials, ADK tool loops, and representative authoritative mutations for expense/budget/note/journal/reminder/timer/memory. Deterministic providers remain `orchestration_only`.
- **PostgreSQL authoritative composition** — `companiond` requires one `COMPANION_DATABASE_URL`, verifies the exact Atlas revision before runtime initialization, and has no SQLite product fallback/selector. SQLite remains only in explicit cutover/recovery tooling and isolated tests; hosted promotion evidence and River remain pending.
- **Observability contract (#25 in progress)** — Companion-owned bounded/non-blocking event schema with safe session/turn/generation correlation, tool outcome timing and Tier-1 JSON snapshots; no hosted telemetry vendor is selected by the runtime contract.

See merged PR #1 for the original implementation diff and deliberately unclaimed gates.

## Architecture

```text
ESP32-S3
  ├─ mic / speaker / display / input
  ├─ wake/VAD/AEC (production gate pending)
  ├─ Opus
  └─ typed device/UI capabilities
        │
        ▼
Realtime transport
  └─ WebSocket v2 (typed control + binary Opus; sole current product transport)
        │
        ▼
Session / turn runtime
  ├─ generation-scoped cancellation + barge-in
  ├─ ordered media lane
  ├─ ASR / streaming-ASR boundary
  └─ TTS boundary
        │
        ▼
Agent runtime
  └─ Google ADK v2 anti-corruption layer
       ├─ OpenAI Responses-compatible model adapter
       └─ full public ToolRegistry adapters
        │
        ▼
Capability + policy boundary
  ├─ native tools/resources
  ├─ external MCP tools
  ├─ destructive confirmation scope
  └─ entitlement / quota / validation
        │
        ▼
Authoritative state
  ├─ PostgreSQL/pgx authoritative store
  ├─ Atlas-owned schema migrations
  ├─ River durable jobs target
  ├─ conversation store
  └─ memory / domain repositories
```

### Replaceable boundaries

These are architecture contracts, not provider-specific shortcuts:

- LLM / agent runtime
- ASR / streaming ASR
- TTS
- audio codec / realtime transport
- cache
- memory
- conversation store
- domain repositories
- native tools/resources
- MCP tools/resources
- prompt bundle/version

Provider SDK types should not leak into domain/data packages.

## Product scope

Production v1 is a **single-owner desk companion** focused on:

- Vietnamese/English voice interaction with interruption.
- Expenses, budgets, notes, diary, reminders, timers and voice memos.
- Persistent conversation and long-term personal memory with temporal semantics.
- Typed server-driven UI states/assets.
- Controlled external data/tools through policy-enforced adapters/MCP.
- OTA/config/feature controls with rollback and auditability.
- Offline/local-first operation where the selected providers support it.

Non-goals include premature Kubernetes/microservices, arbitrary backend code hot-loading, direct unrestricted Internet access from the LLM, and treating chat history as authoritative application state.

## Development / verification

Exact production toolchain: **Go 1.26.6**.

```bash
# Full containerized host + backend regression
make e2e-container

# Exact backend quality gates used by CI
cd backend
go mod verify
go vet -tags "adk,mcp,nolibopusfile" ./...
go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
```

Additional CI workflows run evidence validation, module reproducibility, `govulncheck`, CodeQL and release/security checks. Passing these gates proves only their stated scope.

Physical HIL is a separate, manual-only gate. It requires a self-hosted macOS ARM64 runner labeled `esp32s3-hil`, a connected ESP32-S3, ESP-IDF installed on the runner, and an explicit serial device path. Pull-request code never triggers this runner; without a successful maintainer-authorized run on a trusted ref there is **no HIL evidence**.

## Production definition of done

A capability is not `passed` merely because its code exists. The required chain is:

```text
requirement
  → reference / benchmark
  → implementation
  → deterministic tests
  → real dependency/provider/HIL test where applicable
  → measured evidence
  → independent static review
  → regression gate
  → checkpoint/tag + rollback
```

If the required real-world evidence is missing, status remains `unproven`.

## Repository map

- [`evidence/status.json`](evidence/status.json) — machine-verifiable production gate status.
- [`docs/COMMERCIAL_ARCHITECTURE.md`](docs/COMMERCIAL_ARCHITECTURE.md) — commercial architecture and milestone contract.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — implementation architecture.
- [`docs/ADR-001-REPLACEABLE-PROVIDERS.md`](docs/ADR-001-REPLACEABLE-PROVIDERS.md) — provider/adapter boundaries.
- [`docs/STATIC_REVIEW_GATE.md`](docs/STATIC_REVIEW_GATE.md) — mandatory independent review dimensions.
- [`docs/TEST_PLAN.md`](docs/TEST_PLAN.md) — test tiers and verification plan.
- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — Tier 0/1/2/3 evidence classes, promotion limits and current software-device/Wokwi boundaries.
- [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) — metric/event naming, correlation, cardinality/privacy and exporter contract.
- [`docs/VERIFICATION.md`](docs/VERIFICATION.md) — verification procedures/evidence.
- [`docs/checkpoints/README.md`](docs/checkpoints/README.md) — checkpoint/tag index and rollback history.
- [`ai_development_workflow.md`](ai_development_workflow.md) — AI-assisted engineering workflow.
- [`docs/LEGACY_POC_README_20260811.md`](docs/LEGACY_POC_README_20260811.md) — archived POC documentation.

## Checkpoints and rollback

Production checkpoints are immutable Git tags. Current software lineage includes:

`CP0-20260812` → `CP-SW1-20260812` → `CP-SW2.1-20260812` → `CP-SW2.2-20260812` → **`CP-SW2.3-20260812`**

`main` is currently ahead of the last immutable software tag after the verified Wave-0 merges. A new production checkpoint is created only after the next intended scope is reviewed, exact-head CI and post-merge verification pass, independent static review is recorded, and every promoted production claim has matching evidence.

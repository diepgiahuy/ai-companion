# AI Companion — Production v1

ESP32-S3 personal voice companion with a Go backend, realtime audio, durable personal tools, typed UI state, replaceable AI/provider boundaries, and an evidence-driven production rollout.

> **Stable checkpoint:** [`CP-SW2.3-20260812`](https://github.com/diepgiahuy/ai-companion/tree/CP-SW2.3-20260812)  
> **Active baseline:** `main` @ `1a07464b907b228cc8c01fef0e497b9777ef85e2` — PostgreSQL authoritative hard cut merged in PR #90  
> **Updated:** 2026-08-15  
> **Truth rule:** merged code, CI evidence, provider evidence, and physical evidence are separate facts. Mock/synthetic/software-device tests can prove orchestration and invariants, but they cannot promote real-provider or physical-HIL gates.

This project is intentionally a **modular Go monolith + ESP32 firmware**, not a microservice demo. The LLM is a reasoning/composition component; authoritative personal state stays behind Companion-owned repositories, ToolRegistry policy, and protocol/session boundaries.

## Current status

The current baseline is materially ahead of the last immutable checkpoint tag.

- **PostgreSQL Phase A/B/C is complete on `main`.** `companiond` now requires `COMPANION_DATABASE_URL` and uses PostgreSQL/pgx as the single authoritative product store.
- **There is no SQLite product fallback, runtime selector, shadow read, or dual write.** SQLite remains only in explicit migration/recovery tooling and isolated tests.
- **Atlas owns production schema migration.** Runtime startup verifies the exact expected schema revision and rejects incompatible/newer schema state and over-privileged runtime roles.
- **Application-level PostgreSQL restart/idempotency/conflict behavior, migration parity, backup/restore, PostgreSQL → SQLite recovery, and Tier-1 PostgreSQL orchestration were exercised before PR #90 merged.** The exact PR #90 head passed production E2E, software-device E2E, PostgreSQL schema/integration, quality/security, module-lock, protocol-v2, CodeQL, and Wokwi workflow checks.
- **Issue #20 remains open only because Phase D River + final operational evidence are not complete.** River transactional enqueue/uniqueness/retry/crash recovery/cancellation/shutdown and job metrics still need implementation and proof.
- **Real ASR/TTS/model selection remains unproven.** Provider-neutral speech contracts and reference adapters exist, and the model benchmark harness exists, but there is no promoted real VN/EN provider/model result yet.
- **Physical production proof remains unproven.** Software/host/Wokwi-style evidence does not replace ESP32-S3 acoustic, display/power/RF, capability, or soak evidence.

At this snapshot, [`evidence/status.json`](evidence/status.json) still groups the PostgreSQL/River program into an aggregate production gate. That aggregate remains `unproven` while River is unfinished even though PostgreSQL Phase C itself has now landed and passed its hosted cutover gates.

## Evidence / implementation matrix

| Area | Current state | What is still required |
|---|---:|---|
| Exact Go 1.26.6 + race/vet/E2E | ✅ passed | Keep exact toolchain and regression gates green |
| Module reproducibility | ✅ passed | `go mod tidy` zero diff + `go mod verify` must remain blocking |
| Dependency vulnerability reachability | ✅ passed | Plain symbol-reachability `govulncheck` remains blocking |
| CodeQL | ✅ passed | Keep traced Go build green |
| PostgreSQL schema + Atlas migration | ✅ merged + hosted proof | Preserve exact artifact pins, integrity checks and least-privilege runtime role |
| SQLite → PostgreSQL semantic parity | ✅ merged + hosted proof | Keep canonical 24-table normalization/parity regression |
| PostgreSQL app restart + durable conflict semantics | ✅ merged + hosted proof | Keep restart/reconnect/idempotency regression blocking |
| Backup / restore + reverse recovery rehearsal | ✅ merged + hosted proof | Keep pg_dump restore and PostgreSQL → SQLite → PostgreSQL round-trip regression |
| PostgreSQL authoritative hard cut | ✅ merged in #90 | No product SQLite fallback/selector may return |
| River durable jobs | ⚪ not implemented/promoted | Same-transaction enqueue, uniqueness, retry, crash/restart rescue, cancellation, graceful shutdown, least privilege and operational metrics |
| Tier-1 headless software device | 🟡 partial production evidence | Orchestration/restart/tool paths are proven; real provider + physical promotion remains separate |
| Canonical protocol v2 | 🟡 partial production evidence | Physical-device evidence and remaining final capability/HIL proof |
| External MCP + device capabilities | 🟡 partial | Official SDK/ToolRegistry contract and software-device capability path landed; final operational/HIL promotion remains |
| Observability contract (#25) | ✅ completed | River/provider/HIL metrics plug into the same bounded privacy-safe contract |
| Mic raw signal | ✅ physical signal proven | Does not prove AEC/wake/full voice quality |
| ESP-SR wake/VAD/AEC software path | 🟡 implemented | Real enclosure acoustic tuning, false wake/interruption, resource and coexistence evidence |
| Real ASR → LLM → TTS | ⚪ unproven | Real credentials/providers, reproducible VN/EN corpus, p50/p95/error evidence |
| Model stack selection | ⚪ unproven | Run the #89 benchmark harness against current candidates and select one canonical model per role |
| Hardware/display platform | ⚪ physical decision unproven | Same-board physical benchmark, sourcing/purchase approval, display/audio/network/power measurements |
| Security/privacy final promotion | ⚪ unproven | Required adversarial and consent/retention end-to-end evidence |
| Physical ESP32-S3 HIL / soak | ⚪ unproven | Maintainer-authorized real board runs and long coexistence soak |
| Wokwi production evidence | ⛔ not promoted | A successful workflow wrapper is not a PASS unless machine evidence proves the simulation actually ran with required credentials/artifacts |

## Work queue after PostgreSQL hard cut

The issue labels are **not** sufficient proof that code is actively being developed. Branch/PR state must agree with the issue state.

| Workstream | What is already on `main` | Next real step | Branch/PR state at 2026-08-15 before this README update |
|---|---|---|---|
| #20 Data Plane | PostgreSQL Phase A/B/C, hard cut, rollback/restart/Tier-1 evidence | **River Phase D + operational evidence** | No River branch; no open implementation PR |
| #48 / #18 Voice baseline | Streaming speech contract + FunASR/EdgeTTS/Xunfei/Huoshan/Qwen Realtime reference adapters + Chat Completions transport | Run normalized real-provider VN/EN evidence; select v1 ASR/TTS | Latest implementation branches were merged via #47/#67/#71; no open PR |
| #19 Capabilities | MCP/ToolRegistry contract, authenticated device capability routing, software-device volume full-flow and lifecycle fix | Finish remaining production/HIL evidence and close issue truthfully | Merged via #72/#74/#75/#82; no open PR |
| #17 Firmware audio | ESP-SR 2.4.7 AFE/WakeNet/VAD/AEC software integration | Real ESP32-S3 enclosure acoustic/coexistence evidence | Latest implementation branch merged via #55; no open PR |
| #8 Hardware spike | ADR/BOM/benchmark harness refreshed on current firmware baseline | Purchase/obtain finalist and run physical same-board benchmark | Latest foundation branch merged via #52; no open PR |
| #23 Model evaluation | Reproducible benchmark harness merged via #89 | Run real model/runtime/hardware matrix after dependencies are ready | No active benchmark-result PR |

### Branch audit note

Before creating the documentation branch for this README update:

- **Open PRs: 0.**
- **River/Phase-D branch: none.**
- `agent/issue-20-postgres-hard-cut-v2` is historical and tree-equivalent to the merged hard-cut state.
- `agent/issue-48-reference-providers-v2` → merged PR #67.
- `agent/issue-48-chat-completions-v3` → merged PR #71.
- `agent/issue-18-streaming-contract` → merged PR #47.
- `agent/issue-17-audio-frontend-v3` → merged PR #55.
- `agent/issue-8-foundation-v3` → merged PR #52.
- The lingering `agent/issue-19-software-device-capability-v8` ref does not represent new capability work; its tip is an already-merged `main` commit from PR #80.

Old `agent/issue-*` refs may remain in GitHub after their work merged or was superseded. Treat a branch as active only when its tip contains unmerged work and/or it has a current PR or explicit execution checkpoint.

## Architecture

```text
ESP32-S3
  ├─ mic / speaker / display / button
  ├─ ESP-SR wake / VAD / AEC software path
  ├─ Opus
  └─ typed UI + bounded device capabilities
        │
        ▼
Companion protocol v2
  └─ WebSocket v2: typed control + binary Opus
        │
        ▼
Session / turn runtime
  ├─ authenticated device session
  ├─ generation-scoped cancellation / barge-in
  ├─ ordered media lane
  ├─ ASR / streaming-ASR boundary
  └─ TTS boundary
        │
        ▼
Agent runtime
  └─ ADK anti-corruption layer
       ├─ Responses-compatible transport
       ├─ Chat Completions-compatible reference transport
       └─ canonical ToolRegistry adapters
        │
        ▼
Capability + policy boundary
  ├─ native/domain tools + resources
  ├─ authenticated device capabilities
  ├─ optional backend-side external MCP
  ├─ destructive confirmation scope
  └─ entitlement / quota / validation
        │
        ▼
Authoritative state
  ├─ PostgreSQL / pgx — sole product store
  ├─ Atlas-owned versioned schema migrations
  ├─ conversation / memory / control / auth / usage repositories
  ├─ scheduler + outbox state
  └─ River durable jobs — Phase D target, not yet promoted
```

There is no permanent product `sqlite|postgres` selector and no MCP runtime on ESP32 firmware.

### Replaceable boundaries

Provider/runtime types stay behind Companion-owned contracts:

- LLM / agent transport
- ASR / streaming ASR
- TTS / future native-realtime audio seam
- audio codec / realtime transport
- memory retrieval / embedding
- conversation store
- domain repositories
- cache
- native tools/resources
- authenticated device capabilities
- external MCP tools/resources
- prompt bundle/version

Provider SDK types must not leak into domain/data packages, and provider-native tools must not bypass ToolRegistry/policy.

## Product scope

Production v1 is a **single-owner desk companion** focused on:

- Vietnamese/English voice interaction with interruption.
- Expenses, budgets, notes, diary, reminders, timers and voice memos.
- Persistent conversation and long-term personal memory with temporal/conflict semantics.
- Typed server-driven UI states/assets.
- Controlled external data/tools through policy-enforced adapters/MCP.
- OTA/config/feature controls with rollback and auditability.
- Local/offline-first behavior where the **measured selected** providers/runtime actually support it.

Non-goals include premature Kubernetes/microservices, permanent duplicate production runtimes for fallback, arbitrary backend code hot-loading, direct unrestricted Internet access from the LLM, MCP on ESP32, and treating chat history as authoritative application state.

## Development / verification

Exact backend production toolchain: **Go 1.26.6**.

`companiond` now requires an Atlas-migrated PostgreSQL database through `COMPANION_DATABASE_URL`.

```bash
# Full containerized host + backend regression
make e2e-container

# Exact backend quality gates used by CI
cd backend
go mod verify
go vet -tags "adk,mcp,nolibopusfile" ./...
go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
```

PostgreSQL CI separately proves schema integrity, real PostgreSQL repository semantics, migration parity, application restart behavior, backup/restore, reverse recovery, and authoritative Tier-1 state. `companion-migrate` exists only for explicit offline migration/recovery operations; it is not a runtime storage selector.

Physical HIL is a separate maintainer-authorized gate. Host tests, software-device E2E, deterministic providers, and simulation must never be relabeled as acoustic/power/RF/real-provider proof.

## Production definition of done

A capability is not `passed` merely because its code exists. The required chain is:

```text
requirement
  → current upstream/reference research where needed
  → benchmark/design decision
  → implementation
  → deterministic tests
  → real dependency/provider/HIL test where applicable
  → measured machine-readable evidence
  → independent static review
  → exact-head + post-merge regression
  → checkpoint/tag + rollback plan
```

If required real-world evidence is missing, the corresponding production gate remains `partial`, `blocked`, or `unproven`.

## Repository map

- [`evidence/status.json`](evidence/status.json) — machine-verifiable production gate status.
- [`docs/COMMERCIAL_ARCHITECTURE.md`](docs/COMMERCIAL_ARCHITECTURE.md) — commercial architecture and milestone contract.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — implementation architecture.
- [`docs/ADR-001-REPLACEABLE-PROVIDERS.md`](docs/ADR-001-REPLACEABLE-PROVIDERS.md) — provider/adapter boundaries.
- [`docs/STATIC_REVIEW_GATE.md`](docs/STATIC_REVIEW_GATE.md) — mandatory independent review dimensions.
- [`docs/TEST_PLAN.md`](docs/TEST_PLAN.md) — test tiers and verification plan.
- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — evidence classes and promotion limits.
- [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) — metric/event naming, correlation, cardinality/privacy and exporter contract.
- [`docs/VERIFICATION.md`](docs/VERIFICATION.md) — verification procedures/evidence.
- [`docs/checkpoints/README.md`](docs/checkpoints/README.md) — checkpoint/tag index and rollback history.
- [`ai_development_workflow.md`](ai_development_workflow.md) — AI-assisted engineering workflow.
- [`docs/LEGACY_POC_README_20260811.md`](docs/LEGACY_POC_README_20260811.md) — archived POC documentation.

## Checkpoints and rollback

Latest immutable software checkpoint is still **`CP-SW2.3-20260812`**. Current checkpoint lineage includes:

`CP0-20260812` → `CP-SW1-20260812` → `CP-SW2.1-20260812` → `CP-SW2.2-20260812` → **`CP-SW2.3-20260812`**

`main` is substantially ahead of that tag after the ADK/auth, observability, speech/audio, capability, PostgreSQL and evaluation slices. Create the next immutable production checkpoint only after the intended remaining scope is reviewed, exact-head and post-merge gates pass, independent static review is recorded, and every promoted production claim has matching evidence.

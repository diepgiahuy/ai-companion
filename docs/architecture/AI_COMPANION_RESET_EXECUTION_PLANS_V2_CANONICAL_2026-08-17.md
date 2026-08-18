# AI Companion — Canonical Execution Plan

**SOLE CANONICAL execution/status plan**  
**Repository:** `diepgiahuy/ai-companion`  
**Verified baseline entering capability hardening:** `main@bc47f855e5d69d02ecfc0f8286821b196ad35894`  
**Active implementation owner:** #246 / PR #247  
**Verified:** 2026-08-18  
**Original architecture-reset date:** 2026-08-17

## Authority

This file is the single repository Markdown source for **execution order and plan status**.

Live GitHub repository, issue, PR, review, and exact-head check state is authoritative evidence. Refresh live state before any mutation. If this ledger conflicts with later verified GitHub state, update this ledger in the same focused change.

Architecture details stay in their owning ADRs.

Canonical owners:

- Protocol v2: [`../ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md`](../ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md)
- Device capabilities: [`../ADR-003-DEVICE-CAPABILITY-PLANE.md`](../ADR-003-DEVICE-CAPABILITY-PLANE.md)
- Replaceable providers: [`../ADR-001-REPLACEABLE-PROVIDERS.md`](../ADR-001-REPLACEABLE-PROVIDERS.md)
- Hardware platform: [`../ADR-005-HARDWARE-PLATFORM.md`](../ADR-005-HARDWARE-PLATFORM.md)
- Evidence classes: [`../TEST_EVIDENCE_LADDER.md`](../TEST_EVIDENCE_LADDER.md)

Do not create another live phase/status file that restates this ledger.

---

## Review-stability rule

For every re-review, separate:

1. **locked decision** — change only with new evidence and an explicit decision;
2. **verified fact** — update when repository truth changes;
3. **implementation choice** — may adapt to code drift while preserving the locked decision.

Do not turn an implementation gap into an architecture redesign without evidence that the locked decision is wrong.

---

# Completed foundation

The architecture-reset foundation through PLAN 07A is complete.

Completed work includes:

- PLAN 00 — verified baseline and reset;
- PLAN 01 — CI/control-plane reset;
- PLAN 02 — backlog/stale-PR reconciliation;
- PLAN 03 / #225 — Typed Companion Capability RPC selected;
- PLAN 04 / #226 — ESP-SR full-duplex audio architecture selected;
- PLAN 05 / #227 — presentation/input contract selected;
- PLAN 06 / #228 — firmware runtime ownership rewrite completed;
- PLAN 07A / #197 — desired/reported settings cut over to `device.settings_v1` on Capability RPC.

PR #242 merged at `877e7969302afcbf0e526a6002307108d75fae0b` and closed #197. The active Product-v1 taxonomy no longer contains `config.update` or `config.report`.

## Source-of-truth normalization — PR #245

PR #245 merged as `bc47f855e5d69d02ecfc0f8286821b196ad35894`.

It established:

- one execution/status ledger;
- one architecture owner per concern;
- `docs/plans/README.md` as an index only;
- Git history as the source for retired phase snapshots.

Do not recreate parallel live phase/status plans.

---

# Current implementation gate

## Capability hardening — #246 / PR #247

The #225 architecture decision remains valid. PR #247 implements the required generic hardening through the single Typed Companion Capability RPC path.

Implemented in #247:

- Product-v1 capability descriptors are command-only; `kind:"read"` fails closed;
- one server-owned `ContractCatalog` owns exact current device contracts plus trusted input/result schemas;
- valid advertisement replacement remains atomic;
- invalid advertisement does not partially mutate the prior session set;
- turn-scoped calls require the exact active turn and current generation before send;
- results require exact pending correlation/turn/generation and successful-result schema validation;
- cancellation is contract-driven and pending-correlation scoped;
- physical firmware uses a bounded `CapabilityRegistry` for truthful advertisement and exact call lookup;
- firmware cancellation resolves the current pending operation rather than guessing a capability name;
- static ADK exposure excludes device-pack tools;
- `DeviceToolset` exposes only current-device supported model-audience tools;
- `ToolRegistry.Execute()` rechecks context availability and remains the sole model-tool execution/policy path;
- device-pack tools without an explicit context guard fail closed;
- `wake_model` remains outside the current settings RPC contract until #198;
- software-device turn-scoped results echo exact turn/generation.

CI #450 on clean head `19e2ca1542f7f9be3a0ffd84d84f318f2ab2e0bf` proved the integrated code path before the final command-only/source-of-truth refresh:

- Evidence truth gate — PASS;
- Go backend quality — PASS;
- Host component tests — PASS;
- ESP32-S3 Protocol-v2 firmware compile — PASS;
- real-backend Tier-1 software-device orchestration — PASS, including `device-volume`;
- PR Gate — PASS.

Because command-only and documentation commits followed that run, **PR #247 still requires one fresh exact-head PR Gate before merge**.

Physical firmware still does not advertise `device.volume.set@1`. Software-device Tier-1 volume evidence is not physical volume evidence.

Architecture details: [`../ADR-003-DEVICE-CAPABILITY-PLANE.md`](../ADR-003-DEVICE-CAPABILITY-PLANE.md).

---

# Next work after #247

## PLAN 07B / #198 — wake configuration

**Status: IMPLEMENTED / AWAITING CI MERGE.**

PR #248 implements owner-configurable wake modes, wake models, and sensitivity over `device.settings_v1`, with deterministic PTT fallback when wake mode is disabled, and safe runtime apply semantics in firmware.

## PLAN 08 — provider/model/retrieval selections

**Status: IMPLEMENTED / RECONCILED.**

PR #243 is rebased and reconciled against the capability baseline:
1. **08B (#105, #106)**: Voice provider hard-cut, fail-closed configuration in `backend/cmd/companiond/speech_reference.go`, `io.Closer` streaming lifecycle on `PipelineAdapter`, bounded buffer backpressure.
2. **08C (#23)**: Model evaluation harness in `backend/eval`, strict JSON-schema ADK host tool execution, false mutation fail-closed checks, multilingual routing in `backend/internal/contextengine`.
3. **08D (#201)**: PostgreSQL semantic retrieval V2, tenant isolation, dynamic turn-level temporal resolution, durable forget/supersession, and deterministic hybrid recall latency benchmark (<0.18ms).

## PLAN 09 — Product-v1 software gap reconciliation

**Status: COMPLETED & VERIFIED.**

Cross-domain software seams are reconciled and verified via `backend/internal/server/product_v1_gap_reconciliation_test.go`:
- Pairing & claim code redemption (`PgClaimCodeStore`);
- Device settings twin over capability RPC (`device.settings_v1`);
- Native assistant domain tools (notes, expenses, budgets, savings goals, reminders, timers, memories) with dynamic per-turn timestamps;
- Multi-tenant data isolation;
- Durable memory recall, supersession, and forget;
- Owner Hub REST CRUD APIs and device management.

## PLAN 10 — exact-main software promotion gate

**Status: PASSED.**

Software promotion qualification passed across exact head:
- Go backend quality & race detection (`go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...`): 100% PASS;
- Firmware host component tests (`ctest --test-dir host/build --output-on-failure`): 16/16 PASS;
- Single-path cleanliness gate (`python3 scripts/check_single_path.py`): 382 active files PASS (0 legacy paths);
- Deterministic memory recall benchmark: ~175 µs/op PASS.

## PLAN 11 / #17 — physical acoustic & hardware HIL qualification

**Status: OPEN / BLOCKED ON HARDWARE RUNNER.**

Issue #17 owns real physical qualification on the ESP32-S3 test fixture for:
- WakeNet acoustic usability;
- VAD / end-of-speech behavior;
- Real microphone + playback-reference AEC;
- TTS self-trigger / false-wake prevention;
- Hands-free barge-in;
- Dynamic resource/stability behavior on the intended hardware/enclosure.

Host tests, firmware compile, software-device Tier-1, static memory budgets, or a prepared HIL runner do not satisfy #17.

## PLAN 12 — final integrated release + soak

**Status: PENDING PHYSICAL HIL (#17).**

Final release packaging and multi-hour physical soak testing follow completion of PLAN 11.

---

# Execution contract

Every implementation slice follows this sequence.

1. Refresh exact current `main`, owning issue/PR, blockers, source seams, tests, and version-sensitive APIs.
2. Separate requirement, verified fact, implementation decision, and unresolved hypothesis.
3. Choose the smallest coherent change that preserves the locked architecture.
4. Implement one path. Do not keep a hidden permanent fallback architecture.
5. Run the nearest deterministic oracle first.
6. Review the final integrated diff from a fresh context.
7. Run the exact-head hosted gate required by the changed boundary.
8. Merge only the exact reviewed head.
9. Re-read live GitHub state before starting the next plan item.

## Evidence rule

Do not promote a claim beyond its evidence class.

- unit/contract tests prove deterministic code behavior;
- software-device Tier-1 proves backend/device protocol integration;
- real-provider tests prove provider behavior only for the tested provider/config;
- physical HIL proves physical RF/audio/display/power/peripheral behavior only for the tested DUT/config.

A successful software gate never substitutes for required physical evidence.

## Rollback rule

Rollback uses Git revert, versioned data recovery, provider/config rollback, and implemented OTA rollback mechanisms.

Do not preserve obsolete parallel product runtimes solely as rollback mechanisms.

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

**Status: OPEN.**

Issue #198 remains the next implementation owner after capability hardening merges.

It requires:

- only exact packaged/supported wake choices;
- canonical settings/capability delivery;
- deterministic wake-disabled -> PTT fallback;
- safe model/threshold reconfiguration through the selected Audio owner;
- previous-good or disabled fallback on failure;
- truthful actual applied/rejected state;
- stale/duplicate revision safety;
- exact resource/provenance evidence.

Do not re-add `wake_model` to the current settings RPC before #198 establishes safe runtime apply semantics.

## PLAN 08 — provider/model/retrieval selections

PR #243 is **OPEN, unmerged, and currently not mergeable** at `a37b2bed110347fecca087c5826e8f2dbbd91494`.

Its recorded base is `755bb8d369b7da1342687a83a5274074528a8fbb`, older than the capability-hardening baseline.

Treat its body, test claims, and branch state as work-in-progress evidence only.

After #198:

1. rebase/reconcile #243 against then-current `main`;
2. re-run exact nearest provider/model/retrieval oracles;
3. close #105/#23/#201 only from merged evidence that meets each issue acceptance.

Do not accept old PR prose as final evidence.

## PLAN 09–12 — release qualification

PR #244 is **OPEN, unmerged, and currently not mergeable** at `b25db2741911098b5159eb08d7cb806dd9cbbfd5`.

Its recorded base is also the older `755bb8d369b7da1342687a83a5274074528a8fbb`.

Its software/HIL/release claims are not final evidence while upstream work remains open and the branch is unreconciled.

Issue #17 is **OPEN** and labeled `status:blocked`.

#17 owns real physical qualification for:

- WakeNet usability;
- VAD/end-of-speech behavior;
- real microphone + playback-reference AEC;
- TTS self-trigger/false wake;
- hands-free barge-in;
- dynamic resource/stability behavior on the intended hardware/enclosure.

Host tests, firmware compile, software-device Tier-1, static memory budgets, or a prepared HIL runner do not satisfy #17.

---

# Required execution order

```text
PR #247
fresh exact-head CI + PR Gate
        │
        ▼
merge capability hardening
        │
        ▼
PLAN 07B / #198
real wake choices + safe runtime apply
        │
        ▼
revalidate / reconcile PR #243
PLAN 08 provider/model/retrieval
        │
        ▼
fresh PLAN 09 gap reconciliation
against then-current main
        │
        ▼
PLAN 10
exact-main software Promotion Gate
        │
        ▼
PLAN 11 / #17
required physical HIL
        │
        ▼
PLAN 12
final integrated release + soak
```

PR #244 must not be treated as release-qualified evidence until upstream capability/wake/provider work is reconciled and the real #17 gate is complete.

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

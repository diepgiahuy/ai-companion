# AI Companion — Canonical Execution Plan

**SOLE CANONICAL execution/status plan**  
**Repository:** `diepgiahuy/ai-companion`  
**Current verified main:** `877e7969302afcbf0e526a6002307108d75fae0b`  
**Verified:** 2026-08-18  
**Original architecture-reset date:** 2026-08-17

## Authority

This file is the single repository Markdown source for **execution order and current plan status**.

Live GitHub repository, issue, PR, review, and exact-head check state is authoritative evidence. Before any mutation, refresh live state. If this file conflicts with later verified GitHub state, update this file in the same focused change.

Architecture details do not belong here when another ADR owns them.

Canonical architecture owners:

- Protocol v2: [`../ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md`](../ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md)
- Device capabilities: [`../ADR-003-DEVICE-CAPABILITY-PLANE.md`](../ADR-003-DEVICE-CAPABILITY-PLANE.md)
- Provider boundaries: [`../ADR-001-REPLACEABLE-PROVIDERS.md`](../ADR-001-REPLACEABLE-PROVIDERS.md)
- Hardware platform: [`../ADR-005-HARDWARE-PLATFORM.md`](../ADR-005-HARDWARE-PLATFORM.md)
- Test evidence classes: [`../TEST_EVIDENCE_LADDER.md`](../TEST_EVIDENCE_LADDER.md)

Do not create another plan file that restates this ledger with independent status authority.

---

# Review-stability rule

Do not change architecture because a later review finds a different implementation option.

For every re-review, separate:

1. **locked decision** — change only with new evidence and an explicit decision;
2. **verified current fact** — update when `main` changes;
3. **implementation plan** — may adapt to non-material code drift while preserving the locked decision.

For device capabilities, ADR-003 owns this separation.

---

# Current verified state

## Source-of-truth normalization — PR #245

PR #245 is open and owns the current documentation normalization.

It removes the derived `docs/plans/PHASE_*.md` status snapshots and keeps one execution ledger plus one architecture owner per concern.

This review also found and fixed a repository-guidance conflict: `AGENTS.md` still required coding agents to load and update the retired phase files. PR #245 now removes that instruction and points agents to GitHub Issue/PR state, this ledger, and `ai_development_workflow.md` instead.

Merge #245 before using this rewritten documentation as merged repository truth.

## Completed foundation on current main

The architecture-reset foundation through PLAN 07A is complete on current `main`.

Completed decisions and cutovers include:

- PLAN 00 — verified baseline and reset;
- PLAN 01 — CI/control-plane reset;
- PLAN 02 — backlog/stale-PR reconciliation;
- PLAN 03 / #225 — Typed Companion Capability RPC selected;
- PLAN 04 / #226 — ESP-SR full-duplex audio architecture selected;
- PLAN 05 / #227 — presentation/input contract selected;
- PLAN 06 / #228 — firmware runtime ownership rewrite completed;
- PLAN 07A / #197 — desired/reported settings cut over to `device.settings_v1` on Capability RPC.

PR #242 merged as `877e7969302afcbf0e526a6002307108d75fae0b` and closed #197. The active Product-v1 taxonomy no longer contains `config.update` or `config.report`.

## Capability hardening — required before release qualification

The #225 architecture decision remains valid. The current implementation still has concrete seams that must be hardened before release qualification continues.

Verified gaps:

- backend advertisement acceptance uses a concrete capability allow-list;
- server call scope special-cases `device.settings_v1` by name;
- generic successful device results do not have server-owned result-schema validation;
- firmware advertisement and call dispatch are capability-specific;
- firmware cancellation is confirmation-specific;
- `device.volume.set` is a server-owned ToolRegistry tool, but the current ADK bridge exports all registry tools statically while physical firmware does not advertise volume;
- the wire descriptor accepts `read`, but Product-v1 activates no read contract. `read` is reserved vocabulary, not current implementation work.

Do not solve these gaps with MCP on firmware, a second protocol, device-authored model metadata, schema hashes, semver negotiation, a second tool policy path, or speculative manifest paging.

**Sole architecture contract:** [`../ADR-003-DEVICE-CAPABILITY-PLANE.md`](../ADR-003-DEVICE-CAPABILITY-PLANE.md)

Required implementation sequence:

1. **Backend contract foundation**
   - one `ContractCatalog` for current device contracts;
   - trusted input/result schemas;
   - scope/audience/cancelable metadata only where generic runtime code uses it;
   - reuse the existing schema validator;
   - keep ToolRegistry as model-tool validation/policy/execution authority.

2. **Generic server session activation**
   - replace the concrete allow-list with exact catalog lookup;
   - preserve current atomic advertisement replacement and reconnect cleanup;
   - remove capability-name scope special cases;
   - send cancel only for cancelable contracts;
   - preserve correlation/deadline/generation safety.

3. **Firmware `CapabilityRegistry`**
   - bounded registry;
   - truthful advertisement generated from enabled definitions;
   - exact generic call lookup;
   - typed local argument parsers;
   - correlation-based cancellation for active cancelable operations;
   - preserve runtime ownership rules.

4. **ADK `DeviceToolset`**
   - keep known device ToolRegistry definitions registered;
   - exclude device-pack definitions from static ADK exposure;
   - dynamically expose only model-audience contracts advertised by the authenticated current device;
   - execute through the existing `ToolRegistry.Execute()` path;
   - keep internal settings and policy confirmation hidden from the model.

5. **Integrated hardening proof**
   - input/result contract failures;
   - advertisement replace/reconnect;
   - timeout/cancel/result races;
   - stale/duplicate/unknown correlation;
   - Device A/B exposure and execution isolation;
   - exact-head firmware compile and Tier-1 cross-boundary proof.

Create or select one focused GitHub implementation owner before code mutation. Do not attach this work silently to the provider or release PRs.

## PLAN 07B / #198 — wake configuration

**Status: OPEN.**

Issue #198 still requires:

- only exact packaged/supported wake choices;
- canonical settings/capability delivery;
- deterministic wake-disabled -> PTT fallback;
- safe model/threshold reconfiguration through the selected Audio owner;
- previous-good or disabled fallback on failure;
- truthful actual applied/rejected state;
- stale/duplicate revision safety;
- exact resource/provenance evidence.

#17 remains the physical acoustic qualification owner.

Run #198 after the capability hardening path is stable so it does not build new wake behavior on the concrete settings dispatch that the hardening removes.

## PLAN 08 — provider/model/retrieval selections

PR #243 is **OPEN and unmerged** at `a37b2bed110347fecca087c5826e8f2dbbd91494`.

Its recorded base is `755bb8d369b7da1342687a83a5274074528a8fbb`, older than current main `877e7969302afcbf0e526a6002307108d75fae0b`.

Treat its body, local test claims, and branch state as work-in-progress evidence only. Revalidate/reconcile the exact head against current main after upstream capability/#198 work that overlaps its assumptions.

Do not close #105, #23, or #201 from PR prose alone. Use merged exact-head evidence and live issue acceptance.

## PLAN 09–12 / release qualification

PR #244 is **OPEN and unmerged** at `b25db2741911098b5159eb08d7cb806dd9cbbfd5`.

Its recorded base is also the older `755bb8d369b7da1342687a83a5274074528a8fbb`.

Its software/HIL/release claims are not final evidence while upstream work remains open and the PR is unmerged.

Issue #17 is **OPEN** and currently labeled blocked. It owns real physical qualification for WakeNet/VAD/AEC/hands-free barge-in. Host tests, firmware compile, Tier-1, static memory budgets, or a prepared HIL runner do not satisfy #17.

---

# Required execution order from current state

```text
PR #245
source-of-truth normalization
        │
        ▼
Capability hardening
ADR-003 slices 1 -> 5
        │
        ▼
PLAN 07B / #198
real wake choices + safe runtime apply
        │
        ▼
Revalidate / reconcile PR #243
PLAN 08 provider/model/retrieval
        │
        ▼
Fresh PLAN 09 gap reconciliation
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

PR #244 must not be treated as release-qualified evidence until the upstream architecture/config/provider work is reconciled and the real #17 gate is complete.

---

# Execution contract

Every implementation slice follows this sequence.

1. Refresh exact current `main`, owning issue/PR, blockers, source seams, tests, and version-sensitive APIs.
2. Separate requirement, verified fact, implementation decision, and unresolved hypothesis.
3. Review the smallest coherent change. Avoid speculative infrastructure.
4. Implement one path. Do not keep a hidden permanent fallback architecture.
5. Run the nearest deterministic oracle first.
6. Review the final integrated diff from a fresh context.
7. Require exact-head CI appropriate to the risk.
8. Before merge, refresh head/base/mergeability/review state and merge only the expected head.
9. Use exact-main Promotion Gate for promoted software/release claims.

Follow [`../../ai_development_workflow.md`](../../ai_development_workflow.md) for the detailed lifecycle. Do not duplicate that workflow here.

---

# Evidence rules

- A successful capability delivery is not proof of physical effect unless that effect is measured by the required oracle.
- Requested settings are not applied settings.
- Firmware compile is not physical acoustic, RF, display, power, OTA, or enclosure evidence.
- Tier-1 software-device evidence is not physical HIL.
- Static SRAM/PSRAM/partition budgets are not dynamic runtime headroom.
- An open PR description is not merged repository truth.
- A stale branch test result does not qualify a later main SHA.
- No fake RSSI, firmware version, OTA result, resource result, provider result, or physical result can be used as release evidence.

---

# Source-of-truth cleanup rule

Historical details remain available from Git history, issues, PRs, and commits. Do not keep stale Markdown only to preserve old prose.

When status changes:

1. update this ledger only when the architecture-reset/release execution order changes;
2. update an ADR only when its durable decision/current verified architecture fact changes;
3. keep implementation evidence in the owning issue/PR and GitHub Checks;
4. delete or reduce any Markdown that would become a second architecture or status authority.

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

# Current verified state

## Completed foundation

The architecture-reset foundation through PLAN 07A is complete on current `main`.

Completed decisions and cutovers include:

- PLAN 00 — verified baseline and reset;
- PLAN 01 — CI/control-plane reset;
- PLAN 02 — backlog/stale-PR reconciliation;
- PLAN 03 / #225 — Typed Companion Capability RPC selected;
- PLAN 04 / #226 — ESP-SR full-duplex audio architecture selected;
- PLAN 05 / #227 — presentation/input contract selected;
- PLAN 06 / #228 — firmware runtime ownership rewrite completed and issue closed;
- PLAN 07A / #197 — desired/reported settings cut over to `device.settings_v1` on Capability RPC.

PR #242 merged as `877e7969302afcbf0e526a6002307108d75fae0b` and closed #197. The active Product-v1 protocol taxonomy no longer contains `config.update` or `config.report`.

Do not keep old PLAN 06/07 prose that describes legacy settings transport as still active.

## Open product work

### Capability hardening — REQUIRED before release

A fresh 2026-08-18 repository + Xiaozhi + Google ADK review found a real implementation gap after the original #225/#228 cutover.

The architecture decision remains correct. The implementation is still partly concrete:

- firmware capability dispatch is hard-coded by capability name;
- backend capability acceptance is a concrete allow-list;
- generic successful results are not checked against a server-owned result schema;
- `read` exists in protocol vocabulary but is not implemented end to end;
- `device.volume.set` is registered into the process-global `ToolRegistry`;
- current ADK tools are built from that registry during runtime construction.

This must not be solved by adding MCP, a second protocol, device-authored model metadata, `schema_hash`, or speculative manifest paging.

**Sole architecture contract:** [`../ADR-003-DEVICE-CAPABILITY-PLANE.md`](../ADR-003-DEVICE-CAPABILITY-PLANE.md)

Required implementation order:

1. backend `ContractCatalog` + generic result-schema validation;
2. bounded firmware `CapabilityRegistry` + generic lookup/owned execution;
3. session capability reconciliation through the catalog;
4. ADK dynamic `DeviceToolset` scoped by trusted turn/session device identity;
5. integrated isolation/reconnect/cancel/schema/Tier-1 tests.

Create or select one focused GitHub implementation owner before code mutation. Do not silently attach this work to unrelated release/provider PRs.

### PLAN 07B / #198 — wake configuration

**Status:** OPEN.

Issue #198 remains separate from the settings transport cutover.

Required outcome:

- expose only wake modes/models packaged by the exact firmware artifact;
- deterministic wake-disabled -> PTT fallback;
- safe ESP-SR model/threshold reconfiguration through the selected Audio owner;
- previous-good or disabled fallback on init failure;
- report actual active applied/rejected wake state;
- preserve desired/reported ordering/reconnect semantics;
- keep physical acoustic quality under #17.

The presence of a `wake_model` field is not proof that the active WakeNet model changed.

### PLAN 08 — provider/model/retrieval selections

PR #243 is **OPEN and unmerged**.

Treat its body, local test claims, and branch state as work-in-progress evidence only. Revalidate its exact head against current `main` after PR #242 before merge.

Do not close #105, #23, or #201 from PR prose alone. Close only from merged exact-head evidence and live issue acceptance.

### PLAN 09–12 / release qualification

PR #244 is **OPEN and unmerged**.

Its software/HIL/release claims are not final evidence while the PR is unmerged and upstream work remains open.

Issue #17 is still OPEN and owns real physical qualification for WakeNet/VAD/AEC/hands-free barge-in. Host tests, firmware compile, Tier-1, static memory budgets, or a prepared HIL runner do not satisfy #17.

---

# Required execution order from current main

```text
current main@877e7969
        │
        ▼
Capability hardening
ADR-003 implementation gap
        │
        ▼
PLAN 07B / #198
real wake choices + safe runtime apply
        │
        ▼
Revalidate PLAN 08 / PR #243
provider/model/retrieval selections
        │
        ▼
PLAN 09
fresh Product-v1 gap reconciliation
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

PR #244 must not bypass the open capability hardening, #198, revalidated PLAN 08, or real #17 evidence.

---

# Execution contract

Every implementation slice follows this sequence.

1. **Refresh live truth**
   - exact `main` SHA;
   - owning issue/PR;
   - blockers and dependencies;
   - current source paths and tests;
   - version-sensitive upstream APIs.

2. **Research only where needed**
   - use primary/official sources for version-sensitive technology;
   - use reference repositories as patterns, not authority.

3. **Review the smallest coherent change**
   - verify the goal still exists;
   - check simpler alternatives;
   - check security, privacy, lifecycle, recovery, data integrity, and rollback;
   - avoid speculative infrastructure.

4. **Implement one path**
   - no hidden fallback architecture;
   - preserve real external/public/persisted/security/user semantics;
   - delete replaced internal paths after proven cutover.

5. **Run the nearest oracle**
   - deterministic unit/host/contract test first;
   - boundary/Tier-1 when wiring crosses runtimes;
   - physical/provider evidence only when the claim requires it.

6. **Fresh self-review**
   - review the final integrated diff and failure/recovery behavior from a fresh context.

7. **Exact-head CI**
   - required checks must apply to the exact PR head.

8. **Merge and promote**
   - refresh head/base/mergeability/review threads immediately before merge;
   - merge exact expected head;
   - verify resulting main SHA;
   - run required exact-main Promotion Gate before advancing dependent work.

---

# Evidence rules

- A successful capability delivery is not proof of physical effect unless that effect is software-observable and validated.
- Requested settings are not applied settings.
- Firmware compile is not physical acoustic, RF, display, power, OTA, or enclosure evidence.
- Tier-1 software-device evidence is not physical HIL.
- Static SRAM/PSRAM/partition budgets are not dynamic runtime headroom.
- An open PR description is not merged repository truth.
- A stale branch's test result does not qualify a later main SHA.
- No fake RSSI, firmware version, OTA result, resource result, provider result, or physical result can be used as release evidence.

---

# Source-of-truth cleanup rule

Historical details remain recoverable from Git history, issues, PRs, and commits. Do not preserve stale Markdown only to keep old prose visible.

When a phase changes:

1. update this ledger;
2. update the owning ADR only if architecture changed;
3. update the owning issue/PR with implementation evidence;
4. delete or reduce superseded Markdown that would create a second status or architecture authority.

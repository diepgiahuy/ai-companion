# Modular Execution Plans Index

This directory contains standalone, token-efficient phase plans derived from the canonical architecture reset.

## Agent Context Rule

**DO NOT load all execution plans or the 32KB canonical roadmap into your context.**  
When assigned a specific task, locate the active phase below and load **only** that specific `PHASE_*.md` file.

## Phase Status Ledger

| Phase | File | Primary Owner | Status | Focus |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1** | [`PHASE_01_CI_DEV_SPEEDUP.md`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/plans/PHASE_01_CI_DEV_SPEEDUP.md) | Repo/CI | **COMPLETE** | Fast native PR test oracles, CodeQL deferred to Promotion |
| **Phase 2** | [`PHASE_02_FIRMWARE_RUNTIME_PLAN06.md`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/plans/PHASE_02_FIRMWARE_RUNTIME_PLAN06.md) | `#228` | **COMPLETE** | CardV1 ingress (DONE), InputRouter (DONE), A6 Recovery (DONE), Monolith cleanup & single path (DONE) |
| **Phase 3** | [`PHASE_03_SETTINGS_WAKE_PLAN07.md`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/plans/PHASE_03_SETTINGS_WAKE_PLAN07.md) | `#197`, `#198` | **COMPLETE** | Desired/reported twin over capability RPC, Wake config; unblocks final #228 A4 |
| **Phase 4** | [`PHASE_04_SELECTIONS_BENCHMARKS_PLAN08.md`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/plans/PHASE_04_SELECTIONS_BENCHMARKS_PLAN08.md) | `#105`, `#23`, `#201` | **OPEN / READY** | Real voice provider hard-cut, model selection, PostgreSQL retrieval audit |
| **Phase 5** | [`PHASE_05_RELEASE_GATES_HIL_PLAN09_12.md`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/plans/PHASE_05_RELEASE_GATES_HIL_PLAN09_12.md) | `#17`, Release | **PENDING** | Product-v1 gap audit, software promotion, physical HIL, release soak |

## State Update Protocol

When you complete a slice or phase:
1. Update the status and checklist in the corresponding `PHASE_*.md` file.
2. Update the status row in this index table.
3. Validate single-path and evidence invariants before opening the PR.

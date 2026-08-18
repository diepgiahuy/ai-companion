# Modular Execution Plans Index

This directory contains focused execution checkpoints derived from the canonical architecture reset. **Live GitHub issues, PRs and exact-head checks are authoritative.** These files must not promote work beyond the evidence actually proven on the referenced code.

## Agent Context Rule

Load only the phase needed for the current task, plus the canonical plan when an ordering/status decision depends on it. Re-read live GitHub before mutation.

## Phase Status Ledger

| Phase | File | Primary Owner | Status | Focus |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1** | `PHASE_01_CI_DEV_SPEEDUP.md` | Repo/CI | **COMPLETE** | Risk-aware PR oracles and exact-main promotion |
| **Phase 2** | `PHASE_02_FIRMWARE_RUNTIME_PLAN06.md` | `#228` | **IN PROGRESS** | Core runtime slices landed; #228 remains open until fresh full A1–A8 final review |
| **Phase 3 / 07A** | `PHASE_03_SETTINGS_WAKE_PLAN07.md` | `#197` | **IN REVIEW on #242** | Desired/reported twin over canonical capability RPC; exact-head acceptance pending |
| **Phase 3 / 07B** | `PHASE_03_SETTINGS_WAKE_PLAN07.md` | `#198` | **PENDING after #197** | Real packaged wake choices, safe Audio-owner reconfiguration/fallback, truthful active-config evidence |
| **Phase 4** | `PHASE_04_SELECTIONS_BENCHMARKS_PLAN08.md` | `#105`, `#23`, `#201` | **STACKED / MUST REVALIDATE** | Provider/model/retrieval evidence only after the settings foundation is accepted/rebased |
| **Phase 5** | `PHASE_05_RELEASE_GATES_HIL_PLAN09_12.md` | `#17`, Release | **PENDING** | Product-v1 gap audit, software promotion, physical HIL, release soak |

## Evidence boundaries

- `wake_model` schema/plumbing is **not** proof that the active ESP-SR WakeNet model changed; that remains PLAN 07B / #198.
- Firmware compile/host/Tier-1 software evidence is not physical acoustic/HIL/soak evidence.
- A desired write is not an applied device fact. Owner Hub must project requested/applied/rejected/stale/offline/unknown from authoritative state.
- Fake OTA/version/RSSI/resource success must never be used as release evidence.

## State Update Protocol

When a slice changes status:
1. refresh the exact live issue/PR/head first;
2. update the focused phase file and this index only to the level proven;
3. validate single-path/evidence invariants;
4. require the relevant exact-head CI before merge/promotion.

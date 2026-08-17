# AI Companion — Architecture Reset Execution Plans V2

**SOLE CANONICAL execution plan**  
**Repository:** `diepgiahuy/ai-companion`  
**Planning baseline:** `main@99a9977c75a2af18f1df82bf7d40867e92cdd503`  
**Current verified execution checkpoint:** `main@e4236cd3f0f623dfdb707dba778e6a34fdf91e2a`  
**Verified at:** 2026-08-17 19:31 Asia/Ho_Chi_Minh

**Canonical filename:** `AI_COMPANION_RESET_EXECUTION_PLANS_V2_CANONICAL_2026-08-17.md`

**Execution-unit count: 21**

Counting rule:
- PLAN 00–05 = 6 units;
- PLAN 06A–06E = 5 units;
- PLAN 07A–07B = 2 units;
- PLAN 08A–08D = 4 units;
- PLAN 09–12 = 4 units.

Total: **6 + 5 + 2 + 4 + 4 = 21 execution units**. `PLAN 06` is a grouping header for 06A–06E, not an extra execution unit.

> Older files named `AI_COMPANION_RESET_EXECUTION_PLANS_V2_2026-08-17.md`,
> `AI_COMPANION_RESET_EXECUTION_PLANS_V2_2026-08-17_UPDATED.md`,
> `(1)` copies, and `AI_COMPANION_RESET_MINI_PLANS_2026-08-17.md` are historical
> snapshots only. They have **zero execution authority**.

> **Execution source of truth:** live GitHub repository / PR / issue / review / check state.
> Before every mutation, refresh exact `main`, owning issue, blockers, source paths,
> tests, review threads and exact-head evidence. If this file conflicts with later
> live GitHub state, GitHub wins and this plan must be updated.

---

# Global execution contract

Every implementation slice follows the same sequence:

1. **Research**
   - current repository and live GitHub state first;
   - current primary/official external sources for version-sensitive technology;
   - reference projects only as patterns/candidates.

2. **Verify**
   - exact `main` SHA;
   - owning issue, blockers, parents/sub-issues and PR state;
   - actual source paths, tests, configuration and evidence;
   - version-sensitive assumptions.

3. **Review**
   - confirm the goal still exists;
   - classify material drift;
   - compare simpler alternatives;
   - recheck security, privacy, data integrity, lifecycle, recovery and evidence boundaries;
   - choose the smallest coherent change.

4. **Implement**
   - no hidden parallel architecture;
   - preserve real external/public/persisted/security/user semantics;
   - do not preserve obsolete prototype internals solely for compatibility;
   - delete replaced internal paths after proven cutover.

5. **Nearest oracle**
   - unit/host/contract oracle first;
   - boundary oracle when wiring changes;
   - physical/provider evidence only for claims that require it.

6. **Fresh self-review**
   - review final integrated diff and failure/recovery behavior from a fresh context.

7. **Exact-head CI**
   - merge only when the required evidence applies to the exact PR head.

8. **Merge + Promotion**
   - refresh head/base/mergeability/review threads immediately before merge;
   - merge with exact expected head;
   - verify resulting `main` SHA;
   - require broad `Promotion Gate` on that exact main SHA before advancing to the next dependent slice.

---

# Canonical plan map

```text
PLAN 00  Verified baseline
   ↓
PLAN 01  Development control-plane reset
   ↓
PLAN 02  Backlog / Project / stale-PR reconciliation
   ↓
 ┌──────────────┬──────────────┬──────────────┐
 │ PLAN 03      │ PLAN 04      │ PLAN 05      │
 │ Capability   │ Audio        │ Presentation │
 │ decision     │ decision     │ contract     │
 └──────┬───────┴──────┬───────┴──────┬───────┘
        └───────────────┼───────────────┘
                        ↓
                    PLAN 06
                 Firmware runtime
          ┌────────┬────────┬────────┬────────┐
          │ 06A    │ 06B    │ 06C    │ 06D    │
          │ UI     │ Audio   │ Cap.   │ I/O +  │
          │        │         │        │ Recover│
          └────────┴────────┴────────┴────────┘
                        ↓
                     06E
          non-settings single-path cleanup
                        ↓
          #228 CORE RUNTIME PRE-GATE
     A1/A2/A3/A5/A6/A7 + non-settings A8
                        ↓
                     07A
                settings desired/reported
                        ↓
        #228 FINAL A4 + remaining A8
                        ↓
                 close #228 if PASS
                        ↓
                     07B
                    wake config

02 ──→ 08A Agent runtime ownership — COMPLETE / no action
          │      ADK remains sole product agent runtime unless a new measured ADR replaces it
          ├──→ 08B #105 real-provider evidence → #106 provider hard-cut
          ├──→ 08C #23 model / embedding selection
          └──→ 08D #201 retrieval/memory gap audit → focused child only if proven

Foundations + selections
          ↓
09 Remaining Product-v1 gap reconciliation
          ↓
10 Software Promotion Gate
          ↓
11 Required Physical HIL
          ↓
12 Final integrated release + soak
```

---

# PLAN 00 — Verified Baseline Snapshot

**Status: COMPLETE**

Checkpoint:

`AI_COMPANION_PLAN_00_BASELINE_2026-08-17.md`

Baseline:

`main@99a9977c75a2af18f1df82bf7d40867e92cdd503`

Purpose achieved:

- exact starting SHA recorded;
- open PR/issues/control-plane state audited;
- merge rules/workflows/evidence boundaries reviewed;
- architecture assumptions requiring reset identified;
- no product mutation performed.

---

# PLAN 01 — Development Control-Plane Reset

**Status: COMPLETE**

Owner:

- Issue `#223`
- PR `#224` — `ci: reset risk-aware PR and promotion gates (#223)`

Exact PR head:

`43f94ab4550b0e7560f1da5678e11cfa2bf3404c`

Merge commit:

`b923246f78fb50a8a31ba2ca618d7c966f23c0ae`

Implemented:

- Draft/Ready no longer controls fast/full CI;
- deterministic event/path/risk classification selects nearest useful PR oracle;
- `PR Gate` remains the stable merge check;
- DB/protocol/Tier-1 oracles remain selected when the change crosses those boundaries;
- broad exact-SHA `Promotion Gate` runs on main/manual promotion;
- release requires successful `Promotion Gate` on exact source SHA;
- blocker clear returns work to Backlog/no execution label instead of auto-Ready;
- active-development compatibility rules protect real contracts, not obsolete internal architecture;
- trusted physical HIL remains outside ordinary PR CI.

Exit criteria satisfied; `#223` closed completed.

---

# PLAN 02 — Backlog, Project and Stale-PR Reconciliation

**Status: COMPLETE**

Resolved stale execution inputs:

- PR `#194` — closed without merge.
- PR `#222` — closed without merge.

Useful semantics retained from #222 for future PLAN 07A only:

- PostgreSQL-authoritative desired/reported state;
- owner isolation;
- monotonic versions;
- requested != applied;
- applied/rejected/stale/offline/unknown truth;
- restart/reconnect reconciliation.

Old `config.update/config.report` wiring is not architecture authority.

Focused architecture/runtime owners established:

- `#225` — device capability plane;
- `#226` — audio architecture;
- `#227` — presentation/input contract;
- `#228` — firmware runtime ownership rewrite.

Exit condition:

GitHub issues/PRs again represent current execution ownership rather than stale PR prose.

---

# PLAN 03 — Device Capability Plane Decision

**Status: COMPLETE**

Owner:

Issue `#225` — closed completed.

Selected architecture:

## Typed Companion Capability RPC

Canonical device-local capability family:

- `capability.advertise`
- `capability.call`
- `capability.result`
- `capability.cancel`

Fixed boundary:

- authenticated Companion session remains the device transport;
- firmware is **not** an MCP server/client;
- MCP remains a backend external-integration boundary;
- backend ToolRegistry/policy/owner/device authorization stays authoritative;
- no second permanent capability transport.

Durable architecture record:

PR `#236` — `docs(architecture): close PLAN03 capability decision gap (#225)`

Merged main:

`51af2b1f787adf8ad982095b8df3c7a58b36dab2`

Settings desired/reported migration is PLAN 07A / `#197`.

---

# PLAN 04 — ESP-SR Full-Duplex Audio Architecture Decision

**Status: COMPLETE — architecture decision**

Owner:

Issue `#226` — closed completed.

Selected software architecture:

- ESP-IDF `6.0.2`;
- ESP-SR `2.4.7`;
- ESP32-S3 `AFE_TYPE_FD`;
- `AFE_MODE_HIGH_PERF`;
- `MR` microphone + playback-reference topology;
- AEC + VAD + WakeNet;
- `vad_mute_playback=false`;
- `AFE_MEMORY_ALLOC_MORE_PSRAM`;
- one audio/frontend pipeline;
- playback reference comes only from PCM actually accepted by speaker TX/DMA;
- no new audio task unless measured evidence justifies it.

Implementation ownership moved to PLAN 06B / `#228`.

Physical acoustic/resource evidence remains `#17` / PLAN 11; compile/host evidence is not acoustic PASS.

---

# PLAN 05 — UX / Presentation Contract Decision

**Status: COMPLETE — architecture contract**

Owner:

Issue `#227` — closed completed.

Selected contract:

- one renderer-neutral `PresentationReducer`;
- one semantic `InputRouter`;
- deterministic authority-based P0..P7 priority;
- explicit global/session/generation staleness rules;
- bounded typed presentation events;
- backend `ui.state` is only a hint;
- local/domain truth cannot be overridden by backend UI hints;
- no arbitrary backend render JSON/code;
- renderer remains a local adapter;
- SSD1306 remains acceptable for the current slice; final graphics hardware stays separate.

Implementation ownership moved to PLAN 06A/06D / `#228`.

---

# PLAN 06 — Firmware Runtime Cutover

**Primary owner:** issue `#228`  
**Current live issue state:** OPEN / `status:in-progress`

Acceptance target:

- A1 explicit ownership;
- A2 callback safety;
- A3 audio integrity;
- A4 one capability boundary;
- A5 presentation boundary;
- A6 recovery/stale-event convergence;
- A7 exact software evidence;
- A8 obsolete-path deletion.

Do not close #228 until all applicable A1–A8 acceptance is freshly reviewed against current main.

---

## PLAN 06A — Presentation State Extraction

**Status: IN PROGRESS**

### Completed slice — PR #229

`feat(firmware): establish deterministic presentation reducer (#228)`

Delivered:

- fixed-size allocation-free reducer;
- P0..P7 domain priority;
- global/session/generation scopes;
- stale-event rejection;
- bounded UTF-8-safe text;
- deterministic host reducer tests.

### Completed slice — PR #230

`refactor(firmware): route runtime presentation through reducer (#228)`

Delivered:

- current display path routed through reducer;
- pairing/confirmation precedence moved into typed reducer semantics;
- one physical renderer sink;
- temporary `PresentationDisplay` migration adapter;
- raw backend `ui.card` deliberately left for a later typed cutover.

### Completed producer slice — PR #237

`refactor(presentation): version and bound semantic agent cards (#228)`

Exact PR head:

`ae3448afdb44feb0426450ff3c90e2490cf9eebb`

Merged main:

`e4236cd3f0f623dfdb707dba778e6a34fdf91e2a`

Backend `CardV1` contract:

- `version == 1`;
- `kind`: 1..32-byte ASCII token using alnum / `_` / `-` / `.`;
- `title`: max 96 bytes;
- `primary`: max 192 bytes;
- `secondary`: max 192 bytes;
- text valid UTF-8;
- control characters rejected;
- `progress`: 0..100;
- invalid optional cards are dropped without aborting voice output;
- card data contains no priority/action/script/URL/remote-asset authority.

### Current next slice — A5 physical firmware CardV1 ingress

Required path:

```text
backend CardV1 JSON
    ↓
strict firmware CardV1 decoder
    ↓
typed semantic presentation event
    ↓
PresentationReducer
    ↓
local renderer / SSD1306
```

Delete the old semantic collapse:

```text
ui.card
    ↓
read only `primary`
    ↓
BackendEvent::ui_card raw text
    ↓
display_.show(state_, event.text_view())
```

Implementation constraints:

1. Use exactly the existing backend CardV1 schema.
2. Strict `version == 1`.
3. Match backend byte bounds and type validation.
4. Malformed JSON fails closed.
5. Missing/wrong version fails closed.
6. Wrong JSON types fail closed.
7. Overlong fields fail closed.
8. Progress outside 0..100 fails closed.
9. Unknown syntactically valid kind may fall back to a generic semantic card only through the typed event/reducer.
10. Unknown kind never obtains renderer authority.
11. Raw backend JSON/text must not reach reducer/renderer.
12. `ui.state` must be mapped separately as a bounded presentation hint only; it must not be collapsed into `BackendEventType::ui_card`.
13. `agent.status` must also be mapped separately through a typed/bounded semantic status/activity path; it must not be collapsed into `BackendEventType::ui_card`.
14. Neither `ui.state` nor `agent.status` may override locally authoritative lifecycle/domain truth.
15. SSD1306 may display only title/primary in this slice.
16. Prefer a small host-testable parser/helper.
17. Do not rewrite the large `websocket_voice_backend.cpp` wholesale.
18. Remove/deprecate `BackendEvent::ui_card` raw-text semantics when the last caller is gone.
19. Do not retain permanent legacy + new presentation paths.

Nearest oracles:

- firmware CardV1 parser unit tests;
- malformed JSON;
- missing version;
- unsupported version;
- overlong fields;
- wrong types;
- progress 0/100 and out-of-range;
- unknown kind;
- reducer integration;
- protocol vector/golden test where useful;
- relevant host suite;
- exact-head ESP32-S3 firmware compile;
- exact-head `PR Gate`.

Merge rule:

- refresh main/head immediately before merge;
- mergeable;
- no unresolved review threads;
- exact-head required evidence PASS;
- merge exact head only;
- require broad `Promotion Gate` on resulting exact main before moving to A5b.

---

## PLAN 06B — Audio Ownership Cutover

**Status: CORE SOFTWARE CUTOVER IMPLEMENTED; final #228 A3/A6 integrated acceptance still pending**

Merged slices:

- PR `#231` — runtime playback/reference epoch ownership;
- PR `#232` — playback-reference conversion moved into runtime owner;
- PR `#233` — CompanionApp collapsed onto one `AudioEngine`;
- PR `#234` — repaired software-device compile oracle after Promotion caught a real regression.

Current software result:

- one app-facing AudioEngine owner;
- one capture/playback/frontend lifecycle;
- current-epoch TX-accepted PCM is the AEC-reference source;
- conversion and reference lifetime no longer split across CompanionApp;
- old three-port CompanionApp audio ownership removed;
- no duplicate audio runtime.

Not claimed:

- acoustic AEC effectiveness;
- real barge-in latency;
- final CPU/PSRAM headroom;
- physical audio quality.

Those remain #17 / PLAN 11.

---

## PLAN 06C — Device Capability Integration

**Status: CORE RECEIVE-OWNERSHIP CUTOVER IMPLEMENTED; final #228 A4 is NOT complete until PLAN 07A removes legacy settings transport**

Merged slice:

PR `#235` — `refactor(capability): unify firmware capability receive ownership (#228)`

Result:

- one WebSocket DATA/reassembly owner;
- duplicate confirmation callback path removed;
- pairing + capability traffic share one optional-control mux;
- `capability.call/cancel` consume the selected Companion Capability RPC plane;
- `device.user_confirmation` executes through the canonical plane;
- unsupported capability returns typed unsupported result;
- disconnect/reconnect clears stale confirmation and re-advertises live capabilities.

Remaining `config.update/config.report` migration belongs to PLAN 07A / `#197`.

Because #228 A4 requires the selected `capability.*` contract to be the **only**
Product-v1 device capability path, PR #235 alone is not sufficient to mark A4 PASS
while the legacy settings transport still exists. PLAN 06C therefore records the
core capability receive-ownership cutover as implemented, but **final A4 remains
pending PLAN 07A**.

---

## PLAN 06D — Transport / Input / Recovery Cutover

**Status: NEXT after A5 CardV1**

### A5b — InputRouter

Current split to remove:

- confirmation/pairing input ownership in `main/app_main.cpp`;
- PTT/voicemail input ownership in `CompanionApp`.

Target:

```text
physical button / gesture
    ↓
semantic InputIntent
    ↓
InputRouter
    ↓
authoritative runtime/domain owner
```

Required properties:

- preserve fresh-press suppression for destructive confirmation;
- presentation does not execute business mutations;
- repeated/cancel/ack/confirm inputs converge without duplicate mutation;
- do not combine with A5 CardV1 if that materially harms reviewability.

### A6 — Recovery / stale-event convergence

Reconnect/disconnect/error/cancel/drain/restart must not allow stale backend events, cards or audio to resurrect or roll back local authoritative state.

Required test classes:

- session/generation/epoch invalidation;
- stale card after cancel;
- stale TTS/audio after barge-in;
- reconnect during audio;
- duplicate/out-of-order backend events;
- drain/restart boundaries;
- failure during active media;
- no duplicate mutation from replayed control events.

Exit:

transport/input callbacks publish bounded intent/events and no longer directly mutate unrelated presentation/audio lifecycle.

---

## PLAN 06E — CompanionApp Retirement / Single-Path Cleanup

**Status: PENDING after 06D/A6**

A8 work:

- enumerate every remaining `CompanionApp` responsibility;
- move only genuinely owned orchestration;
- remove dead monolithic code;
- delete raw presentation path;
- delete obsolete callback/input ownership;
- delete compatibility selectors/fallbacks;
- remove temporary adapters when they no longer pay rent;
- specifically review whether `PresentationDisplay` can be removed;
- update single-path validator/evidence terminology;
- preserve only genuine external/public/persisted/security/user compatibility;
- maintain exactly one production runtime path.

Verification:

- dead-path search;
- single-path validation;
- host suite;
- firmware compile;
- selected Tier-1 scenarios;
- exact-head PR Gate;
- exact-main Promotion.

Exit:

#228 passes fresh A1–A8 acceptance review and can close.

---

# Required #228 / #197 execution order

The live contracts create one important dependency constraint:

- #228 A4 says `capability.*` must be the only Product-v1 device capability path.
- #197 is the owner that migrates/deletes legacy `config.update/config.report`.
- #225 explicitly says #197 is unblocked by the capability decision and that #197/#228
  converge/delete the parallel settings transport.

Therefore **do not require #228 to close before #197**; that would create a circular
acceptance dependency. Instead use a two-stage #228 gate:

```text
A5   strict CardV1 + separate ui.state / agent.status presentation ingress
 ↓
A5b  semantic InputRouter
 ↓
A6   recovery / stale-event convergence
 ↓
A8   non-settings runtime legacy deletion
 ↓
#228 CORE RUNTIME PRE-GATE
  - review A1/A2/A3/A5/A6/A7
  - review all non-settings A8 cleanup
  - keep A4 explicitly pending
 ↓
PLAN 07A / #197
  - desired/reported settings rebase
  - migrate config onto selected capability/state boundary
  - delete config.update/config.report
 ↓
FINAL #228 REVIEW
  - A4 must now pass
  - remaining settings-related A8 deletion must pass
  - recheck all A1–A8 for regression
 ↓
close #228 only with exact-head + exact-main Promotion evidence
 ↓
PLAN 07B / #198
```

This sequencing is a Master-V2 execution hold, not a fabricated GitHub blocker.

---

# PLAN 07A — Desired/Reported Settings Rebase

**Status: OPEN / NOT CURRENTLY CLAIMED; held by Master-V2 sequencing until the #228 core-runtime pre-gate**

Owner:

`#197`

Live GitHub truth at this checkpoint:

- issue is OPEN;
- it has **no `status:blocked` label**;
- native dependency summary reports `blocked_by: 0` (its historical dependency is already resolved);
- #225's selected capability decision explicitly unblocked it.

Do not describe #197 as GitHub-blocked. The only current hold is this plan's deliberate
sequencing rule: stabilize the non-settings runtime seams first, then execute #197 before
final #228 A4 closure.

Goal:

Preserve correct durable settings truth while rebasing device delivery onto the selected capability/state architecture.

Required semantics:

- PostgreSQL-authoritative desired/reported state;
- owner isolation;
- monotonic versions;
- requested != applied;
- applied/rejected/stale/offline/unknown;
- restart/reconnect reconciliation;
- truthful Owner Hub projection.

Implementation direction:

1. Re-read current main and #197 after the #228 core-runtime pre-gate; do **not** wait for #228 to close.
2. Salvage only still-valid domain/storage semantics/tests from closed #222.
3. Replace old `config.update/config.report` transport with selected capability/state plane.
4. Add reconnect/reboot reconciliation.
5. Keep auth/privacy/entitlement/secrets outside ordinary settings.
6. Delete obsolete settings transport.
7. Expose only authoritative device state.

Verification:

- real PostgreSQL integration;
- owner/auth/CSRF negatives;
- stale/duplicate/concurrent version tests;
- restart/reconnect;
- firmware apply/reject;
- selected-capability Tier-1 acknowledgement.

Exit:

#197 closes with one settings path and no legacy transport.

---

# PLAN 07B — Wake Configuration Rebase

**Status: GITHUB-BLOCKED by #197; execute only after PLAN 07A**

Goal:

Expose only actually packaged/supported wake modes/models/settings through the canonical settings path and selected ESP-SR audio owner.

Depends on:

- PLAN 04;
- PLAN 07A;
- current #198/#17 evidence contracts.

Implementation:

1. Discover support from actual firmware artifact/config.
2. Expose only supported choices.
3. Route desired config through PLAN 07A.
4. Apply/reinit safely inside Audio owner.
5. Report actual applied/rejected revision.
6. Wake-disabled must preserve deterministic PTT fallback.
7. Measure software resource impact.
8. Leave acoustic promotion to physical HIL.

Exit:

#198 software behavior complete without claiming physical wake/AEC quality.

---

# PLAN 08A — Agent Runtime Ownership Decision

**Status: COMPLETE / NO CURRENT EXECUTION**

Verified current boundary:

- issue `#15` is closed completed after the single-runtime cleanup;
- architecture epic `#21` states Google ADK remains the sole product agent runtime unless an explicit measured architecture decision replaces it;
- model issue `#23` and personal-assistant parent `#201` retain the same ADK + ToolRegistry/policy invariant.

Decision:

**Google ADK remains the sole Product-v1 agent runtime.**

Do not reopen an ADK-vs-other-runtime comparison merely because the old Master plan listed one.
A new runtime decision requires a new measured gap/ADR with current evidence; no permanent dual-agent runtime.

---

# PLAN 08B — Real Voice Provider Evidence and Selection

**Status: OPEN EVIDENCE LANE**

Live owners:

- parent `#18`;
- `#105` — real VN/EN/mixed provider evidence, OPEN;
- `#106` — provider selection/hard-cut, GITHUB-BLOCKED until #105 evidence.

Goal:

Produce comparable real-provider evidence through the canonical Companion path, then select/hard-cut one Product-v1 provider configuration.

Measure:

- ASR quality/latency;
- TTS latency/quality;
- realtime turn latency;
- cancel/barge-in/stale output;
- timeout/rate limit/auth/reconnect;
- exact provider/model/region/config/corpus/SHA/cost.

No dependency on PLAN 08A remains because the ADK runtime boundary is already settled.

---

# PLAN 08C — Model / Embedding Selection

**Status: OPEN SELECTION LANE — owner #23**

Goal:

Select one Product-v1 configuration per justified role using Companion-specific evidence.

Rules:

- Google ADK remains the sole agent runtime;
- tool correctness and false-mutation safety first;
- VN/EN/mixed;
- latency/reliability/deployment economics;
- embedding only if measured retrieval evidence justifies it;
- no silent non-selected fallback stack.

Exit:

one selected exact model/config per role or no candidate passes.

---

# PLAN 08D — Memory / Retrieval V2 Decision

**Status: PENDING GAP AUDIT — parent #201, no executable child assumed**

Goal:

Choose the smallest retrieval architecture that solves **measured** Product-v1 gaps.

Current parent rule:

- audit current PostgreSQL/domain retrieval first;
- create a focused child only after a real gap is demonstrated;
- do not add a generic vector database by default.

If a focused gap is proven:

1. Build cross-domain VN/EN/mixed retrieval evaluation.
2. Measure current deterministic domain/PostgreSQL retrieval.
3. Classify failures.
4. Fix simple deterministic gaps first.
5. Add embedding/vector search only if remaining measured gaps justify it.
6. Define correction/supersession/delete/privacy semantics.
7. Re-run evaluation.

Exit:

one deliberate measured retrieval strategy; no vector DB by default.

---

# PLAN 09 — Remaining Product-v1 Gap Reconciliation

**Status: PENDING after relevant foundations/selections**

Goal:

Recompute what Product-v1 genuinely still lacks without recreating merged features.

Audit areas:

- assets/personalization;
- personal assistant;
- degraded/proactive behavior;
- integrations;
- relationship voice;
- onboarding/pairing;
- OTA/security;
- display/hardware.

For each retained outcome classify:

- merged/proven;
- software-complete, higher-tier evidence pending;
- genuinely missing;
- conditional/deferred.

Create only focused children with their own:

- Goal;
- Research/Verify/Review;
- Implementation Plan;
- Acceptance/scenarios;
- Evidence/exit contract.

No feature-sweep PR.

---

# PLAN 10 — Software Promotion Gate

**Status: PENDING final candidate**

Goal:

Prove one canonical software configuration broadly without confusing software proof with physical HIL.

Candidate exact-SHA gate includes as required:

- Go/race/security;
- PostgreSQL migration/recovery;
- River only where active/changed;
- firmware compile/resource/static contracts;
- capability contracts;
- canonical Tier-1 software-device;
- CodeQL/security scans;
- artifact provenance.

Exit:

one exact candidate SHA with successful broad `Promotion Gate`.

---

# PLAN 11 — Physical HIL Qualification

**Status: PENDING trusted real hardware**

Goal:

Prove physical claims on the actual selected ESP32-S3 hardware/configuration.

Focused HIL owners may include:

- base DUT runner;
- audio Wake/VAD/AEC/barge-in;
- pairing/RF/two-DUT;
- onboarding/SoftAP/browser/power-loss;
- OTA A/B/rollback/power-loss;
- selected display/audio/network coexistence;
- encrypted NVS/eFuse manufacturing flow.

Rules:

- exact firmware/backend/hardware/config provenance;
- no lower-tier evidence substitution;
- irreversible eFuse/manufacturing actions require explicit human approval.

Exit:

every physical claim required for Product-v1 is proven or the corresponding capability remains unpromoted.

---

# PLAN 12 — Final Integrated Release Qualification and Soak

**Status: PENDING**

Goal:

Promote one canonical Product-v1 release only when software promotion, required physical evidence, real-provider/model evidence, recovery/security and soak all refer to the same configuration.

Implementation:

1. Freeze exact release candidate.
2. Run first-boot/claim.
3. Run representative voice expense/saving.
4. Run notes/journal/memory/retrieval.
5. Run reminder/timer recovery.
6. Run barge-in/cancel.
7. Run settings/wake.
8. Run shipped assets/pairing/voice-mail/OTA.
9. Run security negatives and fault/recovery scenarios.
10. Run representative soak.
11. Observe heap/PSRAM/watchdog/reset/audio underrun/reconnect/backend growth/DB health/River lag/stale generations/duplicate actions/display-audio coexistence.
12. Fresh independent release review.
13. Build/attest only from exact SHA with required successful Promotion + higher-tier evidence.
14. Durable docs advertise only promoted behavior.

Exit:

one exact release candidate, one canonical path, no fake telemetry, no lower-tier evidence promotion.

---

# Current execution checkpoint

## Live main

`e4236cd3f0f623dfdb707dba778e6a34fdf91e2a`

Latest merged architecture slice:

PR `#237` — bounded/versioned backend `CardV1` producer contract.

## Current active owner

Issue `#228`:

- OPEN
- `status:in-progress`
- no open PR currently overlaps the next slice

Verified current-main A5 legacy state:

- `BackendEventType::ui_card` still exists with a generic 96-byte text buffer;
- `CompanionApp` still handles `ui_card` by calling `display_.show(state_, event.text_view())`;
- firmware WebSocket ingress still collapses:
  - `ui.card` -> `ui.primary` -> `BackendEventType::ui_card`;
  - `ui.state` -> `emotion` -> `BackendEventType::ui_card`;
  - `agent.status` -> `state` -> `BackendEventType::ui_card`.

This is the exact physical firmware gap A5 must remove.

## Exact main promotion evidence

Exact main SHA:

`e4236cd3f0f623dfdb707dba778e6a34fdf91e2a`

Promotion:

- workflow: `ci`
- run number: `323`
- event: `push`
- workflow run: `32025542139`
- `Promotion Gate` check id: `95375137503`
- `Promotion Gate`: **SUCCESS**

Successful jobs on this exact main SHA:

- Evidence truth gate;
- Host component tests;
- Go backend quality;
- CodeQL Go;
- Protocol-v2 ESP32-S3 firmware compile;
- Tier-1 software-device orchestration;
- PostgreSQL / River integration job;
- Promotion Gate.

**River evidence caveat:** inside the PostgreSQL / River job on this SHA, the
River-sensitive `Prove River transaction and worker lifecycle` and
`Record River operational state` steps were **skipped** because this change was not
River-sensitive. Therefore run #323 is valid broad Promotion evidence for this main SHA,
but it must **not** be cited as fresh River worker-lifecycle proof.

## Open PR queue

**None** at this verification checkpoint.

## Immediate next executable work

**PLAN 06A / #228 A5 — strict physical firmware CardV1 presentation ingress, including
separate typed handling for `ui.card`, `ui.state`, and `agent.status`.**

PLAN 07A / #197 must wait for the **#228 core-runtime pre-gate**, not for #228 final
closure.

---

# Compact status ledger

```text
PLAN 00   COMPLETE
PLAN 01   COMPLETE
PLAN 02   COMPLETE
PLAN 03   COMPLETE — Typed Companion Capability RPC
PLAN 04   COMPLETE — ESP-SR 2.4.7 FD audio architecture
PLAN 05   COMPLETE — PresentationReducer + InputRouter contract

PLAN 06A  IN PROGRESS — A5 firmware CardV1 ingress is next
PLAN 06B  CORE IMPLEMENTED — final integrated A3/A6 acceptance pending
PLAN 06C  CORE IMPLEMENTED — final A4 pending PLAN 07A legacy-settings deletion
PLAN 06D  NEXT — InputRouter + recovery/stale-event
PLAN 06E  PENDING — non-settings single-path/monolith cleanup

PLAN 07A  OPEN / PLAN-HOLD until #228 core-runtime pre-gate; NOT GitHub-blocked
#228 FINAL A4/A8 REVIEW follows PLAN 07A
PLAN 07B  GITHUB-BLOCKED by #197

PLAN 08A  COMPLETE / NO ACTION — ADK remains sole product agent runtime
PLAN 08B  OPEN EVIDENCE LANE — #105 → #106
PLAN 08C  OPEN SELECTION LANE — #23
PLAN 08D  PENDING GAP AUDIT — #201 parent; no executable child assumed
PLAN 09   PENDING
PLAN 10   PENDING
PLAN 11   PENDING
PLAN 12   PENDING
```

---

# Authority / history rule

For normal execution, use only:

1. **this file** — `AI_COMPANION_RESET_EXECUTION_PLANS_V2_CANONICAL_2026-08-17.md`;
2. `AI_COMPANION_PLAN_00_BASELINE_2026-08-17.md` only for the immutable baseline snapshot;
3. live GitHub for current execution truth.

All older V2 / UPDATED / `(1)` / MINI plan files are forensic history only and have
**zero current status, architecture or execution authority**.

The tool surface available in this session cannot delete File Library objects, so old
artifacts may remain physically visible. Their continued presence must never be interpreted
as multiple active plans.

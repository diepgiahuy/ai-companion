# CP-SW2.4 — Temporal Correctness, Personal Data Queries & Notion-Style Web Dashboard

Date: 2026-08-16
Status: IN PROGRESS — Phase 1 PRs passing CI; Phase 2 & 3 in execution

---

## 1. Master Issue Index & Lineage (All 24 Issues)

| Issue # | Type / Area | Title | Phase | Current State |
|---|---|---|---|---|
| **#180** | Backend / Safety | Complete explicit destructive-tool confirmation path | Phase 1 | Handled in **PR #188** (All 8 CI jobs Green) |
| **#184** | Firmware / Concurrency | Synchronize shared Opus decoder across WebSocket TTS & Voice Mail | Phase 1 | Handled in **PR #189** (All checks Green) |
| **#185** | Firmware / OTA | Implement periodic background OTA manifest polling during runtime | Phase 1 | Handled in **PR #189** (All checks Green) |
| **#186** | Firmware / Stability | Implement graceful FreeRTOS task shutdown in backend destructor | Phase 1 | Handled in **PR #189** (All checks Green) |
| **#187** | Backend / Distributed | Move in-memory claim rate limiter & redemption cache to PostgreSQL | Phase 1 | Handled in **PR #189** (All checks Green) |
| **#190** | Backend / Context | Dynamic per-turn time and timezone injection in ADK & native resources | Phase 2 | Handled in **PR #193** (Completed) |
| **#191** | Backend / Query | Add date-range and search filtering for Notes and Voice Memos | Phase 2 | Handled in **PR #193** (Completed) |
| **#192** | Frontend / UI | Responsive Notion-Style Personal-Data & Device Management Dashboard | Phase 3 | Handled in **PR #193** (Completed) |
| **#170** | Firmware / Security | Encrypt persisted Wi-Fi and device credentials in NVS | Phase 4 | Queued for Phase 4 |
| **#105** | Backend / Voice | Benchmark real VN/EN ASR, TTS and native-realtime reference paths | Phase 5 | Queued for Phase 5 |
| **#106** | Backend / Voice | Select and hard-cut the Production v1 voice provider path | Phase 5 | Queued for Phase 5 |
| **#23** | Backend / Model | Benchmark and select the Production-v1 model/embedding stack | Phase 5 | Queued for Phase 5 |
| **#18** | Epic / Voice | Production v1 realtime voice provider qualification and selection | Phase 5 | Parent Epic |
| **#3** | HIL / Testing | Qualify the trusted physical ESP32-S3 test path | Phase 6 | Queued for Phase 6 |
| **#8** | Hardware / Firmware | Physically qualify and select Production-v1 board/display stack | Phase 6 | Queued for Phase 6 |
| **#17** | Audio / Testing | Physically qualify ESP-SR WakeNet, VAD, AEC and barge-in | Phase 6 | Queued for Phase 6 |
| **#100** | Pairing / Testing | Qualify proximity behavior and two-device HIL | Phase 6 | Queued for Phase 6 |
| **#104** | Onboarding / Testing | Qualify first boot, recovery, reset and ownership lifecycle | Phase 6 | Queued for Phase 6 |
| **#114** | OTA / Testing | Qualify A/B update, failed-health rollback and power-loss recovery | Phase 6 | Queued for Phase 6 |
| **#2** | Epic | Interactive Companion product experiences | Phase 6 | Parent Epic |
| **#7** | Epic | Secure proximity-confirmed device pairing | Phase 6 | Parent Epic |
| **#9** | Epic | Expressive display, haptic and LED UX on selected hardware | Phase 6 | Parent Epic |
| **#21** | Epic | Production v1 architecture and platform convergence | Phase 6 | Parent Epic |
| **#91** | Epic | Secure first-boot onboarding, owner claim and recovery | Phase 6 | Parent Epic |

---

## 2. Checkpoint State & Architecture Invariants

### Dynamic Time Context Architecture (Issue #190)
- **Invariant**: Prompt template caching must not be broken by appending a new fingerprint every second.
- **Implementation**: Use ADK `llmagent.Config.InstructionProvider` to dynamically render `Current time: <RFC3339> (<Timezone>)` from the per-turn context (`pipeline.CurrentTurn(ctx)`).
- **Native Resources**: Resolve calendar day/week/month boundaries using `pipeline.CurrentTurn(ctx).Timezone` with safe fallback to `n.location` / `UTC`.

### Query Engine (Issue #191)
- **Invariant**: User isolation must remain strictly enforced across all SQL queries in `pgstore`.
- **Implementation**: Expose optional `from`, `to`, `search` on `note.list` and `voice_memo.list`. Use parameterized SQL queries with case-insensitive `ILIKE` filtering.

### Notion/Xiaozhi UI Architecture (Issue #192)
- **Invariant**: Zero heavy framework/toolchain bloat in production Docker image.
- **Implementation**: Clean, lightweight, responsive HTML5/Vanilla CSS/JS dashboard embedded in `companiond` server, authenticated via `ownerauth` session cookies and CSRF tokens.

---

## 3. Resume & Verification Oracle

To resume and verify after any session interruption:
1. Verify backend quality and race detection:
   ```bash
   cd backend && go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
   ```
2. Verify host firmware test suite:
   ```bash
   cmake -B host/build host/ && cmake --build host/build && ctest --test-dir host/build --output-on-failure
   ```
3. Check evidence gate status:
   ```bash
   python3 scripts/check_evidence.py
   ```

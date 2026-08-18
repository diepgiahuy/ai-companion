# Phase 2: Firmware Runtime Cutover (PLAN 06)

**Status:** IN PROGRESS  
**Primary Owner:** Issue `#228`  
**Core Components:** [`components/companion_app/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/components/companion_app), [`components/esp32_network/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/components/esp32_network), [`host/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/host)

---

## 1. Goal

Decompose monolithic [`CompanionApp`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/components/companion_app) onto single, explicit domain owners (`AudioEngine`, `PresentationReducer`, `InputRouter`, Capability RPC) without dual legacy paths.

---

## 2. Invariants & Architecture Boundaries

1. **Audio:** Single `AudioEngine` owner; AEC playback reference comes only from DMA TX-accepted PCM.
2. **Presentation:** `PresentationReducer` is the sole display state reducer (P0..P7 authority). Backend `ui.state` and `agent.status` are presentation hints and cannot override local device truth.
3. **Card Ingress:** Strict `CardV1` parsing (`version == 1`, title <= 96B, primary <= 192B, secondary <= 192B, progress 0..100). Malformed JSON fails closed.
4. **Input:** Physical buttons/gestures map to semantic `InputIntent` -> `InputRouter`.
5. **Recovery:** Disconnect, cancel, or barge-in increments session epoch / media generation, immediately discarding stale backend audio/cards.

---

## 3. Slice Breakdown & Live Status

* [x] **06A Presentation Reducer Foundation:** PR `#229` & PR `#230` merged.
* [x] **06A Backend CardV1 Producer:** PR `#237` merged (`e4236cd`).
* [x] **06A Firmware CardV1 Ingress:** `40ccad1` merged (typed `PresentationCardV1`, `PresentationHint`, `AgentPresentationStatus` decoupled from raw text).
* [x] **06B Audio Ownership Cutover:** PR `#231`–`#234` merged (single `AudioEngine`).
* [x] **06C Capability Receive Ownership:** PR `#235` merged (RPC plane receive ownership).
* [x] **06D Semantic InputRouter (A5b):** `755bb8d` merged (`InputRouter` gesture dispatch).
* [x] **06D Recovery & Stale-Event Invalidation (A6):** DONE (Epoch/generation stale-event filtering on disconnect/cancel/reconnect).
* [x] **06E Monolith Cleanup & Single-Path (A8):** DONE (Removed `Button` interface & `SemanticButtonAdapter`, moved `Microphone`/`Speaker` ports to audio domain, consolidated capture activity on `AudioEngine`, single canonical path).
* [x] **#228 Core Runtime Pre-Gate Review:** Verified A1, A2, A3, A5, A6, A7 + non-settings A8. (A4 held for Phase 3 / PLAN 07A).

---

## 4. Verification Oracle

```bash
# Host component and deterministic reducer/router tests:
bash scripts/e2e.sh

# Single product runtime path validation:
python3 scripts/check_single_path.py
```

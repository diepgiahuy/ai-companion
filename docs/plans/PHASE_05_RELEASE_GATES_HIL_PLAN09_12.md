# Phase 5: Release Qualification, Promotion & Physical HIL (PLAN 09–12)

**Status:** PENDING  
**Primary Owners:** Issue `#17` (Physical HIL), Canonical Release Pipeline  
**Core Components:** [`tests/firmware/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/tests/firmware), [`host/companion_software_device/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/host/companion_software_device)

---

## 1. Goal

Execute final Product-v1 gap reconciliation, validate candidate software broadly via exact-SHA Promotion Gate, qualify real ESP32-S3 hardware via physical HIL, and run multi-hour release soak.

---

## 2. Invariants & Evidence Boundaries

1. **Promotion Evidence:** Promotion gate must succeed on the exact candidate commit SHA.
2. **Physical HIL:** Acoustic wake, SoftAP, eFuse, and physical flash partitions must be proven on actual DUT hardware (#17); host/software simulations cannot substitute for physical proof.
3. **No Fake Telemetry / Shims:** Shipped docs advertise only promoted and verified capabilities.

---

## 3. Slice Breakdown & Live Status

* [ ] **PLAN 09 Remaining Product-v1 Gap Audit:** Audit pairing, personal assistant, notes, savings goals, OTA, and hardware display gaps.
* [ ] **PLAN 10 Software Promotion Gate:** Run full broad promotion check suite on candidate SHA.
* [ ] **PLAN 11 Physical HIL Qualification (#17):**
  * Acoustic wake, VAD, AEC, barge-in on ESP32-S3 hardware.
  * SoftAP onboarding, Wi-Fi reconnection, power-loss resilience.
  * A/B OTA partition rollback.
* [ ] **PLAN 12 Final Integrated Release Soak:**
  * Multi-hour continuous soak (heap, PSRAM, audio underrun, River queue drain, DB connection pool health).
  * Independent release review and artifact sealing.

---

## 4. Verification Oracle

```bash
# Broad promotion gate & physical HIL runner:
pytest tests/firmware/ -v
```

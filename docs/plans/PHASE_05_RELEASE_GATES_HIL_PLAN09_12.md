# Phase 5: Release Qualification, Promotion & Physical HIL (PLAN 09–12)

**Status:** IN PROGRESS (Software Verification Passed; Physical Hardware HIL & Soak Pending)  
**Primary Owners:** Issue `#17` (Physical HIL), Canonical Release Pipeline  
**Core Components:** [`tests/firmware/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/tests/firmware), [`host/companion_software_device/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/host/companion_software_device)

---

## 1. Goal

Execute final Product-v1 software gap reconciliation, validate candidate software via exact-SHA local Promotion Gate, prepare physical ESP32-S3 HIL test suite (#17), and document release readiness.

---

## 2. Invariants & Evidence Boundaries

1. **Promotion Evidence:** Promotion gates must succeed on the exact candidate commit SHA.
2. **Physical HIL Boundary:** Acoustic wake, SoftAP, eFuse, physical flash partitions, and 24h soak require actual physical DUT hardware (#17); host/software simulations do not substitute for physical hardware proof.
3. **No Fake Telemetry / Shims:** Documentation must distinguish static design budgets from runtime measurements and advertise only verified capabilities.

---

## 3. Slice Breakdown & Live Status

* [x] **PLAN 09 Remaining Product-v1 Software Gap Audit:** COMPLETE (Pairing FSM, personal assistant domain tools, settings twin over `device.settings_v1`, OTA partition structure, and presentation displays verified in software/host integration).
* [x] **PLAN 10 Software Promotion Gate:** COMPLETE (Passed single-path check across 367 active files; 15 passed, 1 skipped for host C++ unit tests; all backend Go test packages passed with -race; E2E host integration passed).
* [ ] **PLAN 11 Physical HIL Qualification (#17):** IN PROGRESS / READY FOR RUNNER EXECUTION
  * Static partition bounds verified: Flash Partition end 0xFF0000 / 0x1000000 (99.6%) from `partitions.csv`.
  * Static design allocation budgets verified via `scripts/budget_check.py` (Internal SRAM design cap 160.5 KiB, PSRAM codec reserve 128.0 KiB). *Note: Physical runtime binary measurements (`idf.py size`) require ESP-IDF hardware build.*
  * Physical HIL test harness (`tests/firmware/test_hil.py`) prepared for dedicated physical runner execution.
* [ ] **PLAN 12 Final Integrated Release Soak & Sealing:** PENDING PHYSICAL HARDWARE RUN
  * Software gates sealed on local candidate SHA.
  * Real-device 24h soak, physical acoustic wake/barge-in, and real cloud provider gates remain tracked as `unproven`/`partial` in `evidence/status.json` until physical runner execution.

---

## 4. Verification Oracle

```bash
# Broad promotion gate & physical HIL runner:
pytest tests/firmware/ -v
```

# Firmware Architecture Review & Maintainability Audit

**Document:** `docs/FIRMWARE_ARCHITECTURE_REVIEW.md`  
**Evaluation Date:** 2026-08-20  
**Target Hardware:** Espressif ESP32-S3 (WROOM-1-N16R8 / 16MB Flash / 8MB Octal PSRAM)  
**Baseline Git SHA:** `233ec3078530012ffe9195156e2bb0c7c77f880c`

---

## 1. Executive Summary

The firmware architecture cleanly separates the hardware-independent application core (`components/companion_app/`) from hardware drivers and network transports (`components/esp32_board/`, `components/esp32_network/`). 

All 16 host C++ test suites compile and pass deterministically. Internal SRAM consumption is strictly bounded at 160.5 KiB (below the 200 KiB hardware design ceiling).

---

## 2. Component Structure & Responsibility Map

```
┌─────────────────────────────────────────────────────────────┐
│                       main/app_main.cpp                     │
│    (Boot Gestures, Provisioning, Lifecycle Composition)    │
└──────────────┬───────────────────────────────┬──────────────┘
               │                               │
               ▼                               ▼
┌──────────────────────────────┐ ┌──────────────────────────────┐
│   components/companion_app   │ │  components/esp32_network    │
│  (FSM, UI Reducer, Actions)  │ │ (WebSocket, Framing, Codec)  │
└──────────────┬───────────────┘ └──────────────┬───────────────┘
               │                                │
               ▼                                ▼
┌──────────────────────────────┐ ┌──────────────────────────────┐
│    components/esp32_board    │ │ components/esp32_provisioning│
│   (I2S, ESP-SR AFE, OLED)    │ │   (NVS Store, Setup Portal)  │
└──────────────────────────────┘ └──────────────────────────────┘
```

---

## 3. Subsystem Findings & Risk Assessment

### A. `components/esp32_network/src/websocket_voice_backend.cpp` (2,088 lines)

- **Ownership Concentration:** The file currently implements four distinct concerns:
  1. `OggOpusParser`: Ogg page framing, CRC32 checksum, and variable-length Opus packet extraction.
  2. `WebSocketFraming`: RFC6455 transport framing, masking, fragmentation, and ping/pong keepalive.
  3. `Protocol v2 Wire Parsing`: JSON-RPC 2.0 serialization for `session.turn.*` and `device.capabilities.*`.
  4. `Media Queue & Ring Buffers`: Outgoing microphone audio buffering and incoming speaker stream feeding.
- **Finding:** While the file is large, its internal boundaries are clearly delineated with well-defined static helper classes and static asserts.
- **Testing Coverage:** 
  - Host test `protocol_vectors` covers JSON-RPC 2.0 wire formats.
  - Host test `transport_policy_tests` covers reconnect and backoff.
  - Host test `capability_dispatch_tests` covers capability registration and execution.
- **Memory Risk:** `OggOpusParser` dynamically allocates the 65 KiB page buffer in `MALLOC_CAP_SPIRAM` (PSRAM), preserving internal SRAM.

### B. `main/app_main.cpp` (618 lines)

- **Role:** Composition Root for ESP-IDF.
- **Responsibilities:**
  - NVS Flash init and `ProvisioningStore` credential load.
  - Boot gesture detection (Hold 3s for Wi-Fi reprovisioning, Hold 8s for Factory Reset).
  - Setup portal fallback when Wi-Fi is unconfigured.
  - Hardware initialization (SSD1306 display, I2S microphone/speaker, ESP-SR AFE).
  - Main event loop task creation.
- **Finding:** Clean composition root; no business domain logic leaked into `app_main.cpp`.

### C. Audio Frontend & ESP-SR Runtime

- **Implementation:** `components/esp32_board/src/esp_sr_audio_frontend.cpp`
- **AFE Configuration:** High-performance full-duplex (`AFE_TYPE_FD`, `AFE_MODE_HIGH_PERF`) with AEC, VAD, and WakeNet enabled in PSRAM (`AFE_MEMORY_ALLOC_MORE_PSRAM`).
- **SRAM Safety:** Ring buffers and DMA buffers reside in dedicated Internal SRAM partitions audited by `scripts/budget_check.py`.

---

## 4. Maintenance Recommendation

- **Verdict:** **RETAIN WITHOUT BREAKING REFACTOR.**
- **Rationale:** The existing C++ firmware codebase passes all 16 host simulation tests and complies with the 160.5 KiB SRAM ceiling. A premature rewrite of `websocket_voice_backend.cpp` introduces unnecessary regression risk without providing product value. If refactored later, extraction should follow the pure internal module pattern (extracting `OggOpusParser` to a private header) with zero changes to wire contracts or memory layout.

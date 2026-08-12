# [Feature] AI Companion: Interactive Suite Upgrade (Tamagotchi 2.0, Voice Mail, Bump-to-Pair)

## 📖 Context & Motivation
The current AI Companion is a functional prototype but lacks emotional depth, privacy controls for messaging, and seamless pairing. This issue tracks the upgrade to a "Tamagotchi 2.0" experience. 

**Target Audience for this Issue:** An Autonomous AI Coding Agent. 
**Agent Instructions:** Do NOT hallucinate architectures. You MUST adhere strictly to the `docs/COMMERCIAL_ARCHITECTURE.md` patterns (Feature Modules, Device Twins). Follow the exact Constraints and Acceptance Criteria below.

---

## 🛠 Hardware Updates (BOM Changes)
The current hardware must be upgraded to support high-fidelity visual and physical feedback.

- **[REPLACE]** `SSD1306 OLED 128x32` ➡️ **High-Res TFT/IPS LCD (e.g., GC9A01 1.28" Round LCD or ST7789)**. Required for fluid, color animations (Squash & Stretch).
- **[ADD]** **Haptic Motor (Coin Vibration Motor) & Driver (e.g., DRV2605L)**. Required for physical feedback during remote interactions.
- **[ADD]** **RGB LED (WS2812B)**. For visual notifications (if not using onboard DevKitC-1 LED).
- **[CONFIG]** Enable **Bluetooth Low Energy (NimBLE)** in `sdkconfig.defaults`.

---

## 🏗 Features & Acceptance Criteria (AC)

### Feature 1: Visual Emotional Differentiation (Tamagotchi 2.0)
The device must distinguish between its own ambient mood and gestures sent by remote friends.

*   **Constraint 1.1:** Do NOT use static text/ASCII emojis.
*   **Constraint 1.2:** Use **LovyanGFX** (not TFT_eSPI) to leverage the ESP32-S3's dual-core DMA for tearing-free, high-FPS rendering.
*   **Constraint 1.3:** Animation logic must be 100% non-blocking. Never use `delay()`.
*   **AC 1.1 (Internal Emotion):** The LCD displays ambient states (Sleepy, Happy, Neutral) using slow "Easing" and "Squash and Stretch" geometry.
*   **AC 1.2 (Transferred Emotion):** When a WebSocket message `{"type": "gesture", "emotion_transfer": "pat"}` is received, the LCD immediately breaks the ambient rhythm, triggering a fast, asymmetric animation (Anticipation & Overlapping Action).
*   **AC 1.3 (Visual/Hardware Markers):** Transferred emotions MUST trigger the Haptic Motor, pulse the RGB LED, and display a temporary Sprite overlay (e.g., a bursting heart) on the LCD.

### Feature 2: Privacy-First Voice Mail
Voice messages from friends must not auto-play.

*   **Constraint 2.1:** Audio blobs must be stored in persistent storage (e.g., SQLite/Postgres on backend), NOT in memory.
*   **Constraint 2.2:** Must integrate via the `FeatureModule` manifest system.
*   **Constraint 2.3 (Privacy Policy):** Must respect the `save_voice_audio` flag from `COMMERCIAL_ARCHITECTURE.md`. If disabled, Voice Mails either cannot be received or must be hard-deleted immediately after first playback.
*   **AC 2.1 (Receiving):** When a message arrives, the device plays a brief chime and pulses the LED. Audio does NOT play.
*   **AC 2.2 (Queueing):** The backend maintains a FIFO queue of unread messages for the device. State mutations MUST generate a Transactional Outbox event.
*   **AC 2.3 (Playback):** The user MUST press a physical button (e.g., double-tap GPIO40) to fetch and play the next voice mail in the queue.

### Feature 3: Physical "Bump to Pair" (1-N Scaling)
Users can pair devices by physically bumping them together.

*   **Constraint 3.1:** The database must use a Many-to-Many `device_pairings` table (No hardcoded 1-1 columns).
*   **Constraint 3.2 (Algo):** Raw RSSI is too noisy. The firmware MUST apply a **Median Filter** followed by a **Kalman Filter** to smooth the RSSI data before evaluating proximity.
*   **AC 3.1 (Trigger):** When a user initiates pairing mode, the ESP32 broadcasts a NimBLE beacon at a high advertising interval (e.g., 50ms) for high-resolution RSSI sampling.
*   **AC 3.2 (Proximity):** If the ESP32 detects a matching beacon where the Kalman-filtered RSSI > -40dBm (indicating physical bump), it sends a pairing request to the Go Backend.
*   **AC 3.3 (Backend Validation):** The Go backend verifies the request, creates the relationship in `device_pairings`, and syncs the updated Friend List down to both devices via their Device Twins.

---

## 📁 Architectural Code Pointers for the AI Agent
When implementing, look at these specific files:

1.  **Backend Protocol:** `backend/internal/protocol/message.go`
    *   *Action:* Add `gesture` and `voice_mail` payload structs.
2.  **Backend Domain:** `backend/internal/domain/pairing.go`
    *   *Action:* Implement the M:N relational logic for the Friend List.
3.  **Firmware Display:** `components/esp32_board/include/companion/display/`
    *   *Action:* Replace OLED driver with `TFT_eSPI` or `LVGL` setup. Implement non-blocking animation tasks.
4.  **Firmware BLE:** `components/esp32_board/src/ble/` (New)
    *   *Action:* Implement NimBLE beacon scanning and advertising for Bump-to-Pair.

---

## 🧪 Testing & Verification Strategy (NO MOCKS)
The market standard for AI-driven development in 2026 strictly avoids mocked dependencies, as mocks often lead to false positives (tests pass, but production crashes). 

*   **Backend Testing (Ephemeral Environments):** 
    *   Do NOT use `gomock` or `testify/mock`. 
    *   Use **Testcontainers** to spin up a real ephemeral SQLite/Postgres database for the M:N `device_pairings` logic.
    *   Test the `voice_mail` FIFO queue against the real database schema.
*   **Firmware Verification (pytest-embedded & HIL):**
    *   Do NOT mock ESP-IDF APIs.
    *   **Phase 1 (Simulation):** Use **Wokwi Simulator CLI** in the CI pipeline for initial non-blocking LVGL/LovyanGFX validation.
    *   **Phase 2 (Physical HIL):** Use **pytest-embedded** connected to a **Local Mac M1 Self-Hosted Runner**. The ESP32 will be physically plugged into the Mac M1. The AI must write E2E tests in Python that automatically flash the binary via the Mac's USB port, inject WebSocket payloads, and assert serial console output.

---

## 🔄 AI Self-Healing Review Loop
To ensure code quality, the AI Agent picking up this issue MUST follow this Review Loop:
1.  **Code & Build:** Write the code and run `go build` / `idf.py build`.
2.  **Linting:** Pass strict static analysis (`golangci-lint run` and `clang-tidy`).
3.  **Real Execution (Integration):** Run the integration tests via Testcontainers/Wokwi.
4.  **Self-Correction:** If CI/tests fail, ingest the exact `stderr`/`stdout` logs, diagnose, and iterate. 
5.  **Stop for Human Check:** Do NOT request human review until the CI is green. Proceed to the HITL checkpoints below.

---

## 🙋‍♂️ Human-In-The-Loop (HITL) Checkpoints
The AI must STOP and wait for human action/approval at these specific gates:
1.  **Hardware Config Review:** After modifying `sdkconfig.defaults` and `BOM.md`, stop and ask the human to confirm the physical hardware changes are wired correctly before writing firmware logic.
2.  **Database Migration Review:** After generating the PR for the `device_pairings` M:N schema, request human approval before running the migration on the staging database.
3.  **Physical PR Verification (Self-Run Before Merge):** Before ANY firmware PR is merged, the AI must attach the compiled `.bin` artifact. The human must flash it (via WebSerial), run physical interaction tests, and comment "LGTM" on the PR. The AI cannot merge based solely on HIL CI success.

---

## 🔀 Execution Plan (PR Splitting Strategy)
To prevent merge conflicts and ensure separation of concerns, the AI MUST break this issue into sequential, non-overlapping Pull Requests (PRs):

*   **PR 1: Backend Protocol & DB (Domain Logic)**
    *   *Scope:* `device_pairings` schema, Go models, `voice_mail` FIFO queue, JSON payload definitions in `message.go`.
    *   *CS Principle:* **Separation of Concerns (SoC)**. The backend must have zero knowledge of how the ESP32 renders the data.
    *   *Architecture Policy:* All DB mutations for Pairing and Voice Mail MUST use the **Transactional Outbox** pattern (Section 8 of `COMMERCIAL_ARCHITECTURE.md`) to trigger WebSocket events.
*   **PR 2: Firmware Infrastructure (BLE & Storage)**
    *   *Scope:* NimBLE scanning/advertising, SPIFFS/NVS storage for downloading voice blobs, **Kalman Filter** for RSSI.
    *   *CS Principle:* **Interface Segregation Principle (ISP)**. Create clean, decoupled C++ interfaces for BLE and Storage.
*   **PR 3: Firmware UI & Audio (Presentation Layer)**
    *   *Scope:* `LovyanGFX` LCD animation tasks, I2S playback logic, physical button interrupts.
    *   *CS Principle:* **Single Responsibility Principle (SRP)**. The display task must only consume state from a thread-safe FreeRTOS queue; it must not handle network logic.

---

## 🚫 Anti-Patterns (Do NOT do this)
- Do NOT block the Audio/I2S FreeRTOS task while rendering LCD animations.
- Do NOT auto-play audio upon receipt.
- Do NOT hardcode pairing IDs in the firmware.
- Do NOT bypass the `COMMERCIAL_ARCHITECTURE.md` patterns. All state must flow through the Device Twin.
- Do NOT write tests using mocked databases or mocked hardware interfaces.

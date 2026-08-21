# Test Coverage & Evidence Hardening Report

**Repository:** `diepgiahuy/ai-companion`  
**Evaluation Date:** 2026-08-20  
**Baseline Git SHA:** `233ec3078530012ffe9195156e2bb0c7c77f880c`  
**CI Gate Status:** 100% Green on `main` (`32380115781`)

---

## 1. Existing Automated Proof

### A. Go Backend Test Suites (45 Packages)

All backend unit and race tests execute with `-tags "adk,mcp,nolibopusfile" -race -count=1`:

| Subsystem / Domain | Package | Statement Coverage | Test Files | Status |
| :--- | :--- | :--- | :--- | :--- |
| **Composition Root** | `cmd/companiond` | 32.9% | 8 files | **PASS** |
| **Capability Core** | `internal/capability` | 62.1% | 4 files | **PASS** |
| **Device Capabilities** | `internal/devicecap` | 77.8% | 4 files | **PASS** |
| **Policy & Authorization** | `internal/policy` | 84.8% | 2 files | **PASS** |
| **ADK Agent Bridge** | `internal/adkbridge` | 54.9% | 5 files | **PASS** |
| **Context & Conversation** | `internal/contextengine`, `conversation` | 89.9% / 69.4% | 3 files | **PASS** |
| **Event Bus & Idempotency** | `internal/events`, `idempotency` | 93.8% / 76.3% | 2 files | **PASS** |
| **Observability & Telemetry** | `internal/observability` | 83.8% | 1 file | **PASS** |
| **Onboarding & Auth** | `internal/onboarding`, `ownerauth` | 69.2% / 70.3% | 7 files | **PASS** |
| **Owner Hub (Web UI)** | `internal/ownerweb` | 54.5% | 6 files | **PASS** |
| **Presentation Engine** | `internal/presentation` | 96.7% | 2 files | **PASS** |
| **Privacy & Retention** | `internal/privacy` | 81.0% | 2 files | **PASS** |
| **Protocol v2 Framing** | `internal/protocol` | 71.4% | 5 files | **PASS** |
| **Runtime Supervision** | `internal/supervision` | 86.0% | 1 file | **PASS** |
| **Server Routing & Guard** | `internal/server` | 56.6% | 19 files | **PASS** |
| **Weather Integration** | `internal/weather` | 55.2% | 1 file | **PASS** |
| **Prompt Engine** | `prompts` | 83.3% | 1 file | **PASS** |
| **Cross-Domain Retrieval** | `eval/personal_retrieval` | 100% (57/57 cases) | 1 file | **PASS** |
| **PostgreSQL Integration** | `internal/pgstore` | Multi-scenario suite | 18 files | **PASS (CI)** |

### B. Firmware Host Simulation Suites (16 Suites)

Host C++ tests run natively with Clang/GCC/Ninja without requiring an ESP32 device:

1. `companion_tests`: Core state machine, timers, alarms, gestures, session transitions (**PASS**)
2. `audio_frontend_tests`: Portable audio frontend interfaces, VAD framing (**PASS**)
3. `audio_runtime_tests`: Ring buffer audio pipeline, overrun/underflow behavior (**PASS**)
4. `audio_frontend_app_tests`: Turn lifecycle integration with audio events (**PASS**)
5. `capability_dispatch_tests`: Firmware capability registry, command execution, result echoing (**PASS**)
6. `pairing_fsm_tests`: BLE proximity pairing finite state machine (**PASS**)
7. `press_gesture_tests`: Single click, double click, long press gesture router (**PASS**)
8. `input_router_tests`: Button input routing to presentation and conversation FSM (**PASS**)
9. `presentation_reducer_tests`: UI view state reduction, priority override, timer/alarm display (**PASS**)
10. `presentation_display_tests`: SSD1306 framebuffer rendering simulation (**PASS**)
11. `presentation_ingress_tests`: Backend turn & capability presentation ingress (**PASS**)
12. `transport_policy_tests`: WebSocket connection policy, backoff calculation (**PASS**)
13. `reconnect_backoff_tests`: Exponential backoff with jitter and max attempt bounds (**PASS**)
14. `provisioning_fsm_tests`: BLE Wi-Fi provisioning state machine (**PASS**)
15. `protocol_vectors`: Protocol v2 JSON-RPC wire serialization/deserialization vectors (**PASS**)
16. `opus_probe`: Native Opus codec header/stream probe (**PASS/Skipped when hardware Opus absent**)

### C. Policy & Single-Path Gates (42 Python Tests)

- `test_ci_scope.py`: Dynamic CI path classification policy (19 tests) (**PASS**)
- `test_check_evidence.py`: Evidence promotion validation rules (9 tests) (**PASS**)
- `test_pairing_rssi_analyze.py`: Proximity RSSI windowing and false-pair rejection (7 tests) (**PASS**)
- `test_github_issue_status.py`: Issue DAG reconciliation and status consistency (7 tests) (**PASS**)
- `scripts/check_single_path.py`: Scanned 394 files; 0 legacy/parallel runtime markers (**PASS**)
- `scripts/budget_check.py`: Verified 160.5 KiB internal SRAM cap & 99.6% partition end (**PASS**)

---

## 2. Missing Proof & Gaps

| Evidence Class | Gate Name | Current State | Missing Element |
| :--- | :--- | :--- | :--- |
| **Real Provider Voice** | `real_voice_e2e` | `unproven` | End-to-end voice conversation with live ASR + LLM + TTS |
| **Real ASR Quality** | `real_asr_quality` | `unproven` | Empirical WER/CER benchmark on VN/EN audio corpus |
| **Real TTS Quality** | `real_tts_quality` | `unproven` | Time-to-first-audio (TTFB) streaming latency benchmark |
| **Real LLM Quality** | `real_llm_tool_quality` | `unproven` | Real model tool selection, parameter accuracy & refusal |
| **Prompt Red-Teaming** | `prompt_regression_eval` | `unproven` | Automated prompt injection / safety regression benchmark |
| **Physical Audio HIL** | `mic_signal_hardware` | `passed` (signal only) | WakeNet false wake, AEC double-talk, barge-in on enclosure |
| **Hardware Soak** | `real_device_24h_soak` | `unproven` | Continuous 24h+ WiFi/session stability on ESP32-S3 |
| **Wokwi Tier-2** | `wokwi_tier2` | `blocked` | Automated headless simulator run (needs secret token) |

---

## 3. Recommended Next Evidence

1. **AI Benchmark Execution (`backend/cmd/eval`):** Execute OpenAI/Gemini/Deepgram reference runs to produce structured JSON evidence reports in `evidence/reports/` for closing #105 and #23.
2. **NVS Secure Storage Verification (#170):** Test encrypted NVS boot/read/write on a provisioned test DUT.
3. **Physical Acoustic Qualification (#17):** Capture calibrated speaker/microphone recordings on the physical HIL fixture to measure AEC attenuation (ERLE > 20 dB) and hands-free barge-in success rate (> 90%).

# Test evidence ladder

This document classifies what each Companion test layer can and cannot prove. A green lower tier never promotes a claim owned by a higher tier.

## Tier 0 — deterministic host and contract

C++ `CompanionApp` tests, Go unit/race/store tests, protocol-v2 golden vectors and deterministic validators prove pure logic, serialization and bounded state-machine behavior. They do not prove a real network, provider, device or physical peripheral.

## Tier 1 — headless software device

`host/companion_software_device` reuses the production C++ `CompanionApp` and `wire_protocol` and replaces only physical ports. Its host-only WebSocket adapter connects to the real Go `companiond` `/v2/device` endpoint and uses system libopus for the same 16 kHz uplink / 24 kHz downlink Opus contract.

The first dependency baseline is intentionally host-only and pinned by the test container: Boost.Beast/Boost.System 1.74 from Debian bookworm, libopus 1.3.1, and nlohmann-json 3.11.2. These packages never enter the ESP-IDF component graph.

Current mandatory scenarios are session/turn/TTS, duplicate live-session message identity, barge-in generation cancellation, disconnect/reconnect with a new session, live config/report ordering, and deterministic protocol-v1 rejection. Synthetic microphone PCM and bounded speaker/display sinks require no host audio hardware.

A Tier-1 run with `MockASR`, `MockAgent` and `MockTTS` is **orchestration evidence only**. Its artifact must say `evidence_class=tier1_orchestration` and `promotion=orchestration_only`; it cannot promote real ASR/TTS/model quality. #18 must plug real providers into this same device FSM/protocol harness rather than create another client.

## Tier 2 — targeted Wokwi firmware simulation

Re-verified against official Wokwi documentation on 2026-08-13: ESP32-S3 is supported, Wokwi CI can run from GitHub Actions, serial expectations and automation scenarios are supported, and CI requires `WOKWI_CLI_TOKEN`. Published monthly simulation limits are currently 50 minutes Free, 200 minutes Hobby/Hobby+, and 2000 minutes Pro. These facts are time-sensitive and must be checked again before enabling a quota-consuming workflow.

No Wokwi PASS exists in this repository yet. The current product composition uses physical I2S/audio/display paths that must not be silently replaced just to manufacture a green simulator result. The first targeted Wokwi PR must either prove a minimal real-firmware boot/network/protocol scenario using a clearly simulation-only board adapter where necessary, or emit `UNAVAILABLE` with the exact token/capability blocker. Wokwi never promotes I2S acoustic, BLE/RSSI/RF, final display timing, current, thermal or enclosure claims.

Official sources used for this checkpoint: Wokwi ESP32 simulation guide, Wokwi CI getting-started/GitHub Actions documentation, and Wokwi CLI documentation.

## Tier 3 — trusted physical HIL

`.github/workflows/firmware_hil.yml` / issue #3 is the only promotion path for physical ESP32 audio, RF/BLE, final display/touch/haptic timing, power/current/thermal and enclosure evidence. It must continue to run only trusted refs with explicit operator/device selection; public PR code never auto-runs on attached hardware.

## Evidence identity

Every machine-readable scenario artifact must include the tested commit, scenario id, result class, backend-config fingerprint, provider identities, timing/counters, protocol version and promotion class. Mock/simulator evidence is never relabeled as production-real evidence. Git rollback removes the harness without changing product runtime behavior.

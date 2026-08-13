# Test Evidence Ladder

This document defines what each non-production test layer is allowed to prove. It is intentionally stricter than a generic test pyramid: every layer has explicit evidence boundaries so software simulation cannot promote hardware/provider claims.

## Tier 0 — deterministic host and contract tests

Runs on normal GitHub-hosted runners.

- C++ `companion_app` FSM/unit tests.
- Go unit/race/integration tests.
- Cross-runtime protocol-v2 golden vectors.
- Schema/validator/property/fuzz tests where applicable.

May prove pure logic, protocol serialization/validation, state-machine invariants, queue bounds and deterministic failure handling.

Does not prove a real device, real network stack, real speech provider, acoustic behavior, RF, display timing or power.

## Tier 1 — headless Companion software device

Target: a host executable that reuses the production C++ `CompanionApp` and `wire_protocol` implementation while replacing only physical ports with deterministic fixture/sink adapters. It connects to the real Go backend over the canonical WebSocket `/v2/device` protocol.

Planned physical-port replacements:

- recorded PCM/WAV microphone source;
- decoded/captured speaker sink;
- display/UI event recorder;
- scripted button/barge-in input;
- host-only WebSocket/Opus backend adapter.

This tier may prove end-to-end application/session behavior such as protocol negotiation, reconnect, control/audio ordering, cancellation/barge-in semantics, configuration updates, backend agent/tool execution and durable state behavior exercised by the selected backend configuration.

When deterministic/mock ASR/TTS/model providers are used, their quality is not promoted. When #18 later supplies real providers, the same software device can drive recorded audio through them and record provider evidence without claiming microphone/AEC quality.

## Tier 2 — simulated ESP32-S3 firmware

Primary candidate: Wokwi CI using ESP-IDF 6.0.2 (`idf.py wokwi` / Wokwi CLI) on the real ESP32-S3 firmware build.

Use it only for capabilities the simulator actually models, for example:

- application boot;
- flash/partition/config startup behavior represented by the simulator;
- Wi-Fi connection;
- TCP/WebSocket protocol handshake and selected reconnect/control scenarios;
- GPIO/button/SPI/I2C behavior where the modeled part/peripheral exists;
- serial fail/pass assertions.

Current Wokwi ESP32-S3 limits relevant to this project: I2S is not implemented and Bluetooth is not implemented. Final ST7789/GC9A01/ESP-VoCat QSPI display behavior and physical performance are not assumed to be modeled. Therefore this tier cannot promote microphone/speaker/AEC, BLE/RF, display frame-time/tearing, or power gates.

Wokwi CI requires a token and has plan-based simulation-time quotas. A missing token/entitlement is `unavailable`, never `passed`. Public-gateway tests use synthetic data only.

### Optional ESP-IDF QEMU

ESP-IDF 6.0.2 documents ESP32-S3 QEMU with CPU/memory/several-peripheral emulation, a virtual framebuffer, and eFuse/security emulation. Use it selectively when it gives stronger evidence than Tier 0/2 (for example secure-boot/eFuse or framebuffer logic). Do not make QEMU a mandatory duplicate simulator without a concrete test it uniquely improves.

## Tier 3 — physical HIL

Trusted-ref physical ESP32-S3/selected-board testing remains mandatory for claims involving:

- I2S microphone/speaker and codec timing;
- ESP-SR AFE/WakeNet/AEC and acoustic echo behavior;
- Bluetooth/RSSI/RF coexistence;
- actual display panel/touch/haptic/LED timing and frame-time/tearing;
- power/current/thermal behavior;
- enclosure/acoustic behavior;
- real flash/boot/recovery behavior when simulation cannot represent the failure mode.

## Truth rule

A higher-fidelity test may support a lower-layer claim, but a lower tier cannot promote a higher-tier claim. `evidence/status.json` must record the evidence kind, commit, workflow/run and test environment before a gate becomes `passed`.

# Xiaozhi architectural and testing reference

The project borrows some useful ideas from Xiaozhi, especially WebSocket control and raw-Opus binary media, but **does not implement Xiaozhi wire compatibility**. Backend and firmware communicate only through the canonical Companion protocol v2 documented in [`ADR-002`](ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md).

| Capability | Xiaozhi reference | Companion current path |
|---|---|---|
| WebSocket control | JSON messages | Typed `Envelope` version 2 |
| Audio media | WebSocket binary Opus | WebSocket binary raw Opus |
| Session startup | hello exchange | `session.hello` / `session.ready` |
| Turn control | listen / abort | `turn.listen` / `turn.abort` |
| Speech lifecycle | STT / TTS messages | `transcript.final` / `tts.lifecycle` |
| Device auth | transport-level identity/auth | Companion credential/policy boundary |
| MQTT + UDP | available | not implemented |
| MCP device tools | available | backend capability boundary only |
| AEC / full duplex | board-dependent | future hardware/audio gate |

The current audio profile retains 60 ms Opus uplink framing, 16 kHz mono uplink and 24 kHz mono downlink. Sharing an audio profile does not make the JSON protocol compatible. A Xiaozhi client or former Companion v1 client cannot connect without implementing Companion protocol v2; this repository intentionally contains no compatibility adapter.

## Hardware-free testing lessons

Xiaozhi provides two useful testing patterns that Companion adopts without copying its implementation:

1. **Firmware host/build validation.** `78/xiaozhi-esp32` contributor guidance runs host-side Python build/unit tests and explicitly separates successful builds from physical hardware validation. This is equivalent to Companion Tier 0: deterministic host tests, protocol vectors, schema checks and representative firmware builds prove logic/build contracts only.
2. **Software device at the wire seam.** `py-xiaozhi` demonstrates that a desktop client can speak the same server protocol as a hardware device. Its normal voice setup still documents microphone/speaker or virtual-audio requirements, so Companion does not depend on desktop audio hardware for CI.

Companion's selected approach is stronger for this repository: the Tier-1 software device reuses the **production C++ `CompanionApp` and `wire_protocol`** instead of reimplementing the device FSM in Python or Go. Only physical ports are replaced by recorded-audio fixtures, scripted input and output/event sinks, while the client connects to the real Go backend over `/v2/device`.

The full evidence hierarchy is defined in [`TEST_EVIDENCE_LADDER.md`](TEST_EVIDENCE_LADDER.md):

```text
Tier 0 deterministic host / cross-runtime contracts
  -> Tier 1 C++ software device against real Go backend
  -> Tier 2 targeted ESP32-S3 simulation for supported boot/network behavior
  -> Tier 3 trusted physical HIL for audio/RF/display/power claims
```

Simulation never promotes physical microphone/speaker, AEC, Bluetooth/RF, final display performance or power evidence.

## ESP-IDF 6

Current Xiaozhi mainline contributor guidance uses ESP-IDF 6.0.2 as the preferred baseline and reserves older IDF lines for explicitly documented legacy boards. Companion likewise uses one ESP-IDF 6.0.2 firmware baseline and does not maintain a second IDF 5 compatibility toolchain.

## Primary references

- <https://github.com/78/xiaozhi-esp32/blob/main/AGENTS.md>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/application.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/audio/audio_service.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/esp-idf-6-migration.md>
- <https://github.com/78/xiaozhi-esp32-python>
- <https://components.espressif.com/component/espressif/esp_websocket_client>

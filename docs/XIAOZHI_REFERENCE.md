# Xiaozhi architectural reference

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

Primary references used for architectural comparison:

- <https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/application.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/audio/audio_service.cc>
- <https://components.espressif.com/component/espressif/esp_websocket_client>

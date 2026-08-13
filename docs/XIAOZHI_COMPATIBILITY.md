# Xiaozhi architectural cross-check

The project reuses Xiaozhi's WebSocket and raw-Opus transport ideas but does not
claim wire compatibility. Backend and firmware communicate through the canonical
Companion protocol v2 documented in
[`ADR-002`](ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md).

| Capability | Xiaozhi reference | Companion v2 |
|---|---|---|
| WebSocket control | JSON messages | Typed `Envelope` version 2 |
| Audio media | WebSocket binary Opus | WebSocket binary raw Opus |
| Session startup | hello exchange | `session.hello` / `session.ready` |
| Turn control | listen / abort | `turn.listen` / `turn.abort` |
| Speech lifecycle | STT / TTS messages | `transcript.final` / `tts.lifecycle` |
| Device auth headers | authorization + device identity | same transport-level concept |
| MQTT + UDP | available | not implemented |
| MCP device tools | available | backend capability boundary only |
| AEC / full duplex | board-dependent | planned; current path is half-duplex |

The audio profile remains 60 ms Opus frames, 16 kHz mono uplink and 24 kHz mono
downlink. That shared profile does not make the JSON wire formats compatible. A
Xiaozhi client or a former Companion v1 client cannot connect without an explicit
protocol-v2 implementation; this repository intentionally contains no adapter.

Primary references used for the architectural comparison:

- <https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/application.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/audio/audio_service.cc>
- <https://components.espressif.com/component/espressif/esp_websocket_client>

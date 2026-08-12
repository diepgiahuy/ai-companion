# Xiaozhi cross-check

Checked against the official `78/xiaozhi-esp32` WebSocket documentation and its
application/audio lifecycle. “Match” means the POC has the same lifecycle
concept; custom companion messages are extensions between this firmware and its
Go backend.

| Capability | Official Xiaozhi | This POC | Result |
|---|---|---|---|
| WebSocket JSON control | yes | yes | match |
| Binary streaming audio | yes | yes | match |
| `hello` negotiation | yes | version, transport, audio params | subset |
| Device auth headers | Authorization, protocol/device/client IDs | same header names | match |
| Listen start/stop | yes | button start; `manual` or `auto_vad` mode; Smart VAD can auto-stop | compatible subset |
| STT and TTS lifecycle | `stt`; TTS start/sentence/stop | same control names | match |
| Abort/barge-in | clears old send/decode/playback | clears bounded queues and restarts capture | match |
| Bounded audio queues | yes | ordered upload 16; playback 24 | match |
| Audio codec | raw Opus in WebSocket binary messages | raw Opus | **same** |
| Frame profile | 60 ms, input 16 kHz/output 24 kHz | 60 ms, input 16 kHz/output 24 kHz | **same** |
| MQTT+UDP transport | supported | no | omitted |
| Offline wake word | ESP-SR on supported boards | not yet; button still starts the turn | planned |
| Smart VAD | board/audio-service dependent | basic local energy VAD for automatic end-of-speech | custom subset |
| MCP device tools | supported | backend business tools only | omitted |
| AEC/full duplex | board/codec dependent | half-duplex + button interruption | planned |
| Proactive alarm/schedule | implementation-dependent | custom `alarm` / `schedule` JSON downlink | custom extension |
| Wi-Fi provisioning | board-specific flows | Kconfig bench credentials | pending |

## Compatibility statement

The current ESP32 and Go backend form one protocol pair. Voice control and audio
implement the Xiaozhi-style WebSocket Opus lifecycle. The companion-specific
`alarm` and `schedule` messages are deliberately additive. MCP, hands-free wake
word, AEC/full-duplex and OTA workflow are not advertised as complete.

Basic Smart VAD does **not** mean always-listening: the button currently opens a
turn and VAD closes it after speech/silence. WakeNet is the planned trigger for a
future buttonless path, using the same `VoiceBackend` turn lifecycle.

## Primary references

- https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md
- https://github.com/78/xiaozhi-esp32/blob/main/main/application.cc
- https://github.com/78/xiaozhi-esp32/blob/main/main/audio/audio_service.cc
- https://components.espressif.com/component/espressif/esp_websocket_client

# Xiaozhi architectural and testing reference

This document records **patterns** Companion learned from Xiaozhi; it is not a compatibility target or a live feature matrix for either project. Companion backend/firmware communicate only through the canonical Companion Protocol v2.

## Pattern comparison

| Concern | Reference pattern | Companion durable choice |
|---|---|---|
| Realtime device transport | JSON control + binary Opus over WebSocket is a useful reference pattern | secure WebSocket + typed Protocol-v2 control + binary Opus |
| Session/turn lifecycle | explicit hello/listen/abort/speech lifecycle | `session.hello` / `session.ready`, canonical turn/generation cancellation |
| Device auth | transport/device identity boundary | database-enrolled unique revocable device credential + backend-owned identity claims |
| Additional MQTT/UDP transport | exists in the reference ecosystem | **not a Companion Production-v1 path** |
| Device-side MCP tools | exists in the reference ecosystem | **not used**; Companion uses backend ToolRegistry + Protocol-v2 device capabilities |
| External MCP/integrations | backend/cloud integration pattern | backend-side official MCP Go SDK behind ToolRegistry/policy |
| AEC / wake / full-duplex behavior | board/acoustic dependent | ESP-SR AFE/WakeNet/VAD/AEC **software path implemented**; enclosure/self-trigger/false-interruption quality remains physical #17 evidence |
| Broad board support | reference project supports many boards | Companion intentionally keeps a narrow selected Production-v1 hardware path |

Sharing transport/audio concepts does not imply wire compatibility. A Xiaozhi client or former Companion-v1 client cannot connect without implementing Companion Protocol v2; this repository intentionally maintains no compatibility adapter.

## Testing lessons retained

Two useful ideas remain:

1. **Separate build/host proof from physical proof.** Firmware build/unit success cannot prove microphone, speaker, BLE/RF, display timing, current draw or enclosure acoustics.
2. **Test the real wire/app state machine without hardware.** Companion Tier 1 reuses the production C++ `CompanionApp` and `wire_protocol` against the real Go backend while replacing only physical ports with fixtures/sinks.

Companion's evidence ladder is:

```text
Tier 0 — deterministic host / cross-runtime contracts
  -> Tier 1 — production C++ software device against real Go backend
  -> Tier 2 — targeted simulation only where supported behavior is represented
  -> Tier 3 — trusted physical HIL for audio/RF/display/power/OTA/peripheral claims
```

The classification contract is [`TEST_EVIDENCE_LADDER.md`](TEST_EVIDENCE_LADDER.md).

## Companion-specific non-copy decisions

Companion deliberately does **not** copy broad reference-project scope when it adds complexity without a product requirement:

- no permanent MQTT/UDP second business transport;
- no MCP runtime on ESP32;
- no unrestricted GPIO/shell/filesystem tool surface;
- no multi-board framework for Production v1 before hardware/product need exists;
- no physical/provider PASS inferred from examples or simulator success;
- no provider-native runtime that bypasses Companion session, ToolRegistry, privacy or durable state.

External/reference projects can inform a Spike or ADR, but merged Companion code and measured Companion evidence decide the product architecture.

## Primary references

Historical/reference links retained for architectural research:

- <https://github.com/78/xiaozhi-esp32>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md>
- <https://github.com/78/xiaozhi-esp32-python>
- <https://components.espressif.com/component/espressif/esp_websocket_client>

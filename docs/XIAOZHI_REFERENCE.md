# Xiaozhi reference index

This file is **research reference only**. It has no Companion architecture or execution authority.

For device capability architecture, use [`ADR-003-DEVICE-CAPABILITY-PLANE.md`](ADR-003-DEVICE-CAPABILITY-PLANE.md). ADR-003 contains the verified Xiaozhi capability patterns that Companion keeps and the patterns it rejects.

For Protocol-v2 interaction semantics, use [`ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md`](ADR-002-INTERACTION-PROTOCOL-CONTRACTS.md).

For test evidence classification, use [`TEST_EVIDENCE_LADDER.md`](TEST_EVIDENCE_LADDER.md).

## Primary upstream references

- <https://github.com/78/xiaozhi-esp32>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/mcp_server.h>
- <https://github.com/78/xiaozhi-esp32/blob/main/main/mcp_server.cc>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/mcp-protocol.md>
- <https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md>

## Rule

Use Xiaozhi as a pattern source, not a compatibility target.

Do not copy a Xiaozhi protocol, transport, MCP lifecycle, tool surface, payload limit, or device-side model authority into Companion without a separate evidence-backed decision.

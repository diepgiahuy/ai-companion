# Historical repository audit

> **Historical snapshot only.** This file originally captured the transition from the uploaded POC toward the modular Companion architecture. Its former “current state” sections referenced SQLite product state, mock voice composition and pre-ESP-SR/pre-Protocol-v2 implementation and are no longer valid as current repository truth.

## What remains useful from the audit

The durable architectural lessons still apply:

- keep ESP32 hardware/realtime concerns small and explicit;
- keep durable/business state in the Go backend behind typed ports;
- use bounded audio/control queues with explicit cancellation semantics;
- separate firmware application state from board/peripheral adapters;
- use one canonical Companion device protocol rather than provider- or reference-project compatibility paths;
- treat Xiaozhi and other projects as architectural references, not source-of-truth for Companion behavior;
- use simulation/host tests only for the properties they actually represent; physical claims require real hardware.

## Current sources

For current repository behavior use:

- [`../README.md`](../README.md) — durable product front door;
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — implementation architecture;
- [`COMMERCIAL_ARCHITECTURE.md`](COMMERCIAL_ARCHITECTURE.md) — stable evolution boundaries;
- [`../evidence/status.json`](../evidence/status.json) — promoted evidence claims;
- GitHub Issues/PRs — remaining/live work;
- `main` — merged product truth.

The detailed original audit remains available through Git history for forensic comparison. Do not use this file to infer the current database, provider, audio, protocol, or branch/PR state.

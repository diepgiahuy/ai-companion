# Voice-mail device UX and verification

This document is the human-readable contract for issue #6. It separates the
implemented product path from evidence that still requires a simulator token or
physical hardware.

## Product flow

1. `voice_mail.available` adds one bounded metadata item to the application
   queue. Delivery is deduplicated by `voice_mail_id`; media never starts here.
2. A notification moves the UI to `voice_mail_waiting`. Only an explicit input
   gesture can request the next item.
3. The backend creates a unique playback/idempotency identity, claims one item,
   and accepts only an authenticated same-origin media reference.
4. Metadata, expiry, type, size, Ogg page CRC, Opus structure, byte count, and
   SHA-256 are validated before firmware exposes decoded audio to the speaker.
5. Download and decode run outside `CompanionApp::tick`. The app continues to
   poll input, display, and backend events while bounded queues feed playback.
6. Success is reported only when the decoder is finished, local and backend
   audio queues are empty, and the speaker reports drained.
7. Ephemeral success removes the item. Retained success returns it to waiting.
   Cancel, timeout, decode, output, and disconnect failures never report success.

After `session.ready`, the server replays at most four currently unread items.
This gives a new process or reboot an authoritative recovery path. A live
notification racing with recovery is harmless because the app deduplicates IDs.

## Firmware buffering decision

The ESP32 adapter uses fixed FreeRTOS queues and a dedicated media task. It does
not assume SPIFFS. Ogg pages use PSRAM when available, with a bounded internal
RAM fallback. Firmware currently performs one authenticated streaming pass to
validate the complete object, then a second authenticated pass to decode it.
This trades bandwidth for the stronger rule that unverified audio is never sent
to the speaker and avoids buffering a complete message in RAM or flash.

The compile/size gate proves target API compatibility and static image size. It
does not prove runtime heap headroom, network throughput, I2S output, or the
selected board's PSRAM behavior. Those measurements remain Tier 3 evidence.

## Automated checkpoints

| Checkpoint | Required oracle | What it proves |
|---|---|---|
| C0 contract | protocol vectors and Go unit tests | Strict protocol-v2 payload and lifecycle behavior |
| C1 app FSM | deterministic host C++ tests | No autoplay, dedupe, one claim, drain ordering, expiry, cancel, retained retry, and timeout |
| C2 authoritative store | PostgreSQL integration test | Ownership, leases, idempotency, policy, outbox, expiry, and cleanup |
| C3 software device | `software-device-e2e` | Real C++ app plus Go server, cold-start recovery, reconnect duplicate, media validation, cancel/reclaim, timeout, and successful logical playback |
| C4 firmware compile | `protocol_v2` ESP32-S3 job | Pinned ESP-IDF 6.0.2 compile, link, and `idf.py size` |
| C5 review | exact-head CI plus diff review | No unresolved automated regression or review finding before merge |

Do not rerun a weaker checkpoint when a green stronger oracle already covers the
same unchanged code. After a failure, fix the smallest responsible layer and
rerun that layer before returning to the failed checkpoint.

## Human-only gates

### Tier 2 Wokwi

Run only after C0-C5 are green and `WOKWI_CLI_TOKEN` is configured on the trusted
workflow. Use the repository firmware without replacing the product audio path.
Record the commit, workflow run ID, simulator scenario, serial assertions, and
whether simulation actually ran. A Wokwi pass may claim boot/network/control
behavior only; it must not claim I2S or acoustic playback.

### Tier 3 trusted HIL

Use the trusted runner procedure in `docs/HIL_RUNNER.md` on the selected ESP32-S3
board and actual input/speaker hardware.

1. Record commit SHA, ESP-IDF version, board revision, PSRAM size, wiring, and
   stable serial identity.
2. Provision least-privilege test devices and create one synthetic Ogg Opus voice
   mail without placing credentials or media URLs in logs.
3. Verify the waiting indication appears and no speaker output starts before the
   physical button/input action.
4. Press once and verify exactly one claim, audible output through the real I2S
   path, responsive input/display during playback, and completion only after the
   amplifier path drains.
5. Repeat with disconnect, reset during claim/playback, explicit cancel, corrupt
   media, and retained policy. Verify no failed case is incorrectly consumed and
   unread recovery occurs after lease/reconnect semantics allow it.
6. Capture JUnit/serial evidence, backend lifecycle IDs with secrets redacted,
   minimum free heap/PSRAM, download/decode timing, and operator notes.

Until these steps pass, the release report must say `Tier 3 pending` and must not
claim physical button, speaker, heap, RF, power, thermal, or enclosure proof.

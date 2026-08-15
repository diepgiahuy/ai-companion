# Audio front end — ESP-SR software path and physical qualification

This document describes the merged audio-front-end architecture and the remaining **physical acoustic evidence boundary**. It is not a roadmap claiming that unrun hardware tests passed.

## Merged software architecture

The firmware has one portable audio-front-end boundary used by `CompanionApp`, with the ESP32-S3 concrete path implemented using ESP-SR AFE/WakeNet/VAD/AEC integration.

```text
microphone PCM ----------> AudioFrontend ----------> CompanionApp turn path
                                ^   |
                                |   +--> speech / end-of-speech / wake events
                                |
real speaker PCM reference ----+        (AEC reference)

CompanionApp ----------> speaker port ----------> physical output
```

Important invariants:

- ESP-SR/vendor types remain outside application/business state logic.
- Wake starts the same canonical turn/session path as button input; there is no second WebSocket/audio runtime.
- Hands-free barge-in cancels through the same generation-scoped backend/session path used by deterministic button interruption.
- Playback-reference samples come from the real speaker PCM path, not from a boolean/config claim.
- Queues/buffers remain bounded and cancellation/reset drains stale generation state.
- The physical button remains a deterministic user/recovery input even when hands-free behavior is enabled.

The current device audio profile keeps the existing Companion Opus/runtime boundaries; format/rate conversion needed by the AFE/reference path remains in the audio adapter rather than the application FSM.

## What software tests can prove

Host/component/Tier-1 tests may prove:

- application state transitions for wake/listen/end/cancel;
- bounded buffering and reset behavior;
- playback-reference under/overrun handling;
- generation cancellation/stale-output suppression;
- reuse of the canonical turn/session path;
- compilation/integration against the selected ESP-SR/ESP-IDF configuration.

They do **not** prove acoustic echo cancellation quality, false-wake rate, microphone/speaker geometry, enclosure coupling, real RF coexistence or sustained physical resource behavior.

## Physical qualification gate

Issue #17 owns physical qualification of the already-merged software path. Promotion requires trusted Tier-3 evidence on the intended board/enclosure.

At minimum measure and record:

- wake-to-listen latency;
- false wakes in quiet/common background conditions;
- false interruptions while normal TTS plays;
- end-of-speech behavior;
- user speech detection during TTS and resulting barge-in latency;
- proof that stale queued TTS does not continue after interruption;
- CPU, internal heap/PSRAM and watchdog behavior;
- at least a representative sustained audio + Wi-Fi coexistence soak;
- tested firmware/backend SHA, board/enclosure, speaker volume, ESP-SR/model/config and test setup.

Normal TTS must not repeatedly self-trigger the microphone/wake path at supported volume/enclosure settings.

Physical layout still matters: microphone/speaker placement, mechanical isolation and enclosure acoustics are part of the measured system. Software AEC is not a substitute for poor acoustic design.

## Evidence status

| Item | Durable state |
|---|---|
| Button-started canonical turn path | implemented/tested software path |
| AudioFrontend application boundary | implemented |
| ESP-SR AFE integration | implemented |
| WakeNet/VAD integration | implemented software path |
| Real playback-reference routing for AEC | implemented software path |
| Generation-scoped hands-free barge-in orchestration | implemented software/Tier-1 path |
| Physical wake false-accept/reject quality | **unproven until #17 HIL** |
| Physical AEC/self-trigger quality | **unproven until #17 HIL** |
| Final enclosure/resource/coexistence soak | **unproven until #17 / selected hardware evidence** |

Do not regress the wording back to “ESP-SR not implemented” merely because physical quality remains unproven; implementation evidence and physical evidence are separate facts.

## Primary references

- Espressif ESP-SR AEC: <https://docs.espressif.com/projects/esp-sr/en/latest/esp32s3/acoustic_echo_cancellation/README.html>
- Espressif ESP-SR AFE: <https://docs.espressif.com/projects/esp-sr/en/latest/esp32s3/audio_front_end/README.html>

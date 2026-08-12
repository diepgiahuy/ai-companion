# Audio front-end roadmap: Smart VAD, WakeNet and AEC

This document is the contract for the next hands-free audio milestone. It exists
so `README.md` can list AEC/wake-word honestly without treating them as a flag
that is already working.

## Current audio mode

- The button still starts a turn. This is the reliable fallback and remains supported.
- A basic local energy VAD now detects speech and automatically stops capture after
  configurable trailing silence (`vad_mean_abs_threshold`, `vad_silence_ms`,
  `vad_min_speech_ms`).
- TTS playback is half-duplex from the microphone point of view. Button barge-in
  cancels TTS; hands-free voice barge-in is not yet accepted.

## Why AEC needs a different input topology

ESP-SR's Audio Front-End (AFE) combines AEC, noise suppression, VAD and WakeNet.
For AEC, the front-end must receive both microphone samples (`M`) and a playback
reference (`R`) that represents what is being sent to the speaker. The channels
are interleaved PCM. AEC therefore cannot be made real by enabling a boolean in
`CompanionApp` while only the INMP441 signal is available.

The current POC has:

- microphone path: 16 kHz mono PCM -> uplink Opus;
- TTS path: 24 kHz mono Opus -> PCM -> MAX98357A.

A future AEC path must therefore also feed a time-aligned speaker reference to the
AFE at its required sample format. With the current 24 kHz downlink, either add a
bounded 24 -> 16 kHz reference resampler or negotiate a compatible playback
reference rate. Keep that conversion outside the application/business state
machine.

## Target component boundary

```text
INMP441 16 kHz -----> AudioFrontend -----> CompanionApp microphone port
                           ^   |
                           |   +--> VAD state / WakeNet state
                           |
TTS playback reference ---+   (AEC reference, time aligned)

CompanionApp -----> Speaker port -----> MAX98357A
```

Recommended firmware API shape:

```cpp
struct AudioFrontendResult {
  size_t samples;
  bool speech_started;
  bool speech_ended;
  bool wake_word_detected;
};

class AudioFrontend {
public:
  virtual ~AudioFrontend() = default;
  virtual bool start() = 0;
  virtual AudioFrontendResult process(
      std::span<const int16_t> mic,
      std::span<const int16_t> playback_reference,
      std::span<int16_t> cleaned_output) = 0;
  virtual void reset() = 0;
};
```

Do not leak ESP-SR types into `companion_app`; put the concrete adapter in
`esp32_board` (or a new `esp32_audio_frontend` component) and inject the portable
interface.

## Wake word flow

1. In `ready/idle`, keep only the low-cost audio front-end/wake detector active.
2. WakeNet detects the configured local wake phrase.
3. Cancel idle animation, start a normal backend turn through the same
   `VoiceBackend::begin_turn(...)` API, and transition to `listening`.
4. Use AFE VAD to decide end-of-speech.
5. Keep the physical button as a deterministic fallback and provisioning/recovery
   control.

Do **not** implement wake word by opening a second independent WebSocket/audio
pipeline; that would duplicate cancellation, queues and turn IDs.

## Hands-free barge-in acceptance gate

Voice barge-in while TTS is playing is only considered complete when all of the
following pass on the actual enclosure:

- playback reference is fed to AEC;
- the device does not self-trigger on its own TTS at normal speaker volume;
- user speech while TTS plays is detected reliably;
- false interruption rate is measured in a quiet room and with common background noise;
- interruption stops queued TTS and begins the new turn without stale audio;
- heap/PSRAM and CPU remain within budget for at least a 30-minute soak test.

Physical design still matters: keep mic and speaker mechanically separated and
use foam/silicone isolation where practical. Software AEC should reduce echo; it
should not be used to excuse a poor acoustic enclosure.

## Status

| Item | Status |
|---|---:|
| Button-started capture | ✅ |
| Basic energy Smart VAD / auto end | ✅ host-tested, 🟡 hardware tuning |
| ESP-SR AFE adapter | 🔴 |
| Noise suppression through AFE | 🔴 |
| WakeNet | 🔴 |
| Playback-reference resampler/routing | 🔴 |
| AEC | 🔴 ⚠️ hardware validation |
| Hands-free voice barge-in | 🔴 ⚠️ depends on AEC + VAD/WakeNet |

## Primary references

- Espressif ESP-SR AEC: https://docs.espressif.com/projects/esp-sr/en/latest/esp32s3/acoustic_echo_cancellation/README.html
- Espressif ESP-SR AFE: https://docs.espressif.com/projects/esp-sr/en/latest/esp32s3/audio_front_end/README.html

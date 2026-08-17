#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <span>

namespace companion {

enum class AudioFrontendEvent : uint8_t {
  none,
  wake_detected,
  speech_started,
  speech_ended,
};

struct AudioFrontendResult {
  size_t samples{};
  AudioFrontendEvent event{AudioFrontendEvent::none};
};

struct PlaybackReferenceStats {
  uint64_t epoch{};
  bool active{};
  uint64_t pushed_samples{};
  uint64_t underflow_events{};
  uint64_t underflow_samples{};
  uint64_t overruns{};
};

// Application-facing audio front-end contract. Vendor SDK types must remain in
// board/audio adapters. The frontend consumes 16 kHz microphone PCM and the
// time-aligned speaker reference used for echo cancellation, and emits cleaned
// 16 kHz PCM plus wake/VAD events into the canonical Companion turn flow.
//
// The reference source is the PCM actually accepted by speaker TX. Its source
// sample-rate travels with the accepted PCM so the owning audio runtime, not
// CompanionApp, can perform the bounded conversion into the AFE's 16 kHz domain.
// Playback-reference lifetime is explicit: zero reference while no epoch is
// active is normal idle behavior and must not be reported as an AEC underflow.
class AudioFrontend {
public:
  virtual ~AudioFrontend() = default;
  virtual bool start() = 0;
  virtual void reset() = 0;
  virtual bool begin_playback_reference(uint64_t epoch) = 0;
  virtual void end_playback_reference(uint64_t epoch) = 0;
  virtual bool push_playback_reference(std::span<const int16_t> accepted_pcm,
                                       uint32_t sample_rate_hz) = 0;
  virtual PlaybackReferenceStats playback_reference_stats() const = 0;
  virtual AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                              std::span<int16_t> cleaned_16k) = 0;
};

// Bounded streaming converter for the current 24 kHz TTS playback path feeding
// a 16 kHz AEC reference. It produces exactly two output samples per complete
// three-sample input group, carries at most two samples between calls and uses
// linear interpolation for the half-sample phase. Physical AEC promotion still
// requires enclosure measurements; this class makes rate/timing behavior
// deterministic and independently testable.
class PlaybackReference24To16 final {
public:
  size_t convert(std::span<const int16_t> input_24k,
                 std::span<int16_t> output_16k) {
    size_t written = 0;
    for (const int16_t sample : input_24k) {
      pending_[pending_count_++] = sample;
      if (pending_count_ != pending_.size()) {
        continue;
      }
      if (written + 2 > output_16k.size()) {
        pending_count_ = 0;
        ++dropped_groups_;
        continue;
      }
      output_16k[written++] = pending_[0];
      const int32_t mixed = static_cast<int32_t>(pending_[1]) +
                            static_cast<int32_t>(pending_[2]);
      output_16k[written++] = static_cast<int16_t>(mixed / 2);
      pending_count_ = 0;
    }
    return written;
  }

  // Reset only streaming phase at an epoch boundary. Lifetime diagnostics are
  // deliberately retained across playback epochs so HIL/soak evidence cannot
  // be erased by normal cancel/drain transitions.
  void reset() { pending_count_ = 0; }
  void clear_metrics() { dropped_groups_ = 0; }

  size_t pending_samples() const { return pending_count_; }
  uint64_t dropped_groups() const { return dropped_groups_; }

private:
  std::array<int16_t, 3> pending_{};
  size_t pending_count_{};
  uint64_t dropped_groups_{};
};

} // namespace companion

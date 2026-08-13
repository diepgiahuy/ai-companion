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

// Application-facing audio front-end contract. Vendor SDK types must remain in
// board/audio adapters. The frontend consumes 16 kHz microphone PCM and the
// time-aligned speaker reference used for echo cancellation, and emits cleaned
// 16 kHz PCM plus wake/VAD events into the canonical CompanionApp turn flow.
class AudioFrontend {
public:
  virtual ~AudioFrontend() = default;
  virtual bool start() = 0;
  virtual void reset() = 0;
  virtual bool push_playback_reference(std::span<const int16_t> pcm_16k) = 0;
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

  void reset() {
    pending_count_ = 0;
    dropped_groups_ = 0;
  }

  size_t pending_samples() const { return pending_count_; }
  uint64_t dropped_groups() const { return dropped_groups_; }

private:
  std::array<int16_t, 3> pending_{};
  size_t pending_count_{};
  uint64_t dropped_groups_{};
};

} // namespace companion

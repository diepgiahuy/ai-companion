#pragma once

#include "companion/audio_frontend.hpp"

#include <cstddef>
#include <cstdint>
#include <span>

namespace companion {

struct EspSrAudioFrontendConfig {
  float wake_threshold{0.60F};
  uint32_t vad_min_speech_ms{128};
  uint32_t vad_min_noise_ms{640};
};

class EspSrAudioFrontend final : public AudioFrontend {
public:
  explicit EspSrAudioFrontend(EspSrAudioFrontendConfig config = {});
  ~EspSrAudioFrontend() override;

  EspSrAudioFrontend(const EspSrAudioFrontend&) = delete;
  EspSrAudioFrontend& operator=(const EspSrAudioFrontend&) = delete;

  bool start() override;
  void reset() override;
  bool set_wake_threshold(float threshold) override;
  bool begin_playback_reference(uint64_t epoch) override;
  void end_playback_reference(uint64_t epoch) override;
  bool push_playback_reference(std::span<const int16_t> accepted_pcm,
                               uint32_t sample_rate_hz) override;
  PlaybackReferenceStats playback_reference_stats() const override;
  AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                      std::span<int16_t> cleaned_16k) override;

  size_t reference_overruns() const;
  size_t output_overruns() const;
  size_t feed_chunk_samples() const;

private:
  struct Impl;
  Impl* impl_{};
  EspSrAudioFrontendConfig config_{};
};

} // namespace companion

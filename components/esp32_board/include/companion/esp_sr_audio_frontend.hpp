#pragma once

#include "companion/audio_frontend.hpp"

#include <cstddef>
#include <cstdint>
#include <span>

namespace companion {

struct EspSrAudioFrontendConfig {
  // Development defaults only. Final threshold/model are physical evidence gates.
  float wake_threshold{0.60F};
  uint32_t vad_min_speech_ms{128};
  uint32_t vad_min_noise_ms{640};
};

// ESP-SR implementation of the portable companion_app AudioFrontend contract.
// All ESP-SR types stay in the .cpp implementation so vendor APIs never leak
// into companion_app or host tests.
class EspSrAudioFrontend final : public AudioFrontend {
public:
  explicit EspSrAudioFrontend(EspSrAudioFrontendConfig config = {});
  ~EspSrAudioFrontend() override;

  EspSrAudioFrontend(const EspSrAudioFrontend&) = delete;
  EspSrAudioFrontend& operator=(const EspSrAudioFrontend&) = delete;

  bool start() override;
  void reset() override;
  bool push_playback_reference(std::span<const int16_t> pcm_16k) override;
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

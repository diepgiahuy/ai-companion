#pragma once

#include "companion/app.hpp"

#include "driver/i2s_std.h"

#include <array>

namespace companion {

class Esp32Audio final : public Microphone, public Speaker {
public:
  Esp32Audio() = default;
  ~Esp32Audio() override;

  bool initialize();
  bool start_capture() override;
  size_t read_capture(std::span<int16_t> destination) override;
  void stop_capture() override;
  bool start_playback(uint32_t sample_rate_hz) override;
  size_t write_playback(std::span<const int16_t> mono_pcm) override;
  bool playback_drained() const override;
  void stop_playback() override;

private:
  i2s_chan_handle_t microphone_channel_{};
  i2s_chan_handle_t speaker_channel_{};
  bool microphone_running_{};
  bool speaker_running_{};
  uint64_t playback_done_at_us_{};
  uint32_t playback_sample_rate_hz_{kAudioSampleRateHz};
  std::array<int32_t, kAudioFrameSamples> microphone_raw_{};
  std::array<int16_t, 512> speaker_stereo_{};

  bool initialize_microphone();
  bool initialize_speaker();
};

} // namespace companion

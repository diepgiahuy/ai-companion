#pragma once

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstring>
#include <string_view>

namespace companion {

inline constexpr std::string_view kWakeModelHeyBin{"hey_bin"};
inline constexpr std::string_view kWakeModelDisabled{"disabled"};

struct DeviceSettings {
  bool smart_vad_enabled{true};
  uint32_t vad_threshold{450};
  uint32_t vad_silence_ms{800};
  uint32_t vad_min_speech_ms{250};
  uint32_t idle_after_ms{5'000};
  uint32_t alarm_visible_ms{10'000};
  uint32_t alarm_tone_ms{900};
  uint16_t alarm_tone_hz{880};
  int16_t alarm_tone_amplitude{3'500};
  uint32_t ota_poll_interval_s{21'600};
  uint8_t volume{70};
  float wake_threshold{0.60F};
  std::array<char, 64> wake_model{"hey_bin"};

  constexpr std::string_view wake_model_view() const {
    size_t length = 0;
    while (length < wake_model.size() && wake_model[length] != '\0') ++length;
    return {wake_model.data(), length};
  }

  constexpr bool wake_enabled() const {
    return wake_model_view() == kWakeModelHeyBin;
  }

  void set_wake_model(std::string_view model) {
    wake_model.fill('\0');
    const size_t count = std::min(model.size(), wake_model.size() - 1);
    std::copy_n(model.begin(), count, wake_model.begin());
  }

  constexpr bool validate() const {
    if (vad_threshold < 1 || vad_threshold > 65535 ||
        vad_silence_ms < 100 || vad_silence_ms > 5000 ||
        vad_min_speech_ms < 50 || vad_min_speech_ms > 5000 ||
        idle_after_ms < 1000 || idle_after_ms > 3600000 ||
        alarm_visible_ms < 1000 || alarm_visible_ms > 3600000) {
      return false;
    }
    if (ota_poll_interval_s < 3600 || ota_poll_interval_s > 604800) {
      return false;
    }
    if (volume > 100) {
      return false;
    }
    if (wake_threshold < 0.40F || wake_threshold > 0.9999F) {
      return false;
    }
    if (alarm_tone_ms > 60000) {
      return false;
    }
    if (alarm_tone_ms > 0 && (alarm_tone_hz < 50 || alarm_tone_hz > 5000)) {
      return false;
    }
    // alarm_tone_amplitude is int16_t, so INT16_MAX is guaranteed by the type.
    // Reject the only invalid range that can still be represented.
    if (alarm_tone_amplitude < 0) {
      return false;
    }
    const auto model = wake_model_view();
    if (model != kWakeModelHeyBin && model != kWakeModelDisabled) {
      return false;
    }
    return true;
  }
};

struct SettingsTwin {
  uint64_t version{};
  DeviceSettings settings{};

  constexpr bool valid() const {
    return version > 0 && settings.validate();
  }
};

} // namespace companion

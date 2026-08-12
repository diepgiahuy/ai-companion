#include "companion/esp32_audio.hpp"

#include "companion/board_pins.hpp"

#include "esp_err.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"

#include <algorithm>
#include <limits>

namespace companion {
namespace {
constexpr char kTag[] = "companion.audio";

int16_t microphone_sample_to_pcm(int32_t raw) {
  // INMP441 supplies a 24-bit sample in a 32-bit I2S slot. The shift leaves
  // useful headroom; tune this one constant after the first physical capture.
  const int32_t scaled = raw >> 14;
  return static_cast<int16_t>(std::clamp(
      scaled, static_cast<int32_t>(std::numeric_limits<int16_t>::min()),
      static_cast<int32_t>(std::numeric_limits<int16_t>::max())));
}
} // namespace

Esp32Audio::~Esp32Audio() {
  stop_capture();
  stop_playback();
  if (microphone_channel_) i2s_del_channel(microphone_channel_);
  if (speaker_channel_) i2s_del_channel(speaker_channel_);
}

bool Esp32Audio::initialize() {
  return initialize_microphone() && initialize_speaker();
}

bool Esp32Audio::initialize_microphone() {
  i2s_chan_config_t channel = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_1, I2S_ROLE_MASTER);
  channel.dma_desc_num = 6;
  channel.dma_frame_num = static_cast<uint32_t>(kAudioFrameSamples);
  if (i2s_new_channel(&channel, nullptr, &microphone_channel_) != ESP_OK) return false;

  i2s_std_config_t config{};
  config.clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(kAudioSampleRateHz);
  config.slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(
      I2S_DATA_BIT_WIDTH_32BIT, I2S_SLOT_MODE_MONO);
  config.slot_cfg.slot_mask = I2S_STD_SLOT_LEFT;
  config.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  config.gpio_cfg.bclk = board::kMicBclk;
  config.gpio_cfg.ws = board::kMicWs;
  config.gpio_cfg.dout = I2S_GPIO_UNUSED;
  config.gpio_cfg.din = board::kMicData;
  config.gpio_cfg.invert_flags = {};
  return i2s_channel_init_std_mode(microphone_channel_, &config) == ESP_OK;
}

bool Esp32Audio::initialize_speaker() {
  i2s_chan_config_t channel = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
  channel.dma_desc_num = 6;
  channel.dma_frame_num = 256;
  if (i2s_new_channel(&channel, &speaker_channel_, nullptr) != ESP_OK) return false;

  i2s_std_config_t config{};
  config.clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(kAudioSampleRateHz);
  config.slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(
      I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_STEREO);
  config.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  config.gpio_cfg.bclk = board::kAmpBclk;
  config.gpio_cfg.ws = board::kAmpLrc;
  config.gpio_cfg.dout = board::kAmpData;
  config.gpio_cfg.din = I2S_GPIO_UNUSED;
  config.gpio_cfg.invert_flags = {};
  return i2s_channel_init_std_mode(speaker_channel_, &config) == ESP_OK;
}

bool Esp32Audio::start_capture() {
  if (!microphone_channel_ || microphone_running_) return false;
  if (i2s_channel_enable(microphone_channel_) != ESP_OK) return false;
  microphone_running_ = true;
  return true;
}

size_t Esp32Audio::read_capture(std::span<int16_t> destination) {
  if (!microphone_running_ || destination.empty()) return 0;
  const size_t wanted = std::min(destination.size(), microphone_raw_.size());
  size_t bytes_read = 0;
  const esp_err_t result = i2s_channel_read(
      microphone_channel_, microphone_raw_.data(), wanted * sizeof(int32_t),
      &bytes_read, 0);
  if (result == ESP_ERR_TIMEOUT) return 0;
  if (result != ESP_OK) {
    ESP_LOGE(kTag, "I2S microphone read failed: %s", esp_err_to_name(result));
    return 0;
  }
  const size_t count = bytes_read / sizeof(int32_t);
  for (size_t i = 0; i < count; ++i) {
    destination[i] = microphone_sample_to_pcm(microphone_raw_[i]);
  }
  return count;
}

void Esp32Audio::stop_capture() {
  if (!microphone_running_) return;
  i2s_channel_disable(microphone_channel_);
  microphone_running_ = false;
}

bool Esp32Audio::start_playback(uint32_t sample_rate_hz) {
  if (!speaker_channel_ || speaker_running_ ||
      (sample_rate_hz != 16'000 && sample_rate_hz != 24'000)) {
    return false;
  }
  i2s_std_clk_config_t clock = I2S_STD_CLK_DEFAULT_CONFIG(sample_rate_hz);
  if (i2s_channel_reconfig_std_clock(speaker_channel_, &clock) != ESP_OK) {
    return false;
  }
  if (i2s_channel_enable(speaker_channel_) != ESP_OK) return false;
  speaker_running_ = true;
  playback_sample_rate_hz_ = sample_rate_hz;
  playback_done_at_us_ = static_cast<uint64_t>(esp_timer_get_time());
  return true;
}

size_t Esp32Audio::write_playback(std::span<const int16_t> mono_pcm) {
  if (!speaker_running_ || mono_pcm.empty()) return 0;
  const size_t frames = std::min<size_t>(speaker_stereo_.size() / 2, mono_pcm.size());
  for (size_t i = 0; i < frames; ++i) {
    speaker_stereo_[2 * i] = mono_pcm[i];
    speaker_stereo_[2 * i + 1] = mono_pcm[i];
  }
  size_t bytes_written = 0;
  const size_t bytes = frames * 2 * sizeof(int16_t);
  const esp_err_t result = i2s_channel_write(
      speaker_channel_, speaker_stereo_.data(), bytes, &bytes_written, 0);
  if (result == ESP_ERR_TIMEOUT) return 0;
  if (result != ESP_OK) {
    ESP_LOGE(kTag, "I2S speaker write failed: %s", esp_err_to_name(result));
    return 0;
  }
  const size_t frames_written = bytes_written / (2 * sizeof(int16_t));
  const uint64_t now = static_cast<uint64_t>(esp_timer_get_time());
  playback_done_at_us_ = std::max(playback_done_at_us_, now) +
                         frames_written * 1'000'000ULL / playback_sample_rate_hz_;
  return frames_written;
}

bool Esp32Audio::playback_drained() const {
  if (!speaker_running_) return true;
  return static_cast<uint64_t>(esp_timer_get_time()) >= playback_done_at_us_;
}

void Esp32Audio::stop_playback() {
  if (!speaker_running_) return;
  i2s_channel_disable(speaker_channel_);
  speaker_running_ = false;
  playback_done_at_us_ = 0;
}

} // namespace companion

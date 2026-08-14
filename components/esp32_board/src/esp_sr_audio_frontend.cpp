#include "companion/esp_sr_audio_frontend.hpp"

#include "esp_afe_sr_models.h"
#include "esp_afe_sr_iface.h"
#include "esp_afe_config.h"
#include "esp_log.h"
#include "model_path.h"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <new>

namespace companion {
namespace {
constexpr char kTag[] = "esp_sr_frontend";
constexpr size_t kMaxAfeChunkSamples = 1024;
constexpr size_t kReferenceCapacity = 8192; // 512 ms at 16 kHz.
constexpr size_t kOutputCapacity = 2048;

static_assert(kReferenceCapacity > kMaxAfeChunkSamples);

struct SampleRing {
  int16_t* storage{};
  size_t capacity{};
  size_t head{};
  size_t count{};

  void clear() { head = count = 0; }
  bool push(int16_t sample) {
    if (count == capacity) return false;
    storage[(head + count) % capacity] = sample;
    ++count;
    return true;
  }
  bool pop(int16_t& sample) {
    if (count == 0) return false;
    sample = storage[head];
    head = (head + 1) % capacity;
    --count;
    return true;
  }
};
} // namespace

struct EspSrAudioFrontend::Impl {
  srmodel_list_t* models{};
  const esp_afe_sr_iface_t* handle{};
  esp_afe_sr_data_t* data{};
  size_t feed_chunk{};
  size_t fetch_chunk{};
  bool last_vad_speech{};
  size_t microphone_count{};
  size_t reference_overruns{};
  size_t output_overruns{};
  std::array<int16_t, kMaxAfeChunkSamples> microphone{};
  std::array<int16_t, kMaxAfeChunkSamples * 2> interleaved{};
  std::array<int16_t, kReferenceCapacity> reference_storage{};
  std::array<int16_t, kOutputCapacity> output_storage{};
  SampleRing references{reference_storage.data(), reference_storage.size()};
  SampleRing output{output_storage.data(), output_storage.size()};

  void destroy() {
    if (handle != nullptr && data != nullptr) {
      handle->destroy(data);
      data = nullptr;
    }
    handle = nullptr;
    if (models != nullptr) {
      esp_srmodel_deinit(models);
      models = nullptr;
    }
    feed_chunk = fetch_chunk = microphone_count = 0;
    references.clear();
    output.clear();
    last_vad_speech = false;
  }

  bool append_output(const int16_t* source, size_t count) {
    if (source == nullptr && count != 0) return false;
    for (size_t i = 0; i < count; ++i) {
      if (!output.push(source[i])) {
        ++output_overruns;
        return false;
      }
    }
    return true;
  }
};

EspSrAudioFrontend::EspSrAudioFrontend(EspSrAudioFrontendConfig config)
    : config_(config) {}

EspSrAudioFrontend::~EspSrAudioFrontend() {
  if (impl_ != nullptr) {
    impl_->destroy();
    delete impl_;
  }
}

bool EspSrAudioFrontend::start() {
  if (impl_ == nullptr) {
    impl_ = new (std::nothrow) Impl{};
    if (impl_ == nullptr) return false;
  }
  impl_->destroy();
  impl_->models = esp_srmodel_init("model");
  if (impl_->models == nullptr) {
    ESP_LOGE(kTag, "ESP-SR model partition is unavailable");
    return false;
  }

  afe_config_t* afe_config =
      afe_config_init("MR", impl_->models, AFE_TYPE_FD, AFE_MODE_HIGH_PERF);
  if (afe_config == nullptr) {
    ESP_LOGE(kTag, "AFE configuration failed");
    impl_->destroy();
    return false;
  }
  afe_config->aec_init = true;
  afe_config->vad_init = true;
  afe_config->wakenet_init = true;
  afe_config->vad_mute_playback = false;
  afe_config->vad_min_speech_ms = static_cast<int>(config_.vad_min_speech_ms);
  afe_config->vad_min_noise_ms = static_cast<int>(config_.vad_min_noise_ms);
  afe_config->memory_alloc_mode = AFE_MEMORY_ALLOC_MORE_PSRAM;
  if (afe_config->wakenet_model_name == nullptr) {
    ESP_LOGE(kTag, "no WakeNet model selected in sdkconfig/model partition");
    afe_config_free(afe_config);
    impl_->destroy();
    return false;
  }

  impl_->handle = esp_afe_handle_from_config(afe_config);
  if (impl_->handle != nullptr) {
    impl_->data = impl_->handle->create_from_config(afe_config);
  }
  afe_config_free(afe_config);
  if (impl_->handle == nullptr || impl_->data == nullptr) {
    ESP_LOGE(kTag, "AFE instance creation failed");
    impl_->destroy();
    return false;
  }

  const int feed = impl_->handle->get_feed_chunksize(impl_->data);
  const int fetch = impl_->handle->get_fetch_chunksize(impl_->data);
  const int channels = impl_->handle->get_feed_channel_num(impl_->data);
  const int sample_rate = impl_->handle->get_samp_rate(impl_->data);
  if (feed <= 0 || fetch <= 0 || channels != 2 || sample_rate != 16000 ||
      feed > static_cast<int>(kMaxAfeChunkSamples) ||
      fetch > static_cast<int>(kOutputCapacity)) {
    ESP_LOGE(kTag, "unsupported AFE shape feed=%d fetch=%d channels=%d rate=%d",
             feed, fetch, channels, sample_rate);
    impl_->destroy();
    return false;
  }
  impl_->feed_chunk = static_cast<size_t>(feed);
  impl_->fetch_chunk = static_cast<size_t>(fetch);

  if (impl_->handle->set_wakenet_threshold != nullptr) {
    const float threshold = std::clamp(config_.wake_threshold, 0.4F, 0.9999F);
    if (impl_->handle->set_wakenet_threshold(impl_->data, 1, threshold) < 0) {
      ESP_LOGE(kTag, "WakeNet threshold configuration failed");
      impl_->destroy();
      return false;
    }
  }
  impl_->handle->print_pipeline(impl_->data);
  ESP_LOGI(kTag, "ESP-SR AFE ready feed=%u fetch=%u", static_cast<unsigned>(impl_->feed_chunk),
           static_cast<unsigned>(impl_->fetch_chunk));
  return true;
}

void EspSrAudioFrontend::reset() {
  if (impl_ == nullptr) return;
  impl_->microphone_count = 0;
  impl_->references.clear();
  impl_->output.clear();
  impl_->last_vad_speech = false;
  if (impl_->handle != nullptr && impl_->data != nullptr && impl_->handle->reset_buffer != nullptr) {
    (void)impl_->handle->reset_buffer(impl_->data);
  }
}

bool EspSrAudioFrontend::push_playback_reference(std::span<const int16_t> pcm_16k) {
  if (impl_ == nullptr || impl_->data == nullptr) return false;
  for (const int16_t sample : pcm_16k) {
    if (!impl_->references.push(sample)) {
      ++impl_->reference_overruns;
      return false;
    }
  }
  return true;
}

AudioFrontendResult EspSrAudioFrontend::process_capture(
    std::span<const int16_t> microphone_16k, std::span<int16_t> cleaned_16k) {
  if (impl_ == nullptr || impl_->data == nullptr || impl_->feed_chunk == 0) return {};
  AudioFrontendEvent event = AudioFrontendEvent::none;

  for (const int16_t microphone_sample : microphone_16k) {
    if (impl_->microphone_count >= impl_->feed_chunk) {
      ESP_LOGE(kTag, "AFE microphone staging overflow");
      ++impl_->output_overruns;
      break;
    }
    impl_->microphone[impl_->microphone_count++] = microphone_sample;
    if (impl_->microphone_count != impl_->feed_chunk) continue;

    for (size_t i = 0; i < impl_->feed_chunk; ++i) {
      int16_t reference_sample = 0;
      (void)impl_->references.pop(reference_sample);
      impl_->interleaved[2 * i] = impl_->microphone[i];
      impl_->interleaved[2 * i + 1] = reference_sample;
    }
    const int fed = impl_->handle->feed(impl_->data, impl_->interleaved.data());
    impl_->microphone_count = 0;
    if (fed < 0) {
      ESP_LOGE(kTag, "AFE feed failed: %d", fed);
      ++impl_->output_overruns;
      break;
    }

    afe_fetch_result_t* result = impl_->handle->fetch_with_delay(impl_->data, 0);
    if (result == nullptr) continue;
    if (result->ret_value == ESP_FAIL) {
      ESP_LOGE(kTag, "AFE fetch failed");
      ++impl_->output_overruns;
      break;
    }
    const size_t fetched = result->data_size > 0
                               ? static_cast<size_t>(result->data_size) / sizeof(int16_t)
                               : 0;
    if (fetched > impl_->fetch_chunk || !impl_->append_output(result->data, fetched)) {
      ESP_LOGE(kTag, "AFE output queue overflow: fetched=%u", static_cast<unsigned>(fetched));
      break;
    }

    if (result->wakeup_state == WAKENET_DETECTED) {
      event = AudioFrontendEvent::wake_detected;
    }
    const bool speech = result->vad_state == VAD_SPEECH;
    if (event == AudioFrontendEvent::none && speech != impl_->last_vad_speech) {
      event = speech ? AudioFrontendEvent::speech_started : AudioFrontendEvent::speech_ended;
    }
    impl_->last_vad_speech = speech;
  }

  size_t output_count = 0;
  while (output_count < cleaned_16k.size()) {
    int16_t sample = 0;
    if (!impl_->output.pop(sample)) break;
    cleaned_16k[output_count++] = sample;
  }
  return {.samples = output_count, .event = event};
}

size_t EspSrAudioFrontend::reference_overruns() const {
  return impl_ == nullptr ? 0 : impl_->reference_overruns;
}

size_t EspSrAudioFrontend::output_overruns() const {
  return impl_ == nullptr ? 0 : impl_->output_overruns;
}

size_t EspSrAudioFrontend::feed_chunk_samples() const {
  return impl_ == nullptr ? 0 : impl_->feed_chunk;
}

} // namespace companion

#include "companion/esp_sr_audio_frontend.hpp"

#include "esp_afe_sr_models.h"
#include "esp_afe_sr_iface.h"
#include "esp_afe_config.h"
#include "esp_log.h"
#include "esp_mn_iface.h"
#include "esp_mn_models.h"
#include "esp_mn_speech_commands.h"
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
constexpr char kWakeCommand[] = "HEY BIN";
constexpr int kWakeCommandId = 1;
constexpr int kWakeCommandDurationMs = 3'000;
constexpr size_t kMaxAfeChunkSamples = 1024;
constexpr size_t kMaxWakeChunkSamples = 1024;
constexpr size_t kReferenceCapacity = 8192;
constexpr size_t kOutputCapacity = 2048;
constexpr size_t kWakeCapacity = 4096;

static_assert(kReferenceCapacity > kMaxAfeChunkSamples);
static_assert(kWakeCapacity > kMaxWakeChunkSamples);

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
  esp_mn_iface_t* multinet{};
  model_iface_data_t* multinet_data{};
  size_t feed_chunk{};
  size_t fetch_chunk{};
  size_t multinet_chunk{};
  bool last_vad_speech{};
  bool reference_active{};
  size_t microphone_count{};
  size_t reference_overruns{};
  size_t output_overruns{};
  size_t wake_overruns{};
  uint64_t reference_epoch{};
  uint64_t reference_pushed_samples{};
  uint64_t reference_underflow_events{};
  uint64_t reference_underflow_samples{};
  std::array<int16_t, kMaxAfeChunkSamples> microphone{};
  std::array<int16_t, kMaxAfeChunkSamples * 2> interleaved{};
  std::array<int16_t, kReferenceCapacity> reference_storage{};
  std::array<int16_t, kOutputCapacity> output_storage{};
  std::array<int16_t, kWakeCapacity> wake_storage{};
  std::array<int16_t, kMaxWakeChunkSamples> wake_chunk{};
  SampleRing references{reference_storage.data(), reference_storage.size()};
  SampleRing output{output_storage.data(), output_storage.size()};
  SampleRing wake{wake_storage.data(), wake_storage.size()};

  void clear_wake_state() {
    wake.clear();
    if (multinet != nullptr && multinet_data != nullptr) {
      multinet->clean(multinet_data);
    }
  }

  void clear_pipeline_state() {
    microphone_count = 0;
    references.clear();
    output.clear();
    clear_wake_state();
    last_vad_speech = false;
    if (handle != nullptr && data != nullptr && handle->reset_buffer != nullptr) {
      (void)handle->reset_buffer(data);
    }
  }

  void destroy() {
    if (multinet != nullptr && multinet_data != nullptr) {
      multinet->destroy(multinet_data);
      multinet_data = nullptr;
    }
    multinet = nullptr;
    multinet_chunk = 0;
    wake.clear();
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
    reference_active = false;
    reference_epoch = 0;
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

  void append_wake_audio(const int16_t* source, size_t count) {
    if (source == nullptr && count != 0) return;
    for (size_t i = 0; i < count; ++i) {
      if (!wake.push(source[i])) {
        ++wake_overruns;
        clear_wake_state();
        ESP_LOGW(kTag, "Hey Bin detector queue overflow; recognition window reset");
        return;
      }
    }
  }

  bool detect_hey_bin() {
    if (multinet == nullptr || multinet_data == nullptr || multinet_chunk == 0) {
      return false;
    }
    while (wake.count >= multinet_chunk) {
      for (size_t i = 0; i < multinet_chunk; ++i) {
        if (!wake.pop(wake_chunk[i])) return false;
      }
      const esp_mn_state_t state = multinet->detect(multinet_data, wake_chunk.data());
      if (state == ESP_MN_STATE_DETECTED) {
        esp_mn_results_t* results = multinet->get_results(multinet_data);
        bool detected = false;
        if (results != nullptr) {
          for (int i = 0; i < results->num; ++i) {
            if (results->command_id[i] == kWakeCommandId) {
              detected = true;
              break;
            }
          }
        }
        clear_wake_state();
        if (detected) return true;
      } else if (state == ESP_MN_STATE_TIMEOUT) {
        multinet->clean(multinet_data);
      }
    }
    return false;
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
  // Hey Bin is recognized by MultiNet from this same AFE's cleaned output.
  // Keep WakeNet disabled so there is one acoustic wake authority and no stale
  // packaged Hi ESP path that could trigger the product accidentally.
  afe_config->wakenet_init = false;
  afe_config->vad_mute_playback = false;
  afe_config->vad_min_speech_ms = static_cast<int>(config_.vad_min_speech_ms);
  afe_config->vad_min_noise_ms = static_cast<int>(config_.vad_min_noise_ms);
  afe_config->memory_alloc_mode = AFE_MEMORY_ALLOC_MORE_PSRAM;

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

  char* multinet_name = esp_srmodel_filter(impl_->models, ESP_MN_PREFIX, nullptr);
  if (multinet_name == nullptr) {
    ESP_LOGE(kTag, "no MultiNet model packaged for Hey Bin");
    impl_->destroy();
    return false;
  }
  impl_->multinet = esp_mn_handle_from_name(multinet_name);
  if (impl_->multinet == nullptr) {
    ESP_LOGE(kTag, "MultiNet interface unavailable for model %s", multinet_name);
    impl_->destroy();
    return false;
  }
  impl_->multinet_data = impl_->multinet->create(multinet_name, kWakeCommandDurationMs);
  if (impl_->multinet_data == nullptr) {
    ESP_LOGE(kTag, "MultiNet instance creation failed for model %s", multinet_name);
    impl_->destroy();
    return false;
  }
  const int multinet_rate = impl_->multinet->get_samp_rate(impl_->multinet_data);
  const int multinet_chunk = impl_->multinet->get_samp_chunksize(impl_->multinet_data);
  if (multinet_rate != 16000 || multinet_chunk <= 0 ||
      multinet_chunk > static_cast<int>(kMaxWakeChunkSamples)) {
    ESP_LOGE(kTag, "unsupported MultiNet shape chunk=%d rate=%d",
             multinet_chunk, multinet_rate);
    impl_->destroy();
    return false;
  }
  impl_->multinet_chunk = static_cast<size_t>(multinet_chunk);

  esp_mn_commands_clear();
  esp_mn_commands_add(kWakeCommandId, kWakeCommand);
  if (esp_mn_commands_update() != nullptr) {
    ESP_LOGE(kTag, "Hey Bin is not accepted by the packaged English MultiNet model");
    impl_->destroy();
    return false;
  }
  impl_->multinet->set_det_threshold(
      impl_->multinet_data, std::clamp(config_.wake_threshold, 0.4F, 0.9999F));
  impl_->multinet->print_active_speech_commands(impl_->multinet_data);

  impl_->handle->print_pipeline(impl_->data);
  ESP_LOGI(kTag, "ESP-SR AFE + Hey Bin ready feed=%u fetch=%u mn=%s chunk=%u",
           static_cast<unsigned>(impl_->feed_chunk),
           static_cast<unsigned>(impl_->fetch_chunk), multinet_name,
           static_cast<unsigned>(impl_->multinet_chunk));
  return true;
}

void EspSrAudioFrontend::reset() {
  if (impl_ == nullptr) return;
  impl_->clear_pipeline_state();
  impl_->reference_active = false;
  impl_->reference_epoch = 0;
}

bool EspSrAudioFrontend::set_wake_threshold(float threshold) {
  if (threshold < 0.4F || threshold > 0.9999F || impl_ == nullptr ||
      impl_->multinet == nullptr || impl_->multinet_data == nullptr) {
    return false;
  }
  config_.wake_threshold = threshold;
  impl_->multinet->set_det_threshold(impl_->multinet_data, threshold);
  return true;
}

bool EspSrAudioFrontend::begin_playback_reference(uint64_t epoch) {
  if (impl_ == nullptr || impl_->data == nullptr || epoch == 0) return false;
  impl_->clear_pipeline_state();
  impl_->reference_epoch = epoch;
  impl_->reference_active = true;
  return true;
}

void EspSrAudioFrontend::end_playback_reference(uint64_t epoch) {
  if (impl_ == nullptr || !impl_->reference_active || epoch != impl_->reference_epoch) return;
  impl_->clear_pipeline_state();
  impl_->reference_active = false;
  impl_->reference_epoch = 0;
}

bool EspSrAudioFrontend::push_playback_reference(
    std::span<const int16_t> accepted_pcm, uint32_t sample_rate_hz) {
  if (sample_rate_hz != 16'000 || impl_ == nullptr || impl_->data == nullptr ||
      !impl_->reference_active) {
    return false;
  }
  for (const int16_t sample : accepted_pcm) {
    if (!impl_->references.push(sample)) {
      ++impl_->reference_overruns;
      return false;
    }
    ++impl_->reference_pushed_samples;
  }
  return true;
}

PlaybackReferenceStats EspSrAudioFrontend::playback_reference_stats() const {
  if (impl_ == nullptr) return {};
  return {
      .epoch = impl_->reference_epoch,
      .active = impl_->reference_active,
      .pushed_samples = impl_->reference_pushed_samples,
      .underflow_events = impl_->reference_underflow_events,
      .underflow_samples = impl_->reference_underflow_samples,
      .overruns = impl_->reference_overruns,
  };
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

    bool reference_underflow = false;
    for (size_t i = 0; i < impl_->feed_chunk; ++i) {
      int16_t reference_sample = 0;
      if (!impl_->references.pop(reference_sample) && impl_->reference_active) {
        reference_underflow = true;
        ++impl_->reference_underflow_samples;
      }
      impl_->interleaved[2 * i] = impl_->microphone[i];
      impl_->interleaved[2 * i + 1] = reference_sample;
    }
    if (reference_underflow) ++impl_->reference_underflow_events;

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

    impl_->append_wake_audio(result->data, fetched);
    if (impl_->detect_hey_bin()) {
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

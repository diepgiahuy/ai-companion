#include "companion/websocket_voice_backend.hpp"

#include <algorithm>
#include <cstdio>
#include <cstring>

#include "cJSON.h"
#include "esp_log.h"

namespace companion {
namespace {
constexpr char kTag[] = "voice_ws";
constexpr int kTextOpcode = 0x1;
constexpr int kBinaryOpcode = 0x2;

std::string_view json_string(const cJSON* object, const char* key) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, key);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return {};
  return item->valuestring;
}

bool parse_runtime_config(const cJSON* root, RuntimeConfigPatch& out) {
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(root, "config_version");
  const cJSON* config = cJSON_GetObjectItemCaseSensitive(root, "config");
  if (!cJSON_IsNumber(version) || !cJSON_IsObject(config) || version->valuedouble < 0) return false;
  const cJSON* smart = cJSON_GetObjectItemCaseSensitive(config, "smart_vad_enabled");
  const cJSON* threshold = cJSON_GetObjectItemCaseSensitive(config, "vad_threshold");
  const cJSON* silence = cJSON_GetObjectItemCaseSensitive(config, "vad_silence_ms");
  const cJSON* min_speech = cJSON_GetObjectItemCaseSensitive(config, "vad_min_speech_ms");
  const cJSON* idle = cJSON_GetObjectItemCaseSensitive(config, "idle_after_ms");
  const cJSON* alarm = cJSON_GetObjectItemCaseSensitive(config, "alarm_visible_ms");
  if (!cJSON_IsBool(smart) || !cJSON_IsNumber(threshold) || !cJSON_IsNumber(silence) ||
      !cJSON_IsNumber(min_speech) || !cJSON_IsNumber(idle) || !cJSON_IsNumber(alarm)) return false;
  out.version = static_cast<uint64_t>(version->valuedouble);
  out.smart_vad_enabled = cJSON_IsTrue(smart);
  out.vad_threshold = static_cast<uint32_t>(threshold->valuedouble);
  out.vad_silence_ms = static_cast<uint32_t>(silence->valuedouble);
  out.vad_min_speech_ms = static_cast<uint32_t>(min_speech->valuedouble);
  out.idle_after_ms = static_cast<uint32_t>(idle->valuedouble);
  out.alarm_visible_ms = static_cast<uint32_t>(alarm->valuedouble);
  return true;
}

template <size_t N>
bool copy_string(std::array<char, N>& destination, std::string_view source) {
  if (source.size() >= N) return false;
  destination.fill('\0');
  std::copy(source.begin(), source.end(), destination.begin());
  return true;
}
} // namespace

WebSocketVoiceBackend::WebSocketVoiceBackend() {
  outbound_queue_ = xQueueCreateStatic(kOutboundQueueCapacity, sizeof(Outbound),
                                       outbound_queue_buffer_.data(),
                                       &outbound_queue_storage_);
  playback_queue_ = xQueueCreateStatic(kPlaybackQueueCapacity, sizeof(AudioFrame),
                                       playback_queue_buffer_.data(),
                                       &playback_queue_storage_);
  event_queue_ = xQueueCreateStatic(kEventQueueCapacity, sizeof(BackendEvent),
                                    event_queue_buffer_.data(),
                                    &event_queue_storage_);
}

WebSocketVoiceBackend::~WebSocketVoiceBackend() {
  if (writer_task_ != nullptr) {
    vTaskDelete(writer_task_);
    writer_task_ = nullptr;
  }
  if (client_ != nullptr) {
    esp_websocket_client_stop(client_);
    esp_websocket_client_destroy(client_);
  }
  if (opus_encoder_ != nullptr) esp_opus_enc_close(opus_encoder_);
  if (opus_decoder_ != nullptr) esp_opus_dec_close(opus_decoder_);
}

bool WebSocketVoiceBackend::initialize(std::string_view url,
                                       std::string_view token,
                                       std::string_view device_id,
                                       std::string_view client_id) {
  if (client_ != nullptr || !copy_string(url_, url) ||
      !copy_string(device_id_, device_id) || !copy_string(client_id_, client_id)) {
    return false;
  }
  const int header_size = std::snprintf(
      headers_.data(), headers_.size(),
      "Authorization: Bearer %.*s\r\nProtocol-Version: 1\r\n"
      "Device-Id: %s\r\nClient-Id: %s\r\n",
      static_cast<int>(token.size()), token.data(), device_id_.data(),
      client_id_.data());
  if (header_size < 0 || static_cast<size_t>(header_size) >= headers_.size()) {
    return false;
  }

  esp_opus_enc_config_t encoder_config{
      .sample_rate = ESP_AUDIO_SAMPLE_RATE_16K,
      .channel = ESP_AUDIO_MONO,
      .bits_per_sample = ESP_AUDIO_BIT16,
      .bitrate = ESP_OPUS_BITRATE_AUTO,
      .frame_duration = ESP_OPUS_ENC_FRAME_DURATION_60_MS,
      .application_mode = ESP_OPUS_ENC_APPLICATION_AUDIO,
      .complexity = 0,
      .enable_fec = false,
      .enable_dtx = true,
      .enable_vbr = true,
  };
  int encoder_frame_bytes = 0;
  if (esp_opus_enc_open(&encoder_config, sizeof(encoder_config), &opus_encoder_) !=
          ESP_AUDIO_ERR_OK ||
      opus_encoder_ == nullptr ||
      esp_opus_enc_get_frame_size(opus_encoder_, &encoder_frame_bytes,
                                  &encoder_output_bytes_) != ESP_AUDIO_ERR_OK ||
      encoder_frame_bytes != static_cast<int>(kOpusFrameSamples * sizeof(int16_t)) ||
      encoder_output_bytes_ > static_cast<int>(kMaximumOpusPacketBytes) ||
      !configure_decoder(playback_sample_rate_hz_.load())) {
    return false;
  }

  esp_websocket_client_config_t config{};
  config.uri = url_.data();
  config.headers = headers_.data();
  config.disable_auto_reconnect = false;
  config.reconnect_timeout_ms = 2'000;
  config.network_timeout_ms = 5'000;
  client_ = esp_websocket_client_init(&config);
  if (client_ == nullptr) return false;
  if (esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                    &WebSocketVoiceBackend::event_handler,
                                    this) != ESP_OK) {
    esp_websocket_client_destroy(client_);
    client_ = nullptr;
    return false;
  }
  writer_task_ = xTaskCreateStatic(&WebSocketVoiceBackend::writer_entry,
                                   "voice-writer", kWriterStackDepth, this, 5,
                                   writer_stack_.data(), &writer_task_storage_);
  return writer_task_ != nullptr;
}

bool WebSocketVoiceBackend::start(uint64_t) {
  if (client_ == nullptr) return false;
  if (client_started_.load()) return true;
  const bool started = esp_websocket_client_start(client_) == ESP_OK;
  client_started_.store(started);
  return started;
}

void WebSocketVoiceBackend::tick(uint64_t) {}

bool WebSocketVoiceBackend::begin_turn(uint64_t, ListenMode mode) {
  if (!protocol_connected_.load() || turn_active_.exchange(true)) return false;
  reset_turn_queues();
  const int length = std::snprintf(active_turn_id_.data(), active_turn_id_.size(),
                                   "turn-%llu",
                                   static_cast<unsigned long long>(++turn_sequence_));
  if (length < 0 || static_cast<size_t>(length) >= active_turn_id_.size() ||
      !enqueue_command(CommandType::listen_start, active_turn_id_.data(), mode)) {
    turn_active_.store(false);
    return false;
  }
  return true;
}

bool WebSocketVoiceBackend::send_audio(std::span<const int16_t> pcm) {
  if (!turn_active_.load() || pcm.empty()) return false;
  size_t source_offset = 0;
  while (source_offset < pcm.size()) {
    const size_t count = std::min(pcm.size() - source_offset,
                                  kOpusFrameSamples - upload_payload_size_);
    std::copy_n(pcm.begin() + source_offset, count,
                upload_payload_.begin() + upload_payload_size_);
    source_offset += count;
    upload_payload_size_ += count;
    if (upload_payload_size_ == kOpusFrameSamples) {
      if (!encode_and_enqueue(upload_payload_)) return false;
      upload_payload_.fill(0);
      upload_payload_size_ = 0;
    }
  }
  return true;
}

bool WebSocketVoiceBackend::finish_turn(uint64_t) {
  if (!turn_active_.load()) return false;
  if (upload_payload_size_ != 0) {
    std::fill(upload_payload_.begin() + upload_payload_size_,
              upload_payload_.end(), 0);
    if (!encode_and_enqueue(upload_payload_)) return false;
    upload_payload_.fill(0);
    upload_payload_size_ = 0;
  }
  return enqueue_command(CommandType::listen_stop, active_turn_id_.data());
}

void WebSocketVoiceBackend::cancel_turn() {
  if (turn_active_.exchange(false) || tts_active_.exchange(false)) {
    xQueueReset(outbound_queue_);
    enqueue_command(CommandType::abort, active_turn_id_.data());
  }
  reset_turn_queues();
}

bool WebSocketVoiceBackend::poll_event(BackendEvent& event) {
  return xQueueReceive(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::report_config(const RuntimeConfigPatch& config, bool applied) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::config_report;
  outbound.command.config = config;
  outbound.command.applied = applied;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

size_t WebSocketVoiceBackend::read_playback(std::span<int16_t> destination) {
  AudioFrame frame{};
  if (xQueueReceive(playback_queue_, &frame, 0) != pdPASS) return 0;
  const size_t count = std::min(destination.size(), static_cast<size_t>(frame.count));
  std::copy_n(frame.samples.begin(), count, destination.begin());
  return count;
}

bool WebSocketVoiceBackend::playback_empty() const {
  return uxQueueMessagesWaiting(playback_queue_) == 0;
}

void WebSocketVoiceBackend::event_handler(void* context, esp_event_base_t,
                                          int32_t event_id, void* event_data) {
  static_cast<WebSocketVoiceBackend*>(context)->on_event(
      event_id, static_cast<esp_websocket_event_data_t*>(event_data));
}

void WebSocketVoiceBackend::writer_entry(void* context) {
  static_cast<WebSocketVoiceBackend*>(context)->writer_loop();
}

void WebSocketVoiceBackend::on_event(int32_t event_id,
                                     esp_websocket_event_data_t* data) {
  switch (event_id) {
  case WEBSOCKET_EVENT_CONNECTED:
    socket_connected_.store(true);
    enqueue_command(CommandType::hello);
    break;
  case WEBSOCKET_EVENT_DISCONNECTED:
    socket_connected_.store(false);
    protocol_connected_.store(false);
    turn_active_.store(false);
    tts_active_.store(false);
    xQueueReset(outbound_queue_);
    enqueue_event(BackendEventType::disconnected);
    break;
  case WEBSOCKET_EVENT_DATA:
    if (data == nullptr || data->data_ptr == nullptr || data->data_len < 0) break;
    if (data->payload_offset == 0) receive_opcode_ = data->op_code;
    if (receive_opcode_ == kTextOpcode) {
      const size_t offset = static_cast<size_t>(data->payload_offset);
      const size_t length = static_cast<size_t>(data->data_len);
      if (offset + length >= text_payload_.size()) {
        enqueue_event(BackendEventType::error, "CONTROL TOO LARGE");
        text_payload_size_ = 0;
        break;
      }
      std::copy_n(data->data_ptr, length, text_payload_.begin() + offset);
      text_payload_size_ = offset + length;
      if (text_payload_size_ == static_cast<size_t>(data->payload_len)) {
        text_payload_[text_payload_size_] = '\0';
        handle_text({text_payload_.data(), text_payload_size_});
        text_payload_size_ = 0;
      }
    } else if (receive_opcode_ == kBinaryOpcode) {
      handle_binary(*data);
    }
    break;
  case WEBSOCKET_EVENT_ERROR:
    enqueue_event(BackendEventType::error, "WEBSOCKET ERROR");
    break;
  default:
    break;
  }
}

void WebSocketVoiceBackend::writer_loop() {
  while (true) {
    Outbound outbound{};
    if (xQueueReceive(outbound_queue_, &outbound, portMAX_DELAY) != pdPASS) continue;
    if (outbound.type == OutboundType::control) {
      const Command& command = outbound.command;
      char json[384]{};
      switch (command.type) {
      case CommandType::hello:
        std::snprintf(json, sizeof(json),
                      "{\"type\":\"hello\",\"version\":1,"
                      "\"transport\":\"websocket\",\"audio_params\":{"
                      "\"format\":\"opus\",\"sample_rate\":16000,"
                      "\"channels\":1,\"frame_duration\":60}});
        break;
      case CommandType::listen_start: {
        const char* mode = command.mode == ListenMode::auto_vad ? "auto_vad" : "manual";
        std::snprintf(json, sizeof(json),
                      "{\"type\":\"listen\",\"state\":\"start\","
                      "\"mode\":\"%s\",\"turn_id\":\"%s\"}",
                      mode, command.turn_id.data());
        break;
      }
      case CommandType::listen_stop:
        std::snprintf(json, sizeof(json),
                      "{\"type\":\"listen\",\"state\":\"stop\","
                      "\"turn_id\":\"%s\"}", command.turn_id.data());
        break;
      case CommandType::abort:
        std::snprintf(json, sizeof(json),
                      "{\"type\":\"abort\",\"reason\":\"button_barge_in\","
                      "\"turn_id\":\"%s\"}", command.turn_id.data());
        break;
      case CommandType::alarm_ack:
        std::snprintf(json, sizeof(json),
                      "{\"type\":\"alarm_ack\",\"id\":\"%s\"}",
                      command.turn_id.data());
        break;
      case CommandType::config_report:
        std::snprintf(json, sizeof(json),
          "{\"type\":\"config_report\",\"config_version\":%llu,\"applied\":%s,\"config\":{" 
          "\"smart_vad_enabled\":%s,\"vad_threshold\":%lu,\"vad_silence_ms\":%lu,"
          "\"vad_min_speech_ms\":%lu,\"idle_after_ms\":%lu,\"alarm_visible_ms\":%lu}}",
          static_cast<unsigned long long>(command.config.version), command.applied ? "true" : "false",
          command.config.smart_vad_enabled ? "true" : "false",
          static_cast<unsigned long>(command.config.vad_threshold),
          static_cast<unsigned long>(command.config.vad_silence_ms),
          static_cast<unsigned long>(command.config.vad_min_speech_ms),
          static_cast<unsigned long>(command.config.idle_after_ms),
          static_cast<unsigned long>(command.config.alarm_visible_ms));
        break;
      }
      if (!send_text(json)) ESP_LOGW(kTag, "control send failed");
    } else if (socket_connected_.load()) {
      const int bytes = static_cast<int>(outbound.audio.count);
      const int written = esp_websocket_client_send_bin(
          client_, reinterpret_cast<const char*>(outbound.audio.bytes.data()), bytes,
          pdMS_TO_TICKS(100));
      if (written != bytes) {
        enqueue_event(BackendEventType::error, "AUDIO SEND FAILED");
      }
    }
  }
}

void WebSocketVoiceBackend::handle_text(std::string_view json) {
  cJSON* root = cJSON_ParseWithLength(json.data(), json.size());
  if (root == nullptr) {
    enqueue_event(BackendEventType::error, "INVALID CONTROL");
    return;
  }
  const std::string_view type = json_string(root, "type");
  if (type == "hello") {
    const cJSON* params = cJSON_GetObjectItemCaseSensitive(root, "audio_params");
    const cJSON* rate = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "sample_rate");
    const cJSON* duration = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "frame_duration");
    if (!cJSON_IsNumber(rate) || !cJSON_IsNumber(duration) ||
        (rate->valueint != 16'000 && rate->valueint != 24'000) ||
        duration->valueint != 60 || !configure_decoder(rate->valueint)) {
      cJSON_Delete(root);
      enqueue_event(BackendEventType::error, "UNSUPPORTED OPUS HELLO");
      return;
    }
    playback_sample_rate_hz_.store(static_cast<uint32_t>(rate->valueint));
    protocol_connected_.store(true);
    RuntimeConfigPatch config{};
    if (parse_runtime_config(root, config)) enqueue_config_event(config);
    enqueue_event(BackendEventType::connected);
  } else if (type == "config") {
    RuntimeConfigPatch config{};
    if (!parse_runtime_config(root, config) || !enqueue_config_event(config))
      enqueue_event(BackendEventType::error, "INVALID CONFIG");
  } else if (type == "stt") {
    enqueue_event(BackendEventType::transcript, json_string(root, "text"));
  } else if (type == "tts") {
    const std::string_view state = json_string(root, "state");
    if (state == "start") {
      tts_active_.store(true);
      enqueue_event(BackendEventType::tts_started);
    } else if (state == "sentence_start") {
      enqueue_event(BackendEventType::tts_sentence, json_string(root, "text"));
    } else if (state == "stop") {
      tts_active_.store(false);
      turn_active_.store(false);
      enqueue_event(BackendEventType::tts_finished);
    }
  } else if (type == "alarm") {
    const std::string_view alarm_id = json_string(root, "id");
    enqueue_event(BackendEventType::alarm, json_string(root, "message"));
    if (!alarm_id.empty()) enqueue_command(CommandType::alarm_ack, alarm_id);
  } else if (type == "schedule") {
    enqueue_event(BackendEventType::schedule, json_string(root, "message"));
  } else if (type == "ui") {
    const cJSON* ui = cJSON_GetObjectItemCaseSensitive(root, "ui");
    if (cJSON_IsObject(ui)) {
      enqueue_event(BackendEventType::ui_card, json_string(ui, "primary"));
    }
  } else if (type == "error") {
    turn_active_.store(false);
    tts_active_.store(false);
    enqueue_event(BackendEventType::error, json_string(root, "code"));
  }
  cJSON_Delete(root);
}

void WebSocketVoiceBackend::handle_binary(
    const esp_websocket_event_data_t& data) {
  if (!tts_active_.load()) return;
  const size_t offset = static_cast<size_t>(data.payload_offset);
  const size_t length = static_cast<size_t>(data.data_len);
  const size_t expected = static_cast<size_t>(data.payload_len);
  if (expected == 0 || expected > kMaximumOpusPacketBytes ||
      offset + length > expected) {
    enqueue_event(BackendEventType::error, "INVALID TTS FRAME");
    return;
  }
  std::memcpy(binary_payload_.bytes.data() + offset, data.data_ptr, length);
  if (offset + length == expected) {
    binary_payload_.count = static_cast<uint16_t>(expected);
    if (!decode_and_enqueue(binary_payload_))
      enqueue_event(BackendEventType::error, "OPUS DECODE FAILED");
    binary_payload_ = {};
  }
}

bool WebSocketVoiceBackend::encode_and_enqueue(
    std::span<const int16_t, kOpusFrameSamples> pcm) {
  if (opus_encoder_ == nullptr) return false;
  OpusPacket packet{};
  esp_audio_enc_in_frame_t input{
      .buffer = reinterpret_cast<uint8_t*>(const_cast<int16_t*>(pcm.data())),
      .len = static_cast<uint32_t>(pcm.size_bytes()),
  };
  esp_audio_enc_out_frame_t output{
      .buffer = packet.bytes.data(),
      .len = static_cast<uint32_t>(packet.bytes.size()),
      .encoded_bytes = 0,
  };
  if (esp_opus_enc_process(opus_encoder_, &input, &output) != ESP_AUDIO_ERR_OK ||
      output.encoded_bytes == 0 || output.encoded_bytes > packet.bytes.size()) {
    return false;
  }
  packet.count = static_cast<uint16_t>(output.encoded_bytes);
  return enqueue_audio(packet);
}

bool WebSocketVoiceBackend::configure_decoder(uint32_t sample_rate_hz) {
  if (sample_rate_hz != 16'000 && sample_rate_hz != 24'000) return false;
  if (opus_decoder_ != nullptr) {
    esp_opus_dec_close(opus_decoder_);
    opus_decoder_ = nullptr;
  }
  esp_opus_dec_cfg_t config{
      .sample_rate = sample_rate_hz,
      .channel = ESP_AUDIO_MONO,
      .frame_duration = static_cast<esp_opus_dec_frame_duration_t>(
          ESP_OPUS_ENC_FRAME_DURATION_60_MS),
      .self_delimited = false,
  };
  return esp_opus_dec_open(&config, sizeof(config), &opus_decoder_) ==
             ESP_AUDIO_ERR_OK &&
         opus_decoder_ != nullptr;
}

bool WebSocketVoiceBackend::decode_and_enqueue(const OpusPacket& packet) {
  if (opus_decoder_ == nullptr || packet.count == 0) return false;
  std::array<int16_t, kMaximumDecodedSamples> decoded{};
  esp_audio_dec_in_raw_t input{
      .buffer = const_cast<uint8_t*>(packet.bytes.data()),
      .len = packet.count,
      .consumed = 0,
      .frame_recover = ESP_AUDIO_DEC_RECOVERY_NONE,
  };
  esp_audio_dec_out_frame_t output{
      .buffer = reinterpret_cast<uint8_t*>(decoded.data()),
      .len = static_cast<uint32_t>(decoded.size() * sizeof(int16_t)),
      .decoded_size = 0,
  };
  esp_audio_dec_info_t info{};
  if (esp_opus_dec_decode(opus_decoder_, &input, &output, &info) !=
          ESP_AUDIO_ERR_OK ||
      output.decoded_size == 0 || output.decoded_size % sizeof(int16_t) != 0) {
    return false;
  }
  const size_t decoded_samples = output.decoded_size / sizeof(int16_t);
  for (size_t offset = 0; offset < decoded_samples;) {
    AudioFrame frame{};
    frame.count = static_cast<uint16_t>(
        std::min(kAudioFrameSamples, decoded_samples - offset));
    std::copy_n(decoded.begin() + offset, frame.count, frame.samples.begin());
    if (xQueueSend(playback_queue_, &frame, 0) != pdPASS) return false;
    offset += frame.count;
  }
  return true;
}

bool WebSocketVoiceBackend::enqueue_command(CommandType type,
                                            std::string_view turn,
                                            ListenMode mode) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = type;
  outbound.command.mode = mode;
  if (!copy_string(outbound.command.turn_id, turn)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_audio(const OpusPacket& frame) {
  Outbound outbound{};
  outbound.type = OutboundType::audio;
  outbound.audio = frame;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_event(BackendEventType type,
                                          std::string_view text) {
  BackendEvent event{};
  event.type = type;
  event.set_text(text);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_config_event(const RuntimeConfigPatch& config) {
  BackendEvent event{};
  event.type = BackendEventType::config;
  event.config = config;
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::send_text(std::string_view text) {
  if (!socket_connected_.load()) return false;
  const int written = esp_websocket_client_send_text(
      client_, text.data(), static_cast<int>(text.size()), pdMS_TO_TICKS(1'000));
  return written == static_cast<int>(text.size());
}

void WebSocketVoiceBackend::reset_turn_queues() {
  xQueueReset(playback_queue_);
  binary_payload_ = {};
  upload_payload_ = {};
  upload_payload_size_ = 0;
}

} // namespace companion

#include "companion/websocket_voice_backend.hpp"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <initializer_list>

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

bool has_only_fields(const cJSON* object,
                     std::initializer_list<std::string_view> allowed);

bool parse_uint32(const cJSON* value, uint32_t& output) {
  if (!cJSON_IsNumber(value) || value->valuedouble < 0 ||
      value->valuedouble > UINT32_MAX) {
    return false;
  }
  const auto parsed = static_cast<uint32_t>(value->valuedouble);
  if (value->valuedouble != static_cast<double>(parsed)) return false;
  output = parsed;
  return true;
}

bool parse_uint64(const cJSON* value, uint64_t& output) {
  constexpr double kMaximumExactJSONInteger = 9'007'199'254'740'991.0;
  if (!cJSON_IsNumber(value) || value->valuedouble < 0 ||
      value->valuedouble > kMaximumExactJSONInteger) {
    return false;
  }
  const auto parsed = static_cast<uint64_t>(value->valuedouble);
  if (value->valuedouble != static_cast<double>(parsed)) return false;
  output = parsed;
  return true;
}

bool optional_bounded_string(const cJSON* object, const char* key,
                             size_t maximum_size) {
  const cJSON* value = cJSON_GetObjectItemCaseSensitive(object, key);
  return value == nullptr ||
         (cJSON_IsString(value) && value->valuestring != nullptr &&
          std::strlen(value->valuestring) <= maximum_size);
}

bool parse_runtime_config(const cJSON* payload, RuntimeConfigPatch& out) {
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(payload, "config_version");
  const cJSON* config = cJSON_GetObjectItemCaseSensitive(payload, "config");
  uint64_t parsed_version = 0;
  if (!parse_uint64(version, parsed_version) || !cJSON_IsObject(config) ||
      !has_only_fields(config, {"smart_vad_enabled", "vad_threshold",
                                "vad_silence_ms", "vad_min_speech_ms",
                                "idle_after_ms", "alarm_visible_ms", "locale",
                                "timezone", "voice_key"}) ||
      !optional_bounded_string(config, "locale", 64) ||
      !optional_bounded_string(config, "timezone", 64) ||
      !optional_bounded_string(config, "voice_key", 128)) return false;
  const cJSON* smart = cJSON_GetObjectItemCaseSensitive(config, "smart_vad_enabled");
  const cJSON* threshold = cJSON_GetObjectItemCaseSensitive(config, "vad_threshold");
  const cJSON* silence = cJSON_GetObjectItemCaseSensitive(config, "vad_silence_ms");
  const cJSON* min_speech = cJSON_GetObjectItemCaseSensitive(config, "vad_min_speech_ms");
  const cJSON* idle = cJSON_GetObjectItemCaseSensitive(config, "idle_after_ms");
  const cJSON* alarm = cJSON_GetObjectItemCaseSensitive(config, "alarm_visible_ms");
  uint32_t parsed_threshold = 0;
  uint32_t parsed_silence = 0;
  uint32_t parsed_min_speech = 0;
  uint32_t parsed_idle = 0;
  uint32_t parsed_alarm = 0;
  if (!cJSON_IsBool(smart) || !parse_uint32(threshold, parsed_threshold) ||
      !parse_uint32(silence, parsed_silence) ||
      !parse_uint32(min_speech, parsed_min_speech) ||
      !parse_uint32(idle, parsed_idle) || !parse_uint32(alarm, parsed_alarm) ||
      parsed_threshold < 1 || parsed_threshold > 65'535 ||
      parsed_silence < 100 || parsed_silence > 5'000 ||
      parsed_min_speech < 50 || parsed_min_speech > 5'000 ||
      parsed_idle < 1'000 || parsed_idle > 3'600'000 ||
      parsed_alarm < 1'000 || parsed_alarm > 3'600'000) return false;
  out.version = parsed_version;
  out.smart_vad_enabled = cJSON_IsTrue(smart);
  out.vad_threshold = parsed_threshold;
  out.vad_silence_ms = parsed_silence;
  out.vad_min_speech_ms = parsed_min_speech;
  out.idle_after_ms = parsed_idle;
  out.alarm_visible_ms = parsed_alarm;
  return true;
}

bool optional_features_valid(const cJSON* payload) {
  const cJSON* features = cJSON_GetObjectItemCaseSensitive(payload, "features");
  if (features == nullptr) return true;
  if (!has_only_fields(features, {"streaming_tts", "button_barge_in"})) return false;
  for (const char* key : {"streaming_tts", "button_barge_in"}) {
    const cJSON* value = cJSON_GetObjectItemCaseSensitive(features, key);
    if (value != nullptr && !cJSON_IsBool(value)) return false;
  }
  return true;
}

const cJSON* json_object(const cJSON* object, const char* key) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, key);
  return cJSON_IsObject(item) ? item : nullptr;
}

bool has_only_fields(const cJSON* object,
                     std::initializer_list<std::string_view> allowed) {
  if (!cJSON_IsObject(object)) return false;
  for (const cJSON* item = object->child; item != nullptr; item = item->next) {
    if (item->string == nullptr) return false;
    const std::string_view name = item->string;
    if (std::find(allowed.begin(), allowed.end(), name) == allowed.end()) {
      return false;
    }
  }
  return true;
}

bool json_integer_equals(const cJSON* value, int expected) {
  return cJSON_IsNumber(value) && value->valuedouble == expected &&
         value->valueint == expected;
}

bool payload_fields_valid(protocol::ControlType type, const cJSON* payload) {
  using protocol::ControlType;
  switch (type) {
  case ControlType::session_ready:
    return has_only_fields(payload, {"transport", "audio_params", "features",
                                     "config", "config_version"});
  case ControlType::session_ping:
  case ControlType::session_pong:
    return has_only_fields(payload, {});
  case ControlType::turn_abort:
    return has_only_fields(payload, {"reason"});
  case ControlType::turn_state:
    return has_only_fields(payload, {"state", "reason"});
  case ControlType::transcript_final:
    return has_only_fields(payload, {"text"});
  case ControlType::tts_lifecycle:
    return has_only_fields(payload, {"state", "text"});
  case ControlType::agent_status:
    return has_only_fields(payload, {"state"});
  case ControlType::ui_card:
    return has_only_fields(payload, {"ui"});
  case ControlType::ui_state:
    return has_only_fields(payload, {"emotion", "tool_name"});
  case ControlType::alarm_fired:
    return has_only_fields(payload, {"alarm_id", "message", "fire_at"});
  case ControlType::schedule_updated:
    return has_only_fields(payload, {"message", "fire_at"});
  case ControlType::config_update:
    return has_only_fields(payload, {"config_version", "config"});
  case ControlType::protocol_error:
    return has_only_fields(payload, {"code", "message"});
  default:
    return true;
  }
}

bool payload_semantics_valid(protocol::ControlType type, const cJSON* payload) {
  using protocol::ControlType;
  const auto nonempty = [payload](const char* field) {
    return !json_string(payload, field).empty();
  };
  switch (type) {
  case ControlType::session_ready: {
    if (!optional_features_valid(payload)) return false;
    const cJSON* version = cJSON_GetObjectItemCaseSensitive(payload, "config_version");
    const cJSON* config = cJSON_GetObjectItemCaseSensitive(payload, "config");
    uint64_t ignored_version = 0;
    if (!parse_uint64(version, ignored_version)) return false;
    if (config == nullptr) return true;
    RuntimeConfigPatch ignored{};
    return parse_runtime_config(payload, ignored);
  }
  case ControlType::session_ping:
  case ControlType::session_pong:
    return true;
  case ControlType::turn_abort:
    return nonempty("reason");
  case ControlType::turn_state:
    return nonempty("state");
  case ControlType::transcript_final:
    return nonempty("text");
  case ControlType::tts_lifecycle: {
    const std::string_view state = json_string(payload, "state");
    return state == "start" || state == "stop" ||
           ((state == "sentence_start" || state == "sentence_end") &&
            nonempty("text"));
  }
  case ControlType::agent_status:
    return nonempty("state");
  case ControlType::ui_card:
    return json_object(payload, "ui") != nullptr;
  case ControlType::ui_state:
    return nonempty("emotion");
  case ControlType::alarm_fired:
    return nonempty("alarm_id") && nonempty("message") && nonempty("fire_at");
  case ControlType::schedule_updated:
    return nonempty("message") && nonempty("fire_at");
  case ControlType::config_update: {
    RuntimeConfigPatch ignored{};
    return parse_runtime_config(payload, ignored);
  }
  case ControlType::protocol_error:
    return nonempty("code") && nonempty("message");
  default:
    return true;
  }
}

enum class VersionStatus : uint8_t { valid, unsupported, malformed };

VersionStatus protocol_version_status(const cJSON* root) {
  constexpr double kMaximumExactJSONInteger = 9'007'199'254'740'991.0;
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(root, "version");
  if (!cJSON_IsNumber(version) ||
      std::abs(version->valuedouble) > kMaximumExactJSONInteger ||
      std::trunc(version->valuedouble) != version->valuedouble) {
    return VersionStatus::malformed;
  }
  return version->valuedouble == static_cast<double>(protocol::kVersion)
             ? VersionStatus::valid
             : VersionStatus::unsupported;
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
      "Authorization: Bearer %.*s\r\nProtocol-Version: 2\r\n"
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
  if (!protocol_connected_.load()) return false;
  std::array<char, 40> turn_id{};
  const int length = std::snprintf(turn_id.data(), turn_id.size(),
                                   "turn-%llu",
                                   static_cast<unsigned long long>(++turn_sequence_));
  if (length < 0 || static_cast<size_t>(length) >= turn_id.size()) return false;
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool already_active = turn_active_.exchange(true);
  if (!already_active) active_turn_id_ = turn_id;
  taskEXIT_CRITICAL(&turn_id_lock_);
  if (already_active) return false;
  reset_turn_queues();
  if (!enqueue_command(CommandType::listen_start, turn_id.data(), mode)) {
    turn_active_.store(false);
    return false;
  }
  return true;
}

bool WebSocketVoiceBackend::send_audio(std::span<const int16_t> pcm) {
  if (!turn_active_.load() || pcm.empty()) return false;
  size_t source_offset = 0;
  while (source_offset < pcm.size()) {
    std::array<int16_t, kOpusFrameSamples> frame{};
    bool frame_ready = false;
    uint64_t generation = 0;
    taskENTER_CRITICAL(&media_buffer_lock_);
    if (!turn_active_.load()) {
      taskEXIT_CRITICAL(&media_buffer_lock_);
      return false;
    }
    generation = media_generation_.load();
    const size_t count = std::min(pcm.size() - source_offset,
                                  kOpusFrameSamples - upload_payload_size_);
    std::copy_n(pcm.begin() + source_offset, count,
                upload_payload_.begin() + upload_payload_size_);
    source_offset += count;
    upload_payload_size_ += count;
    if (upload_payload_size_ == kOpusFrameSamples) {
      frame = upload_payload_;
      upload_payload_ = {};
      upload_payload_size_ = 0;
      frame_ready = true;
    }
    taskEXIT_CRITICAL(&media_buffer_lock_);
    if (frame_ready &&
        (!turn_active_.load() || generation != media_generation_.load() ||
         !encode_and_enqueue(frame, generation))) return false;
  }
  return turn_active_.load();
}

bool WebSocketVoiceBackend::finish_turn(uint64_t) {
  if (!turn_active_.load()) return false;
  std::array<int16_t, kOpusFrameSamples> frame{};
  bool frame_ready = false;
  uint64_t generation = 0;
  taskENTER_CRITICAL(&media_buffer_lock_);
  if (!turn_active_.load()) {
    taskEXIT_CRITICAL(&media_buffer_lock_);
    return false;
  }
  generation = media_generation_.load();
  if (upload_payload_size_ != 0) {
    std::copy_n(upload_payload_.begin(), upload_payload_size_, frame.begin());
    upload_payload_ = {};
    upload_payload_size_ = 0;
    frame_ready = true;
  }
  taskEXIT_CRITICAL(&media_buffer_lock_);
  if (frame_ready &&
      (!turn_active_.load() || generation != media_generation_.load() ||
       !encode_and_enqueue(frame, generation))) return false;
  if (!turn_active_.load() || generation != media_generation_.load()) return false;
  const std::array<char, 40> turn_id = active_turn_id_snapshot();
  return enqueue_command(CommandType::listen_stop, turn_id.data());
}

void WebSocketVoiceBackend::cancel_turn() {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool was_turn_active = turn_active_.exchange(false);
  const bool was_tts_active = tts_active_.exchange(false);
  const bool had_active_turn = was_turn_active || was_tts_active;
  const std::array<char, 40> turn_id = active_turn_id_;
  taskEXIT_CRITICAL(&turn_id_lock_);
  if (had_active_turn) {
    xQueueReset(outbound_queue_);
    enqueue_command(CommandType::abort, turn_id.data());
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
    protocol_connected_.store(false);
    clear_session_id();
    reset_turn_queues();
    enqueue_command(CommandType::hello);
    break;
  case WEBSOCKET_EVENT_DISCONNECTED:
    socket_connected_.store(false);
    protocol_connected_.store(false);
    clear_session_id();
    taskENTER_CRITICAL(&turn_id_lock_);
    turn_active_.store(false);
    tts_active_.store(false);
    taskEXIT_CRITICAL(&turn_id_lock_);
    xQueueReset(outbound_queue_);
    reset_turn_queues();
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
      char payload[384]{};
      protocol::ControlType type{};
      std::string_view turn_id;
      std::string_view correlation_id;
      switch (command.type) {
      case CommandType::hello:
        type = protocol::ControlType::session_hello;
        std::snprintf(payload, sizeof(payload),
                      "{\"transport\":\"websocket\",\"audio_params\":{"
                      "\"format\":\"opus\",\"sample_rate\":16000,"
                      "\"channels\":1,\"frame_duration\":60}}");
        break;
      case CommandType::session_pong:
        type = protocol::ControlType::session_pong;
        correlation_id = command.correlation_id.data();
        std::snprintf(payload, sizeof(payload), "{}");
        break;
      case CommandType::listen_start: {
        type = protocol::ControlType::turn_listen;
        turn_id = command.turn_id.data();
        const char* mode = command.mode == ListenMode::auto_vad ? "auto_vad" : "manual";
        std::snprintf(payload, sizeof(payload),
                      "{\"state\":\"start\",\"mode\":\"%s\"}", mode);
        break;
      }
      case CommandType::listen_stop:
        type = protocol::ControlType::turn_listen;
        turn_id = command.turn_id.data();
        std::snprintf(payload, sizeof(payload), "{\"state\":\"stop\"}");
        break;
      case CommandType::abort:
        type = protocol::ControlType::turn_abort;
        turn_id = command.turn_id.data();
        std::snprintf(payload, sizeof(payload),
                      "{\"reason\":\"button_barge_in\"}");
        break;
      case CommandType::alarm_ack:
        type = protocol::ControlType::alarm_ack;
        {
          char alarm_id[128]{};
          size_t alarm_id_size = 0;
          if (!protocol::encode_json_string(command.turn_id.data(), alarm_id,
                                            alarm_id_size) ||
              std::snprintf(payload, sizeof(payload), "{\"alarm_id\":%.*s}",
                            static_cast<int>(alarm_id_size), alarm_id) < 0) {
            payload[0] = '\0';
          }
        }
        break;
      case CommandType::config_report:
        type = protocol::ControlType::config_report;
        std::snprintf(payload, sizeof(payload),
          "{\"config_version\":%llu,\"applied\":%s,\"config\":{"
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
      case CommandType::protocol_error:
        type = protocol::ControlType::protocol_error;
        {
          char code[256]{};
          char message[256]{};
          size_t code_size = 0;
          size_t message_size = 0;
          if (!protocol::encode_json_string(command.code.data(), code, code_size) ||
              !protocol::encode_json_string(command.message.data(), message,
                                            message_size)) {
            payload[0] = '\0';
            break;
          }
          const int payload_size = std::snprintf(
              payload, sizeof(payload), "{\"code\":%.*s,\"message\":%.*s}",
              static_cast<int>(code_size), code,
              static_cast<int>(message_size), message);
          if (payload_size < 0 ||
              static_cast<size_t>(payload_size) >= sizeof(payload)) {
            payload[0] = '\0';
          }
        }
        break;
      }
      if (std::strlen(payload) >= sizeof(payload)) {
        ESP_LOGW(kTag, "control payload too large");
        continue;
      }
      char message_id[32]{};
      const int message_id_size = std::snprintf(
          message_id, sizeof(message_id), "firmware-%llu",
          static_cast<unsigned long long>(message_sequence_.fetch_add(1) + 1));
      char json[768]{};
      size_t json_size = 0;
      const std::array<char, 64> session_id = session_id_snapshot();
      const protocol::Envelope envelope{
          .type = type,
          .message_id = message_id_size > 0 &&
                        static_cast<size_t>(message_id_size) < sizeof(message_id)
                            ? std::string_view(message_id)
                            : std::string_view{},
          .payload_json = payload,
          .correlation_id = correlation_id,
          .session_id = session_id.data(),
          .turn_id = turn_id,
          .generation_id = 0,
          .has_generation_id = false,
          .idempotency_key = {},
          .occurred_at = {},
      };
      if (!protocol::encode(envelope, json, json_size)) {
        ESP_LOGW(kTag, "control envelope encode failed");
        continue;
      }
      if (!send_text({json, json_size})) ESP_LOGW(kTag, "control send failed");
    } else if (socket_connected_.load() &&
               outbound.media_generation == media_generation_.load()) {
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
  if (!cJSON_IsObject(root)) {
    enqueue_protocol_error("invalid_envelope", "control envelope must be an object");
    enqueue_event(BackendEventType::error, "INVALID CONTROL ENVELOPE");
    cJSON_Delete(root);
    return;
  }
  if (!has_only_fields(root, {"version", "type", "message_id",
                              "correlation_id", "session_id", "turn_id",
                              "generation_id", "idempotency_key",
                              "occurred_at", "payload"})) {
    enqueue_protocol_error("invalid_envelope", "control envelope has unknown fields");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL FIELD");
    cJSON_Delete(root);
    return;
  }
  const VersionStatus version_status = protocol_version_status(root);
  if (version_status != VersionStatus::valid) {
    const bool unsupported = version_status == VersionStatus::unsupported;
    enqueue_protocol_error(unsupported ? "unsupported_protocol_version" : "invalid_envelope",
                           unsupported ? "only protocol version 2 is supported"
                                       : "version must be an integer");
    enqueue_event(BackendEventType::error,
                  unsupported ? "UNSUPPORTED PROTOCOL VERSION"
                              : "INVALID CONTROL VERSION");
    cJSON_Delete(root);
    return;
  }
  const std::string_view message_id = json_string(root, "message_id");
  const std::string_view type_name = json_string(root, "type");
  const cJSON* payload = json_object(root, "payload");
  protocol::ControlType type{};
  if (message_id.empty() || type_name.empty() || payload == nullptr ||
      message_id.size() >= Command{}.correlation_id.size()) {
    enqueue_protocol_error("invalid_envelope", "message_id, type, and payload object are required");
    enqueue_event(BackendEventType::error, "INVALID CONTROL ENVELOPE");
    cJSON_Delete(root);
    return;
  }
  if (!protocol::parse_type(type_name, type)) {
    enqueue_protocol_error("unknown_message_type", "control type is not supported");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL TYPE");
    cJSON_Delete(root);
    return;
  }
  if (!payload_fields_valid(type, payload)) {
    enqueue_protocol_error("invalid_envelope", "control payload has unknown fields");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL PAYLOAD FIELD");
    cJSON_Delete(root);
    return;
  }
  if (!payload_semantics_valid(type, payload)) {
    enqueue_protocol_error("invalid_envelope", "control payload is malformed");
    enqueue_event(BackendEventType::error, "INVALID CONTROL PAYLOAD");
    cJSON_Delete(root);
    return;
  }

  if (!protocol_connected_.load() &&
      type != protocol::ControlType::session_ready &&
      type != protocol::ControlType::protocol_error) {
    enqueue_protocol_error("invalid_envelope", "session.ready is required first");
    enqueue_event(BackendEventType::error, "CONTROL BEFORE SESSION READY");
    cJSON_Delete(root);
    return;
  }

  if (type != protocol::ControlType::session_ready && protocol_connected_.load()) {
    const std::array<char, 64> expected_session = session_id_snapshot();
    if (json_string(root, "session_id") != expected_session.data()) {
      enqueue_protocol_error("invalid_envelope", "session_id does not match");
      enqueue_event(BackendEventType::error, "INVALID CONTROL SESSION");
      cJSON_Delete(root);
      return;
    }
  }

  const bool turn_scoped =
      type == protocol::ControlType::turn_abort ||
      type == protocol::ControlType::turn_state ||
      type == protocol::ControlType::transcript_final ||
      type == protocol::ControlType::tts_lifecycle ||
      type == protocol::ControlType::agent_status ||
      type == protocol::ControlType::ui_card ||
      type == protocol::ControlType::ui_state;
  const std::string_view incoming_turn_id = json_string(root, "turn_id");
  if (turn_scoped && incoming_turn_id.empty()) {
    enqueue_protocol_error("invalid_envelope", "turn-scoped control requires turn_id");
    enqueue_event(BackendEventType::error, "MISSING CONTROL TURN");
    cJSON_Delete(root);
    return;
  }
  if (turn_scoped ||
      (type == protocol::ControlType::protocol_error && !incoming_turn_id.empty())) {
    if (!active_turn_matches(incoming_turn_id)) {
      cJSON_Delete(root);
      return; // A delayed terminal/control from an older turn is harmless.
    }
  }

  if (type == protocol::ControlType::session_ready) {
    if (protocol_connected_.load()) {
      enqueue_protocol_error("invalid_envelope", "session.ready was already accepted");
      enqueue_event(BackendEventType::error, "DUPLICATE SESSION READY");
      cJSON_Delete(root);
      return;
    }
    const std::string_view session_id = json_string(root, "session_id");
    const std::string_view transport = json_string(payload, "transport");
    const cJSON* params = cJSON_GetObjectItemCaseSensitive(payload, "audio_params");
    const std::string_view format = params == nullptr ? std::string_view{} :
        json_string(params, "format");
    const cJSON* rate = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "sample_rate");
    const cJSON* channels = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "channels");
    const cJSON* duration = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "frame_duration");
    if (transport != "websocket" || format != "opus" ||
        !has_only_fields(params, {"format", "sample_rate", "channels",
                                  "frame_duration"}) ||
        !json_integer_equals(rate, 24'000) ||
        !json_integer_equals(channels, 1) ||
        !json_integer_equals(duration, 60) || !configure_decoder(24'000)) {
      enqueue_protocol_error("invalid_envelope", "unsupported session.ready transport or audio parameters");
      cJSON_Delete(root);
      enqueue_event(BackendEventType::error, "UNSUPPORTED OPUS HELLO");
      return;
    }
    playback_sample_rate_hz_.store(static_cast<uint32_t>(rate->valueint));
    if (session_id.empty() || !set_session_id(session_id)) {
      cJSON_Delete(root);
      enqueue_protocol_error("invalid_envelope", "session.ready requires a bounded session_id");
      enqueue_event(BackendEventType::error, "INVALID SESSION READY");
      return;
    }
    protocol_connected_.store(true);
    RuntimeConfigPatch config{};
    if (parse_runtime_config(payload, config)) enqueue_config_event(config);
    enqueue_event(BackendEventType::connected);
  } else if (type == protocol::ControlType::config_update) {
    RuntimeConfigPatch config{};
    if (!parse_runtime_config(payload, config) || !enqueue_config_event(config))
      enqueue_event(BackendEventType::error, "INVALID CONFIG");
  } else if (type == protocol::ControlType::transcript_final) {
    enqueue_event(BackendEventType::transcript, json_string(payload, "text"));
  } else if (type == protocol::ControlType::tts_lifecycle) {
    const std::string_view state = json_string(payload, "state");
    if (state == "start") {
      if (activate_tts_for_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_started);
    } else if (state == "sentence_start") {
      enqueue_event(BackendEventType::tts_sentence, json_string(payload, "text"));
    } else if (state == "stop") {
      if (deactivate_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_finished);
    }
  } else if (type == protocol::ControlType::alarm_fired) {
    const std::string_view alarm_id = json_string(payload, "alarm_id");
    enqueue_event(BackendEventType::alarm, json_string(payload, "message"));
    if (!alarm_id.empty()) enqueue_command(CommandType::alarm_ack, alarm_id);
  } else if (type == protocol::ControlType::schedule_updated) {
    enqueue_event(BackendEventType::schedule, json_string(payload, "message"));
  } else if (type == protocol::ControlType::ui_card) {
    const cJSON* ui = cJSON_GetObjectItemCaseSensitive(payload, "ui");
    if (cJSON_IsObject(ui)) {
      enqueue_event(BackendEventType::ui_card, json_string(ui, "primary"));
    }
  } else if (type == protocol::ControlType::ui_state) {
    enqueue_event(BackendEventType::ui_card, json_string(payload, "emotion"));
  } else if (type == protocol::ControlType::agent_status) {
    enqueue_event(BackendEventType::ui_card, json_string(payload, "state"));
  } else if (type == protocol::ControlType::turn_state) {
    if (json_string(payload, "state") == "interrupted" &&
        deactivate_matching_turn(incoming_turn_id)) {
      reset_turn_queues();
    }
  } else if (type == protocol::ControlType::session_ping) {
    enqueue_pong(message_id);
  } else if (type == protocol::ControlType::session_pong) {
    // No state transition is associated with a pong.
  } else if (type == protocol::ControlType::turn_abort) {
    if (deactivate_matching_turn(incoming_turn_id)) reset_turn_queues();
  } else if (type == protocol::ControlType::protocol_error) {
    bool applies = true;
    if (!incoming_turn_id.empty()) {
      applies = deactivate_matching_turn(incoming_turn_id);
    } else {
      taskENTER_CRITICAL(&turn_id_lock_);
      turn_active_.store(false);
      tts_active_.store(false);
      taskEXIT_CRITICAL(&turn_id_lock_);
    }
    if (applies) {
      reset_turn_queues();
      enqueue_event(BackendEventType::error, json_string(payload, "code"));
    }
  } else {
    enqueue_protocol_error("invalid_envelope", "control type is invalid in this direction");
    enqueue_event(BackendEventType::error, "INVALID CONTROL DIRECTION");
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
    taskENTER_CRITICAL(&media_buffer_lock_);
    binary_payload_ = {};
    taskEXIT_CRITICAL(&media_buffer_lock_);
    enqueue_event(BackendEventType::error, "INVALID TTS FRAME");
    return;
  }
  OpusPacket packet{};
  bool packet_ready = false;
  uint64_t generation = 0;
  taskENTER_CRITICAL(&media_buffer_lock_);
  if (offset == 0) binary_payload_ = {};
  if (offset != binary_payload_.count) {
    binary_payload_ = {};
    taskEXIT_CRITICAL(&media_buffer_lock_);
    enqueue_event(BackendEventType::error, "OUT OF ORDER TTS FRAME");
    return;
  }
  std::memcpy(binary_payload_.bytes.data() + offset, data.data_ptr, length);
  binary_payload_.count = static_cast<uint16_t>(offset + length);
  if (offset + length == expected) {
    packet = binary_payload_;
    binary_payload_ = {};
    packet_ready = true;
    generation = media_generation_.load();
  }
  taskEXIT_CRITICAL(&media_buffer_lock_);
  if (packet_ready && tts_active_.load() &&
      generation == media_generation_.load() &&
      !decode_and_enqueue(packet, generation))
    enqueue_event(BackendEventType::error, "OPUS DECODE FAILED");
}

bool WebSocketVoiceBackend::encode_and_enqueue(
    std::span<const int16_t, kOpusFrameSamples> pcm,
    uint64_t media_generation) {
  if (opus_encoder_ == nullptr) return false;
  OpusPacket packet{};
  esp_audio_enc_in_frame_t input{
      .buffer = reinterpret_cast<uint8_t*>(const_cast<int16_t*>(pcm.data())),
      .len = static_cast<uint32_t>(pcm.size_bytes()),
  };
  esp_audio_enc_out_frame_t output{};
  output.buffer = packet.bytes.data();
  output.len = static_cast<uint32_t>(packet.bytes.size());
  if (esp_opus_enc_process(opus_encoder_, &input, &output) != ESP_AUDIO_ERR_OK ||
      output.encoded_bytes == 0 || output.encoded_bytes > packet.bytes.size()) {
    return false;
  }
  packet.count = static_cast<uint16_t>(output.encoded_bytes);
  return enqueue_audio(packet, media_generation);
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

bool WebSocketVoiceBackend::decode_and_enqueue(const OpusPacket& packet,
                                               uint64_t media_generation) {
  if (opus_decoder_ == nullptr || packet.count == 0) return false;
  std::array<int16_t, kMaximumDecodedSamples> decoded{};
  esp_audio_dec_in_raw_t input{
      .buffer = const_cast<uint8_t*>(packet.bytes.data()),
      .len = packet.count,
      .consumed = 0,
      .frame_recover = ESP_AUDIO_DEC_RECOVERY_NONE,
  };
  esp_audio_dec_out_frame_t output{};
  output.buffer = reinterpret_cast<uint8_t*>(decoded.data());
  output.len = static_cast<uint32_t>(decoded.size() * sizeof(int16_t));
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
    taskENTER_CRITICAL(&media_buffer_lock_);
    const bool still_current = tts_active_.load() &&
                               media_generation == media_generation_.load();
    const bool queued = still_current &&
                        xQueueSend(playback_queue_, &frame, 0) == pdPASS;
    taskEXIT_CRITICAL(&media_buffer_lock_);
    if (!still_current) return true;
    if (!queued) return false;
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

bool WebSocketVoiceBackend::enqueue_pong(std::string_view correlation_id) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::session_pong;
  if (!copy_string(outbound.command.correlation_id, correlation_id)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_protocol_error(std::string_view code,
                                                    std::string_view message) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::protocol_error;
  if (!copy_string(outbound.command.code, code) ||
      !copy_string(outbound.command.message, message)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_audio(const OpusPacket& frame,
                                          uint64_t media_generation) {
  if (!turn_active_.load() || media_generation != media_generation_.load()) return false;
  Outbound outbound{};
  outbound.type = OutboundType::audio;
  outbound.audio = frame;
  outbound.media_generation = media_generation;
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

bool WebSocketVoiceBackend::set_session_id(std::string_view session_id) {
  taskENTER_CRITICAL(&session_id_lock_);
  const bool copied = copy_string(session_id_, session_id);
  taskEXIT_CRITICAL(&session_id_lock_);
  return copied;
}

void WebSocketVoiceBackend::clear_session_id() {
  taskENTER_CRITICAL(&session_id_lock_);
  session_id_.fill('\0');
  taskEXIT_CRITICAL(&session_id_lock_);
}

std::array<char, 64> WebSocketVoiceBackend::session_id_snapshot() {
  std::array<char, 64> session_id{};
  taskENTER_CRITICAL(&session_id_lock_);
  session_id = session_id_;
  taskEXIT_CRITICAL(&session_id_lock_);
  return session_id;
}

std::array<char, 40> WebSocketVoiceBackend::active_turn_id_snapshot() {
  std::array<char, 40> turn_id{};
  taskENTER_CRITICAL(&turn_id_lock_);
  turn_id = active_turn_id_;
  taskEXIT_CRITICAL(&turn_id_lock_);
  return turn_id;
}

bool WebSocketVoiceBackend::active_turn_matches(std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = (turn_active_.load() || tts_active_.load()) &&
                       turn_id == active_turn_id_.data();
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

bool WebSocketVoiceBackend::activate_tts_for_matching_turn(
    std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = turn_active_.load() &&
                       turn_id == active_turn_id_.data();
  if (matches) tts_active_.store(true);
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

bool WebSocketVoiceBackend::deactivate_matching_turn(std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = (turn_active_.load() || tts_active_.load()) &&
                       turn_id == active_turn_id_.data();
  if (matches) {
    turn_active_.store(false);
    tts_active_.store(false);
  }
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

void WebSocketVoiceBackend::reset_turn_queues() {
  taskENTER_CRITICAL(&media_buffer_lock_);
  media_generation_.fetch_add(1);
  xQueueReset(playback_queue_);
  binary_payload_ = {};
  upload_payload_ = {};
  upload_payload_size_ = 0;
  taskEXIT_CRITICAL(&media_buffer_lock_);
}

} // namespace companion

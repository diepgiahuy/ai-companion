#include "companion/websocket_voice_backend.hpp"

#include "cJSON.h"

#include <algorithm>
#include <cstdio>
#include <initializer_list>

namespace companion {
namespace {
constexpr std::string_view kConfirmationName = "device.user_confirmation";
constexpr std::string_view kConfirmationVersion = "1";

template <size_t N>
std::string_view fixed_view(const std::array<char, N>& value) {
  const auto end = std::find(value.begin(), value.end(), '\0');
  return {value.data(), static_cast<size_t>(end - value.begin())};
}

template <size_t N>
bool copy_fixed(std::array<char, N>& destination, std::string_view value) {
  if (value.empty() || value.size() >= destination.size()) return false;
  destination.fill('\0');
  std::copy(value.begin(), value.end(), destination.begin());
  return true;
}

std::string_view json_string(const cJSON* object, const char* key) {
  if (object == nullptr) return {};
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, key);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return {};
  return item->valuestring;
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

bool exact_uint64(const cJSON* value, uint64_t& output) {
  constexpr double kMaximumExactJSONInteger = 9'007'199'254'740'991.0;
  if (!cJSON_IsNumber(value) || value->valuedouble < 0 ||
      value->valuedouble > kMaximumExactJSONInteger) return false;
  const auto parsed = static_cast<uint64_t>(value->valuedouble);
  if (value->valuedouble != static_cast<double>(parsed)) return false;
  output = parsed;
  return true;
}

bool exact_uint32(const cJSON* value, uint32_t& output) {
  uint64_t parsed = 0;
  if (!exact_uint64(value, parsed) || parsed > UINT32_MAX) return false;
  output = static_cast<uint32_t>(parsed);
  return true;
}

bool strict_capability_envelope(const cJSON* root) {
  return has_only_fields(root,
                         {"version", "type", "message_id", "correlation_id",
                          "session_id", "turn_id", "generation_id", "payload"});
}
} // namespace

std::string_view UserConfirmationRequest::correlation_id_view() const {
  return fixed_view(correlation_id);
}
std::string_view UserConfirmationRequest::turn_id_view() const {
  return fixed_view(turn_id);
}
std::string_view UserConfirmationRequest::tool_name_view() const {
  return fixed_view(tool_name);
}
std::string_view UserConfirmationRequest::prompt_view() const {
  return fixed_view(prompt);
}

bool UserConfirmationRequest::valid() const {
  return !correlation_id_view().empty() && !turn_id_view().empty() &&
         !tool_name_view().empty() && !prompt_view().empty() &&
         generation_id > 0 && deadline_ms >= 50 && deadline_ms <= 5'000;
}

bool WebSocketVoiceBackend::enable_confirmation_protocol() {
  if (confirmation_protocol_enabled_.load()) return true;
  if (client_ == nullptr || client_started_.load()) return false;

  // Pairing and capability controls share exactly one DATA/reassembly owner.
  // If pairing has not already installed the mux, replace the base handler here
  // instead of registering an additional confirmation callback.
  if (!pairing_protocol_enabled_.load()) {
    if (esp_websocket_unregister_events(client_, WEBSOCKET_EVENT_ANY,
                                        &WebSocketVoiceBackend::event_handler) != ESP_OK) {
      return false;
    }
    if (esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                      &WebSocketVoiceBackend::pairing_event_handler,
                                      this) != ESP_OK) {
      (void)esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                          &WebSocketVoiceBackend::event_handler,
                                          this);
      return false;
    }
  }
  confirmation_protocol_enabled_.store(true);
  return true;
}

bool WebSocketVoiceBackend::advertise_capabilities() {
  if (!protocol_connected_.load()) return false;
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  const int id_size = std::snprintf(message_id.data(), message_id.size(),
                                    "firmware-%llu",
                                    static_cast<unsigned long long>(
                                        message_sequence_.fetch_add(1) + 1));
  if (id_size <= 0 || static_cast<size_t>(id_size) >= message_id.size()) return false;
  const bool confirm = confirmation_protocol_enabled_.load();
  char payload[512]{};
  if (confirm) {
    std::snprintf(payload, sizeof(payload),
                  "{\"capabilities\":["
                  "{\"name\":\"device.user_confirmation\",\"version\":\"1\",\"kind\":\"command\"},"
                  "{\"name\":\"device.settings_v1\",\"version\":\"1\",\"kind\":\"command\"}"
                  "]}");
  } else {
    std::snprintf(payload, sizeof(payload),
                  "{\"capabilities\":["
                  "{\"name\":\"device.settings_v1\",\"version\":\"1\",\"kind\":\"command\"}"
                  "]}");
  }
  std::array<char, 768> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::capability_advertise,
      .message_id = message_id.data(),
      .payload_json = payload,
      .correlation_id = {},
      .session_id = session,
      .turn_id = {},
      .generation_id = 0,
      .has_generation_id = false,
      .idempotency_key = {},
      .occurred_at = {},
  };
  const bool sent = protocol::encode(envelope, encoded, written) &&
                    send_text({encoded.data(), written});
  if (sent && confirm) confirmation_advertised_.store(true);
  return sent;
}

bool WebSocketVoiceBackend::advertise_user_confirmation() {
  if (!confirmation_protocol_enabled_.load() || !protocol_connected_.load()) return false;
  if (confirmation_advertised_.load()) return true;
  return advertise_capabilities();
}

bool WebSocketVoiceBackend::poll_user_confirmation(UserConfirmationRequest& request) {
  taskENTER_CRITICAL(&confirmation_lock_);
  if (!confirmation_active_ || !confirmation_ready_) {
    taskEXIT_CRITICAL(&confirmation_lock_);
    return false;
  }
  request = active_confirmation_;
  confirmation_ready_ = false;
  taskEXIT_CRITICAL(&confirmation_lock_);
  return request.valid();
}

bool WebSocketVoiceBackend::user_confirmation_current(
    const UserConfirmationRequest& request) {
  taskENTER_CRITICAL(&confirmation_lock_);
  const bool current = confirmation_active_ &&
      active_confirmation_.correlation_id_view() == request.correlation_id_view() &&
      active_confirmation_.turn_id_view() == request.turn_id_view() &&
      active_confirmation_.generation_id == request.generation_id;
  taskEXIT_CRITICAL(&confirmation_lock_);
  return current;
}

void WebSocketVoiceBackend::clear_user_confirmation() {
  taskENTER_CRITICAL(&confirmation_lock_);
  active_confirmation_ = {};
  confirmation_ready_ = false;
  confirmation_active_ = false;
  taskEXIT_CRITICAL(&confirmation_lock_);
}

bool WebSocketVoiceBackend::enqueue_confirmation_result(
    const UserConfirmationRequest& request, bool approved) {
  if (!request.valid() || !protocol_connected_.load()) return false;
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  const int id_size = std::snprintf(message_id.data(), message_id.size(),
                                    "firmware-%llu",
                                    static_cast<unsigned long long>(
                                        message_sequence_.fetch_add(1) + 1));
  if (id_size <= 0 || static_cast<size_t>(id_size) >= message_id.size()) return false;
  char payload[96]{};
  const int payload_size = std::snprintf(
      payload, sizeof(payload), "{\"ok\":true,\"value\":{\"approved\":%s}}",
      approved ? "true" : "false");
  if (payload_size <= 0 || static_cast<size_t>(payload_size) >= sizeof(payload)) return false;
  std::array<char, 1'024> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::capability_result,
      .message_id = message_id.data(),
      .payload_json = {payload, static_cast<size_t>(payload_size)},
      .correlation_id = request.correlation_id_view(),
      .session_id = session,
      .turn_id = request.turn_id_view(),
      .generation_id = request.generation_id,
      .has_generation_id = true,
      .idempotency_key = {},
      .occurred_at = {},
  };
  return protocol::encode(envelope, encoded, written) &&
         send_text({encoded.data(), written});
}

bool WebSocketVoiceBackend::enqueue_unsupported_capability_result(
    std::string_view correlation_id, std::string_view turn_id,
    uint64_t generation_id, std::string_view capability_name,
    std::string_view capability_version) {
  if (!protocol_connected_.load() || correlation_id.empty() || turn_id.empty() ||
      generation_id == 0 || capability_name.empty() || capability_version.empty()) {
    return false;
  }
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  const int id_size = std::snprintf(message_id.data(), message_id.size(),
                                    "firmware-%llu",
                                    static_cast<unsigned long long>(
                                        message_sequence_.fetch_add(1) + 1));
  if (id_size <= 0 || static_cast<size_t>(id_size) >= message_id.size()) return false;

  constexpr std::string_view payload = "{\"ok\":false,\"error\":\"unsupported\"}";
  std::array<char, 768> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::capability_result,
      .message_id = message_id.data(),
      .payload_json = payload,
      .correlation_id = correlation_id,
      .session_id = session,
      .turn_id = turn_id,
      .generation_id = generation_id,
      .has_generation_id = true,
      .idempotency_key = {},
      .occurred_at = {},
  };
  return protocol::encode(envelope, encoded, written) &&
         send_text({encoded.data(), written});
}

bool WebSocketVoiceBackend::respond_user_confirmation(
    const UserConfirmationRequest& request, bool approved) {
  if (!user_confirmation_current(request)) return false;
  const bool sent = enqueue_confirmation_result(request, approved);
  clear_user_confirmation();
  return sent;
}

bool WebSocketVoiceBackend::handle_confirmation_cancel(
    const cJSON* root, const cJSON* payload) {
  const std::string_view correlation = json_string(root, "correlation_id");
  const std::string_view turn = json_string(root, "turn_id");
  const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");
  uint64_t generation_id = 0;
  const std::string_view reason = json_string(payload, "reason");
  if (!strict_capability_envelope(root) ||
      correlation.empty() || correlation.size() > 128 ||
      turn.empty() || turn.size() > 128 || !active_turn_matches(turn) ||
      !exact_uint64(generation, generation_id) || generation_id == 0 ||
      !has_only_fields(payload, {"reason"}) || reason.empty() || reason.size() > 64) {
    enqueue_event(BackendEventType::error, "INVALID CONFIRMATION CANCEL");
    return true;
  }
  taskENTER_CRITICAL(&confirmation_lock_);
  if (confirmation_active_ &&
      active_confirmation_.correlation_id_view() == correlation &&
      active_confirmation_.turn_id_view() == turn &&
      active_confirmation_.generation_id == generation_id) {
    active_confirmation_ = {};
    confirmation_ready_ = false;
    confirmation_active_ = false;
  }
  taskEXIT_CRITICAL(&confirmation_lock_);
  return true;
}

bool WebSocketVoiceBackend::handle_confirmation_call(
    const cJSON* root, const cJSON* payload) {
  const std::string_view correlation = json_string(root, "correlation_id");
  const std::string_view turn = json_string(root, "turn_id");
  const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");
  uint64_t generation_id = 0;
  if (!strict_capability_envelope(root) ||
      correlation.empty() || correlation.size() > 128 ||
      turn.empty() || turn.size() > 128 || !active_turn_matches(turn) ||
      !exact_uint64(generation, generation_id) || generation_id == 0 ||
      !has_only_fields(payload, {"name", "version", "arguments", "deadline_ms"}) ||
      json_string(payload, "name") != kConfirmationName ||
      json_string(payload, "version") != kConfirmationVersion) {
    enqueue_event(BackendEventType::error, "INVALID CONFIRMATION CONTROL");
    return true;
  }

  const cJSON* arguments = cJSON_GetObjectItemCaseSensitive(payload, "arguments");
  const cJSON* deadline = cJSON_GetObjectItemCaseSensitive(payload, "deadline_ms");
  uint32_t deadline_ms = 0;
  const std::string_view tool_name = json_string(arguments, "tool_name");
  const std::string_view prompt = json_string(arguments, "prompt");
  if (!cJSON_IsObject(arguments) ||
      !has_only_fields(arguments, {"tool_name", "prompt"}) ||
      tool_name.empty() || tool_name.size() > 96 ||
      prompt.empty() || prompt.size() > 192 ||
      !exact_uint32(deadline, deadline_ms) || deadline_ms < 50 || deadline_ms > 5'000) {
    enqueue_event(BackendEventType::error, "INVALID CONFIRMATION REQUEST");
    return true;
  }

  UserConfirmationRequest request{};
  const bool copied = copy_fixed(request.correlation_id, correlation) &&
                      copy_fixed(request.turn_id, turn) &&
                      copy_fixed(request.tool_name, tool_name) &&
                      copy_fixed(request.prompt, prompt);
  request.generation_id = generation_id;
  request.deadline_ms = deadline_ms;
  taskENTER_CRITICAL(&confirmation_lock_);
  const bool busy = confirmation_active_;
  if (!busy && copied && request.valid()) {
    active_confirmation_ = request;
    confirmation_ready_ = true;
    confirmation_active_ = true;
  }
  taskEXIT_CRITICAL(&confirmation_lock_);
  if (busy || !copied || !request.valid()) {
    enqueue_event(BackendEventType::error,
                  busy ? "CONFIRMATION BUSY" : "INVALID CONFIRMATION REQUEST");
  }
  return true;
}

bool WebSocketVoiceBackend::enqueue_settings_result(
    std::string_view correlation_id, std::string_view turn_id,
    uint64_t generation_id, bool has_generation_id,
    bool ok, const SettingsTwin* applied_twin,
    std::string_view error_code) {
  if (!protocol_connected_.load() || correlation_id.empty()) return false;
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  const int id_size = std::snprintf(message_id.data(), message_id.size(),
                                    "firmware-%llu",
                                    static_cast<unsigned long long>(
                                        message_sequence_.fetch_add(1) + 1));
  if (id_size <= 0 || static_cast<size_t>(id_size) >= message_id.size()) return false;

  char payload[768]{};
  int payload_size = 0;
  if (ok && applied_twin != nullptr) {
    payload_size = std::snprintf(
        payload, sizeof(payload),
        "{\"ok\":true,\"value\":{\"version\":%llu,\"settings\":{"
        "\"smart_vad_enabled\":%s,"
        "\"vad_threshold\":%lu,"
        "\"vad_silence_ms\":%lu,"
        "\"vad_min_speech_ms\":%lu,"
        "\"idle_after_ms\":%lu,"
        "\"alarm_visible_ms\":%lu,"
        "\"alarm_tone_ms\":%lu,"
        "\"alarm_tone_hz\":%u,"
        "\"alarm_tone_amplitude\":%d,"
        "\"ota_poll_interval_s\":%lu,"
        "\"volume\":%u,"
        "\"wake_threshold\":%.2f,"
        "\"wake_model\":\"%s\"}}}",
        static_cast<unsigned long long>(applied_twin->version),
        applied_twin->settings.smart_vad_enabled ? "true" : "false",
        static_cast<unsigned long>(applied_twin->settings.vad_threshold),
        static_cast<unsigned long>(applied_twin->settings.vad_silence_ms),
        static_cast<unsigned long>(applied_twin->settings.vad_min_speech_ms),
        static_cast<unsigned long>(applied_twin->settings.idle_after_ms),
        static_cast<unsigned long>(applied_twin->settings.alarm_visible_ms),
        static_cast<unsigned long>(applied_twin->settings.alarm_tone_ms),
        static_cast<unsigned>(applied_twin->settings.alarm_tone_hz),
        static_cast<int>(applied_twin->settings.alarm_tone_amplitude),
        static_cast<unsigned long>(applied_twin->settings.ota_poll_interval_s),
        static_cast<unsigned>(applied_twin->settings.volume),
        static_cast<double>(applied_twin->settings.wake_threshold),
        applied_twin->settings.wake_model_view().data());
  } else {
    payload_size = std::snprintf(
        payload, sizeof(payload),
        "{\"ok\":false,\"error\":\"%s\"}",
        error_code.empty() ? "invalid_argument" : error_code.data());
  }
  if (payload_size <= 0 || static_cast<size_t>(payload_size) >= sizeof(payload)) return false;

  std::array<char, 1'024> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::capability_result,
      .message_id = message_id.data(),
      .payload_json = {payload, static_cast<size_t>(payload_size)},
      .correlation_id = correlation_id,
      .session_id = session,
      .turn_id = turn_id,
      .generation_id = generation_id,
      .has_generation_id = has_generation_id,
      .idempotency_key = {},
      .occurred_at = {},
  };
  return protocol::encode(envelope, encoded, written) &&
         send_text({encoded.data(), written});
}

bool WebSocketVoiceBackend::handle_settings_call(
    const cJSON* root, const cJSON* payload) {
  const std::string_view correlation = json_string(root, "correlation_id");
  const std::string_view turn = json_string(root, "turn_id");
  const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");
  uint64_t generation_id = 0;
  const bool has_generation = exact_uint64(generation, generation_id) && generation_id > 0;

  if (correlation.empty() || correlation.size() > 128 ||
      !has_only_fields(payload, {"name", "version", "arguments", "deadline_ms"}) ||
      json_string(payload, "name") != kSettingsCapability ||
      json_string(payload, "version") != kSettingsCapabilityVersion) {
    if (!correlation.empty()) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
    }
    enqueue_event(BackendEventType::error, "INVALID SETTINGS CONTROL");
    return true;
  }

  const cJSON* deadline = cJSON_GetObjectItemCaseSensitive(payload, "deadline_ms");
  uint32_t deadline_ms = 0;
  if (deadline != nullptr && (!exact_uint32(deadline, deadline_ms) || deadline_ms < 50 || deadline_ms > 60'000)) {
    (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                  false, nullptr, "invalid_argument");
    enqueue_event(BackendEventType::error, "INVALID SETTINGS DEADLINE");
    return true;
  }

  const cJSON* arguments = cJSON_GetObjectItemCaseSensitive(payload, "arguments");
  if (!cJSON_IsObject(arguments)) {
    (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                  false, nullptr, "invalid_argument");
    enqueue_event(BackendEventType::error, "INVALID SETTINGS ARGUMENTS");
    return true;
  }

  const cJSON* ver = cJSON_GetObjectItemCaseSensitive(arguments, "version");
  uint64_t target_version = 0;
  if (!exact_uint64(ver, target_version) || target_version == 0 ||
      target_version <= current_settings_version_.load()) {
    (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                  false, nullptr, "invalid_argument");
    enqueue_event(BackendEventType::error, "STALE OR INVALID SETTINGS VERSION");
    return true;
  }

  const cJSON* settings_obj = cJSON_GetObjectItemCaseSensitive(arguments, "settings");
  if (!cJSON_IsObject(settings_obj)) {
    (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                  false, nullptr, "invalid_argument");
    enqueue_event(BackendEventType::error, "INVALID SETTINGS OBJECT");
    return true;
  }

  DeviceSettings parsed = current_settings_;
  const cJSON* smart_vad = cJSON_GetObjectItemCaseSensitive(settings_obj, "smart_vad_enabled");
  if (smart_vad != nullptr) {
    if (!cJSON_IsBool(smart_vad)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.smart_vad_enabled = cJSON_IsTrue(smart_vad);
  }

  const cJSON* vad_thr = cJSON_GetObjectItemCaseSensitive(settings_obj, "vad_threshold");
  if (vad_thr != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(vad_thr, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.vad_threshold = val;
  }

  const cJSON* vad_sil = cJSON_GetObjectItemCaseSensitive(settings_obj, "vad_silence_ms");
  if (vad_sil != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(vad_sil, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.vad_silence_ms = val;
  }

  const cJSON* vad_min = cJSON_GetObjectItemCaseSensitive(settings_obj, "vad_min_speech_ms");
  if (vad_min != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(vad_min, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.vad_min_speech_ms = val;
  }

  const cJSON* idle_ms = cJSON_GetObjectItemCaseSensitive(settings_obj, "idle_after_ms");
  if (idle_ms != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(idle_ms, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.idle_after_ms = val;
  }

  const cJSON* alarm_ms = cJSON_GetObjectItemCaseSensitive(settings_obj, "alarm_visible_ms");
  if (alarm_ms != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(alarm_ms, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.alarm_visible_ms = val;
  }

  const cJSON* altone_ms = cJSON_GetObjectItemCaseSensitive(settings_obj, "alarm_tone_ms");
  if (altone_ms != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(altone_ms, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.alarm_tone_ms = val;
  }

  const cJSON* altone_hz = cJSON_GetObjectItemCaseSensitive(settings_obj, "alarm_tone_hz");
  if (altone_hz != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(altone_hz, val) || val > UINT16_MAX) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.alarm_tone_hz = static_cast<uint16_t>(val);
  }

  const cJSON* altone_amp = cJSON_GetObjectItemCaseSensitive(settings_obj, "alarm_tone_amplitude");
  if (altone_amp != nullptr) {
    if (!cJSON_IsNumber(altone_amp) || altone_amp->valuedouble < 0 || altone_amp->valuedouble > INT16_MAX) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.alarm_tone_amplitude = static_cast<int16_t>(altone_amp->valuedouble);
  }

  const cJSON* ota_s = cJSON_GetObjectItemCaseSensitive(settings_obj, "ota_poll_interval_s");
  if (ota_s != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(ota_s, val)) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.ota_poll_interval_s = val;
  }

  const cJSON* vol = cJSON_GetObjectItemCaseSensitive(settings_obj, "volume");
  if (vol != nullptr) {
    uint32_t val = 0;
    if (!exact_uint32(vol, val) || val > 100) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.volume = static_cast<uint8_t>(val);
  }

  const cJSON* wake_thr = cJSON_GetObjectItemCaseSensitive(settings_obj, "wake_threshold");
  if (wake_thr != nullptr) {
    if (!cJSON_IsNumber(wake_thr) || wake_thr->valuedouble < 0.40 || wake_thr->valuedouble > 0.9999) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.wake_threshold = static_cast<float>(wake_thr->valuedouble);
  }

  const std::string_view wake_mdl = json_string(settings_obj, "wake_model");
  if (!wake_mdl.empty()) {
    if (wake_mdl.size() >= parsed.wake_model.size()) {
      (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                    false, nullptr, "invalid_argument");
      return true;
    }
    parsed.set_wake_model(wake_mdl);
  }

  if (!parsed.validate()) {
    (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                  false, nullptr, "invalid_argument");
    enqueue_event(BackendEventType::error, "INVALID SETTINGS VALUES");
    return true;
  }

  current_settings_ = parsed;
  current_settings_version_.store(target_version);
  const SettingsTwin applied_twin{
      .version = target_version,
      .settings = parsed,
  };
  (void)enqueue_settings_event(applied_twin);
  (void)enqueue_settings_result(correlation, turn, generation_id, has_generation,
                                true, &applied_twin, {});
  return true;
}

} // namespace companion

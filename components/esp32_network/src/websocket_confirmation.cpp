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

bool same_settings(const DeviceSettings& left, const DeviceSettings& right) {
  return left.smart_vad_enabled == right.smart_vad_enabled &&
         left.vad_threshold == right.vad_threshold &&
         left.vad_silence_ms == right.vad_silence_ms &&
         left.vad_min_speech_ms == right.vad_min_speech_ms &&
         left.idle_after_ms == right.idle_after_ms &&
         left.alarm_visible_ms == right.alarm_visible_ms &&
         left.alarm_tone_ms == right.alarm_tone_ms &&
         left.alarm_tone_hz == right.alarm_tone_hz &&
         left.alarm_tone_amplitude == right.alarm_tone_amplitude &&
         left.ota_poll_interval_s == right.ota_poll_interval_s &&
         left.volume == right.volume &&
         left.wake_threshold == right.wake_threshold &&
         left.wake_model_view() == right.wake_model_view();
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

void WebSocketVoiceBackend::clear_pending_settings() {
  taskENTER_CRITICAL(&settings_lock_);
  pending_settings_ = {};
  pending_settings_correlation_.fill('\0');
  pending_settings_turn_.fill('\0');
  pending_settings_generation_ = 0;
  pending_settings_has_generation_ = false;
  settings_pending_ = false;
  taskEXIT_CRITICAL(&settings_lock_);
}

bool WebSocketVoiceBackend::advertise_capabilities() {
  if (!protocol_connected_.load()) return false;
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  // Any incomplete RPC belonged to the previous transport/session. The runtime
  // state itself is retained; only the in-flight acknowledgement is discarded.
  clear_pending_settings();

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

  constexpr std::string_view response = "{\"ok\":false,\"error\":\"unsupported\"}";
  std::array<char, 768> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::capability_result,
      .message_id = message_id.data(),
      .payload_json = response,
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
    bool ok, const SettingsTwin* settings_twin,
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

  char payload[256]{};
  int payload_size = 0;
  if (ok && settings_twin != nullptr) {
    payload_size = std::snprintf(
        payload, sizeof(payload),
        "{\"ok\":true,\"value\":{\"applied\":true,\"version\":%llu}}",
        static_cast<unsigned long long>(settings_twin->version));
  } else if (!ok && settings_twin != nullptr) {
    payload_size = std::snprintf(
        payload, sizeof(payload),
        "{\"ok\":true,\"value\":{\"applied\":false,\"version\":%llu,\"error\":\"%.*s\"}}",
        static_cast<unsigned long long>(settings_twin->version),
        static_cast<int>(error_code.size()), error_code.data());
  } else {
    const std::string_view code = error_code.empty() ? std::string_view{"invalid_argument"}
                                                     : error_code;
    payload_size = std::snprintf(
        payload, sizeof(payload),
        "{\"ok\":false,\"error\":\"%.*s\"}",
        static_cast<int>(code.size()), code.data());
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

bool WebSocketVoiceBackend::report_settings_apply(const SettingsTwin& twin,
                                                  bool applied) {
  std::array<char, 129> correlation{};
  std::array<char, 129> turn{};
  uint64_t generation = 0;
  bool has_generation = false;
  bool matched = false;

  taskENTER_CRITICAL(&settings_lock_);
  if (settings_pending_ && pending_settings_.version == twin.version &&
      same_settings(pending_settings_.settings, twin.settings)) {
    matched = true;
    correlation = pending_settings_correlation_;
    turn = pending_settings_turn_;
    generation = pending_settings_generation_;
    has_generation = pending_settings_has_generation_;
    if (applied) {
      current_settings_ = twin.settings;
      current_settings_version_.store(twin.version);
    }
    pending_settings_ = {};
    pending_settings_correlation_.fill('\0');
    pending_settings_turn_.fill('\0');
    pending_settings_generation_ = 0;
    pending_settings_has_generation_ = false;
    settings_pending_ = false;
  }
  taskEXIT_CRITICAL(&settings_lock_);

  if (!matched) return false;
  return enqueue_settings_result(fixed_view(correlation), fixed_view(turn), generation,
                                 has_generation, applied, &twin,
                                 applied ? std::string_view{} :
                                           std::string_view{"apply_failed"});
}

bool WebSocketVoiceBackend::handle_settings_call(
    const cJSON* root, const cJSON* payload) {
  const std::string_view correlation = json_string(root, "correlation_id");
  const std::string_view turn = json_string(root, "turn_id");
  const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");

  if (!strict_capability_envelope(root) || correlation.empty() || correlation.size() > 128 ||
      !turn.empty() || generation != nullptr ||
      !has_only_fields(payload, {"name", "version", "arguments", "deadline_ms"}) ||
      json_string(payload, "name") != kSettingsCapability ||
      json_string(payload, "version") != kSettingsCapabilityVersion) {
    if (!correlation.empty()) {
      (void)enqueue_settings_result(correlation, {}, 0, false,
                                    false, nullptr, "invalid_argument");
    }
    return true;
  }

  const cJSON* deadline = cJSON_GetObjectItemCaseSensitive(payload, "deadline_ms");
  uint32_t deadline_ms = 0;
  if (!exact_uint32(deadline, deadline_ms) || deadline_ms < 50 || deadline_ms > 5'000) {
    (void)enqueue_settings_result(correlation, {}, 0, false,
                                  false, nullptr, "invalid_argument");
    return true;
  }

  const cJSON* arguments = cJSON_GetObjectItemCaseSensitive(payload, "arguments");
  if (!cJSON_IsObject(arguments) || !has_only_fields(arguments, {"version", "settings"})) {
    (void)enqueue_settings_result(correlation, {}, 0, false,
                                  false, nullptr, "invalid_argument");
    return true;
  }

  const cJSON* version_value = cJSON_GetObjectItemCaseSensitive(arguments, "version");
  const cJSON* settings_obj = cJSON_GetObjectItemCaseSensitive(arguments, "settings");
  uint64_t target_version = 0;
  if (!exact_uint64(version_value, target_version) || target_version == 0 ||
      !cJSON_IsObject(settings_obj) ||
      !has_only_fields(settings_obj,
                       {"smart_vad_enabled", "vad_threshold", "vad_silence_ms",
                        "vad_min_speech_ms", "idle_after_ms", "alarm_visible_ms",
                        "ota_poll_interval_s", "wake_model"})) {
    SettingsTwin rejected{.version = target_version};
    (void)enqueue_settings_result(correlation, {}, 0, false,
                                  false, &rejected, "invalid_argument");
    return true;
  }

  DeviceSettings base{};
  uint64_t current_version = 0;
  bool busy = false;
  taskENTER_CRITICAL(&settings_lock_);
  base = current_settings_;
  current_version = current_settings_version_.load();
  busy = settings_pending_;
  taskEXIT_CRITICAL(&settings_lock_);

  if (busy) {
    (void)enqueue_settings_result(correlation, {}, 0, false,
                                  false, nullptr, "busy");
    return true;
  }

  DeviceSettings parsed = base;
  const cJSON* smart_vad = cJSON_GetObjectItemCaseSensitive(settings_obj, "smart_vad_enabled");
  if (smart_vad != nullptr) {
    if (!cJSON_IsBool(smart_vad)) goto invalid_settings;
    parsed.smart_vad_enabled = cJSON_IsTrue(smart_vad);
  }

#define PARSE_U32_SETTING(JSON_KEY, MEMBER)                                      \
  do {                                                                           \
    const cJSON* item = cJSON_GetObjectItemCaseSensitive(settings_obj, JSON_KEY); \
    if (item != nullptr) {                                                        \
      uint32_t value = 0;                                                         \
      if (!exact_uint32(item, value)) goto invalid_settings;                     \
      parsed.MEMBER = value;                                                      \
    }                                                                             \
  } while (false)

  PARSE_U32_SETTING("vad_threshold", vad_threshold);
  PARSE_U32_SETTING("vad_silence_ms", vad_silence_ms);
  PARSE_U32_SETTING("vad_min_speech_ms", vad_min_speech_ms);
  PARSE_U32_SETTING("idle_after_ms", idle_after_ms);
  PARSE_U32_SETTING("alarm_visible_ms", alarm_visible_ms);
  PARSE_U32_SETTING("ota_poll_interval_s", ota_poll_interval_s);
#undef PARSE_U32_SETTING

  if (const cJSON* wake = cJSON_GetObjectItemCaseSensitive(settings_obj, "wake_model");
      wake != nullptr) {
    if (!cJSON_IsString(wake) || wake->valuestring == nullptr) goto invalid_settings;
    const std::string_view model = wake->valuestring;
    if (model.empty() || model.size() >= parsed.wake_model.size()) goto invalid_settings;
    parsed.set_wake_model(model);
  }
  if (!parsed.validate()) goto invalid_settings;

  {
    const SettingsTwin candidate{.version = target_version, .settings = parsed};
    if (target_version < current_version ||
        (target_version == current_version && !same_settings(parsed, base))) {
      (void)enqueue_settings_result(correlation, {}, 0, false,
                                    false, &candidate, "invalid_argument");
      return true;
    }
    if (target_version == current_version) {
      (void)enqueue_settings_result(correlation, {}, 0, false,
                                    true, &candidate, {});
      return true;
    }

    taskENTER_CRITICAL(&settings_lock_);
    settings_pending_ = true;
    pending_settings_ = candidate;
    const bool copied = copy_fixed(pending_settings_correlation_, correlation);
    pending_settings_turn_.fill('\0');
    pending_settings_generation_ = 0;
    pending_settings_has_generation_ = false;
    taskEXIT_CRITICAL(&settings_lock_);
    if (!copied || !enqueue_settings_event(candidate)) {
      clear_pending_settings();
      (void)enqueue_settings_result(correlation, {}, 0, false,
                                    false, nullptr, "busy");
    }
    return true;
  }

invalid_settings:
  {
    const SettingsTwin rejected{.version = target_version, .settings = base};
    (void)enqueue_settings_result(correlation, {}, 0, false,
                                  false, &rejected, "invalid_argument");
    return true;
  }
}

} // namespace companion

#include "companion/websocket_voice_backend.hpp"

#include "cJSON.h"

#include <algorithm>
#include <cstdio>
#include <initializer_list>

namespace companion {
namespace {
constexpr uint8_t kTextOpcode = 0x1;
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
  if (esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                    &WebSocketVoiceBackend::confirmation_event_handler,
                                    this) != ESP_OK) {
    return false;
  }
  confirmation_protocol_enabled_.store(true);
  return true;
}

void WebSocketVoiceBackend::confirmation_event_handler(
    void* context, esp_event_base_t, int32_t event_id, void* event_data) {
  static_cast<WebSocketVoiceBackend*>(context)->on_confirmation_event(
      event_id, static_cast<esp_websocket_event_data_t*>(event_data));
}

void WebSocketVoiceBackend::on_confirmation_event(
    int32_t event_id, esp_websocket_event_data_t* data) {
  if (event_id == WEBSOCKET_EVENT_CONNECTED) {
    confirmation_advertised_.store(false);
    clear_user_confirmation();
    confirmation_text_payload_size_ = 0;
    return;
  }
  if (event_id == WEBSOCKET_EVENT_DISCONNECTED) {
    confirmation_advertised_.store(false);
    clear_user_confirmation();
    confirmation_text_payload_size_ = 0;
    return;
  }
  if (event_id != WEBSOCKET_EVENT_DATA || data == nullptr ||
      data->data_ptr == nullptr || data->data_len < 0) {
    return;
  }
  if (data->payload_offset == 0) confirmation_receive_opcode_ = data->op_code;
  if (confirmation_receive_opcode_ != kTextOpcode) return;

  const size_t offset = static_cast<size_t>(data->payload_offset);
  const size_t length = static_cast<size_t>(data->data_len);
  if (offset + length >= confirmation_text_payload_.size()) {
    confirmation_text_payload_size_ = 0;
    return;
  }
  std::copy_n(data->data_ptr, length,
              confirmation_text_payload_.begin() + offset);
  confirmation_text_payload_size_ = offset + length;
  if (confirmation_text_payload_size_ == static_cast<size_t>(data->payload_len)) {
    confirmation_text_payload_[confirmation_text_payload_size_] = '\0';
    (void)handle_confirmation_text(
        {confirmation_text_payload_.data(), confirmation_text_payload_size_});
    confirmation_text_payload_size_ = 0;
  }
}

bool WebSocketVoiceBackend::advertise_user_confirmation() {
  if (!confirmation_protocol_enabled_.load() || !protocol_connected_.load()) return false;
  if (confirmation_advertised_.load()) return true;
  const auto session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot);
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  const int id_size = std::snprintf(message_id.data(), message_id.size(),
                                    "firmware-%llu",
                                    static_cast<unsigned long long>(
                                        message_sequence_.fetch_add(1) + 1));
  if (id_size <= 0 || static_cast<size_t>(id_size) >= message_id.size()) return false;
  constexpr std::string_view payload =
      "{\"capabilities\":[{\"name\":\"device.user_confirmation\","
      "\"version\":\"1\",\"kind\":\"command\"}]}";
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
  if (sent) confirmation_advertised_.store(true);
  return sent;
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

bool WebSocketVoiceBackend::respond_user_confirmation(
    const UserConfirmationRequest& request, bool approved) {
  if (!user_confirmation_current(request)) return false;
  const bool sent = enqueue_confirmation_result(request, approved);
  clear_user_confirmation();
  return sent;
}

bool WebSocketVoiceBackend::handle_confirmation_text(std::string_view json) {
  cJSON* root = cJSON_ParseWithLength(json.data(), json.size());
  if (root == nullptr || !cJSON_IsObject(root)) {
    if (root != nullptr) cJSON_Delete(root);
    return false;
  }
  const std::string_view type_name = json_string(root, "type");
  protocol::ControlType type{};
  if (!protocol::parse_type(type_name, type) ||
      (type != protocol::ControlType::capability_call &&
       type != protocol::ControlType::capability_cancel)) {
    cJSON_Delete(root);
    return false;
  }
  if (!has_only_fields(root, {"version", "type", "message_id", "correlation_id",
                              "session_id", "turn_id", "generation_id",
                              "idempotency_key", "occurred_at", "payload"})) {
    cJSON_Delete(root);
    return true;
  }

  const auto session_snapshot = session_id_snapshot();
  const std::string_view expected_session = fixed_view(session_snapshot);
  const std::string_view message_id = json_string(root, "message_id");
  const std::string_view session = json_string(root, "session_id");
  const std::string_view correlation = json_string(root, "correlation_id");
  const std::string_view turn = json_string(root, "turn_id");
  const cJSON* payload = cJSON_GetObjectItemCaseSensitive(root, "payload");
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(root, "version");
  const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");
  uint64_t generation_id = 0;
  const bool base_valid =
      cJSON_IsNumber(version) && version->valuedouble == 2.0 &&
      !message_id.empty() && message_id.size() <= 256 &&
      !expected_session.empty() && session == expected_session &&
      !correlation.empty() && correlation.size() <= 128 &&
      !turn.empty() && turn.size() <= 128 && active_turn_matches(turn) &&
      exact_uint64(generation, generation_id) && generation_id > 0 &&
      cJSON_IsObject(payload);
  if (!base_valid) {
    enqueue_event(BackendEventType::error, "INVALID CONFIRMATION CONTROL");
    cJSON_Delete(root);
    return true;
  }

  if (type == protocol::ControlType::capability_cancel) {
    if (!has_only_fields(payload, {"reason"}) ||
        json_string(payload, "reason").empty()) {
      enqueue_event(BackendEventType::error, "INVALID CONFIRMATION CANCEL");
      cJSON_Delete(root);
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
    cJSON_Delete(root);
    return true;
  }

  if (!has_only_fields(payload, {"name", "version", "arguments", "deadline_ms"}) ||
      json_string(payload, "name") != kConfirmationName ||
      json_string(payload, "version") != kConfirmationVersion) {
    enqueue_event(BackendEventType::error, "UNSUPPORTED CONFIRMATION");
    cJSON_Delete(root);
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
    cJSON_Delete(root);
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
  cJSON_Delete(root);
  return true;
}

} // namespace companion

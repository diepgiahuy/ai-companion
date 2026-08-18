#include "companion/websocket_voice_backend.hpp"
#include "companion/capability_dispatch.hpp"

#include "cJSON.h"
#include "esp_random.h"
#include "psa/crypto.h"

#include <array>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <string>
#include <string_view>

namespace companion {
namespace {
constexpr uint8_t kTextOpcode = 0x1;
constexpr uint8_t kBinaryOpcode = 0x2;
constexpr char kBase32[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

std::string_view fixed_view(const char* data, size_t capacity) {
  size_t length = 0;
  while (length < capacity && data[length] != '\0') ++length;
  return {data, length};
}

template <size_t N>
bool copy_fixed(std::array<char, N>& destination, std::string_view value) {
  if (value.empty() || value.size() >= destination.size()) return false;
  destination.fill('\0');
  std::memcpy(destination.data(), value.data(), value.size());
  return true;
}

std::string_view json_string(const cJSON* object, const char* name) {
  if (object == nullptr) return {};
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, name);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return {};
  return item->valuestring;
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

bool valid_pairing_alias(std::string_view value) {
  if (value.size() != 19 || !value.starts_with("CP-")) return false;
  for (const char c : value.substr(3)) {
    if ((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) continue;
    return false;
  }
  return true;
}

bool random_id(std::array<char, 64>& output, std::string_view prefix) {
  if (prefix.size() + 32 >= output.size()) return false;
  std::array<uint8_t, 16> bytes{};
  esp_fill_random(bytes.data(), bytes.size());
  static constexpr char hex[] = "0123456789abcdef";
  output.fill('\0');
  std::memcpy(output.data(), prefix.data(), prefix.size());
  size_t cursor = prefix.size();
  for (uint8_t byte : bytes) {
    output[cursor++] = hex[(byte >> 4) & 0x0f];
    output[cursor++] = hex[byte & 0x0f];
  }
  return true;
}

bool format_now_rfc3339(std::array<char, 36>& output) {
  const std::time_t now = std::time(nullptr);
  if (now < 1'577'836'800) return false;
  std::tm utc{};
  if (gmtime_r(&now, &utc) == nullptr) return false;
  output.fill('\0');
  return std::strftime(output.data(), output.size(), "%Y-%m-%dT%H:%M:%SZ", &utc) > 0;
}

int64_t days_from_civil(int year, unsigned month, unsigned day) {
  year -= month <= 2;
  const int era = (year >= 0 ? year : year - 399) / 400;
  const unsigned yoe = static_cast<unsigned>(year - era * 400);
  const unsigned doy = (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1;
  const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  return static_cast<int64_t>(era) * 146097 + static_cast<int64_t>(doe) - 719468;
}

bool parse_rfc3339_ms(std::string_view value, uint64_t& output) {
  if (value.size() < 20) return false;
  int year = 0, month = 0, day = 0, hour = 0, minute = 0, second = 0;
  if (std::sscanf(std::string(value.substr(0, 19)).c_str(),
                  "%4d-%2d-%2dT%2d:%2d:%2d",
                  &year, &month, &day, &hour, &minute, &second) != 6) {
    return false;
  }
  if (year < 1970 || month < 1 || month > 12 || day < 1 || day > 31 ||
      hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 60) {
    return false;
  }
  size_t cursor = 19;
  unsigned millis = 0;
  if (cursor < value.size() && value[cursor] == '.') {
    ++cursor;
    unsigned scale = 100;
    size_t digits = 0;
    while (cursor < value.size() && value[cursor] >= '0' && value[cursor] <= '9') {
      if (digits < 3) {
        millis += static_cast<unsigned>(value[cursor] - '0') * scale;
        scale /= 10;
      }
      ++digits;
      ++cursor;
    }
    if (digits == 0) return false;
  }
  if (cursor + 1 != value.size() || value[cursor] != 'Z') return false;
  const int64_t days = days_from_civil(year, static_cast<unsigned>(month), static_cast<unsigned>(day));
  if (days < 0) return false;
  const uint64_t seconds = static_cast<uint64_t>(days) * 86'400ULL +
                           static_cast<uint64_t>(hour) * 3'600ULL +
                           static_cast<uint64_t>(minute) * 60ULL +
                           static_cast<uint64_t>(second);
  output = seconds * 1'000ULL + millis;
  return true;
}

bool encode_base32_10(const uint8_t* input, std::array<char, 20>& output) {
  if (input == nullptr) return false;
  output.fill('\0');
  output[0] = 'C'; output[1] = 'P'; output[2] = '-';
  uint32_t accumulator = 0;
  int bits = 0;
  size_t cursor = 3;
  for (size_t index = 0; index < 10; ++index) {
    accumulator = (accumulator << 8) | input[index];
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      if (cursor >= 19) return false;
      output[cursor++] = kBase32[(accumulator >> bits) & 0x1f];
    }
  }
  if (bits != 0 || cursor != 19) return false;
  output[cursor] = '\0';
  return true;
}

bool hmac_sha256(std::string_view key, std::string_view message,
                 std::array<uint8_t, 32>& output) {
  if (key.empty() || message.empty() || psa_crypto_init() != PSA_SUCCESS) return false;

  psa_key_attributes_t attributes = PSA_KEY_ATTRIBUTES_INIT;
  psa_set_key_type(&attributes, PSA_KEY_TYPE_HMAC);
  psa_set_key_usage_flags(&attributes, PSA_KEY_USAGE_SIGN_MESSAGE);
  psa_set_key_algorithm(&attributes, PSA_ALG_HMAC(PSA_ALG_SHA_256));

  psa_key_id_t key_id = 0;
  const psa_status_t import_status = psa_import_key(
      &attributes, reinterpret_cast<const uint8_t*>(key.data()), key.size(), &key_id);
  psa_reset_key_attributes(&attributes);
  if (import_status != PSA_SUCCESS) return false;

  size_t output_length = 0;
  const psa_status_t mac_status = psa_mac_compute(
      key_id, PSA_ALG_HMAC(PSA_ALG_SHA_256),
      reinterpret_cast<const uint8_t*>(message.data()), message.size(),
      output.data(), output.size(), &output_length);
  const psa_status_t destroy_status = psa_destroy_key(key_id);
  return mac_status == PSA_SUCCESS && destroy_status == PSA_SUCCESS &&
         output_length == output.size();
}

bool validate_mux_envelope(const cJSON* root,
                           std::string_view expected_session,
                           const cJSON*& payload,
                           std::string_view& message_id) {
  if (!cJSON_IsObject(root)) return false;
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(root, "version");
  const cJSON* payload_item = cJSON_GetObjectItemCaseSensitive(root, "payload");
  if (!cJSON_IsNumber(version) || version->valuedouble != 2.0 || !cJSON_IsObject(payload_item)) {
    return false;
  }
  message_id = json_string(root, "message_id");
  const std::string_view session_id = json_string(root, "session_id");
  if (message_id.empty() || message_id.size() > 256 ||
      expected_session.empty() || session_id != expected_session) {
    return false;
  }
  payload = payload_item;
  return true;
}
} // namespace

std::string_view PairingBackendEvent::session_id_view() const {
  return fixed_view(pairing_session_id.data(), pairing_session_id.size());
}

std::string_view PairingBackendEvent::confirmation_nonce_view() const {
  return fixed_view(confirmation_nonce.data(), confirmation_nonce.size());
}

std::string_view PairingBackendEvent::reason_view() const {
  return fixed_view(reason.data(), reason.size());
}

bool WebSocketVoiceBackend::enable_pairing_protocol() {
  if (pairing_protocol_enabled_.load()) return true;
  if (client_ == nullptr || client_started_.load()) return false;
  pairing_event_queue_ = xQueueCreateStatic(
      kPairingEventQueueCapacity, sizeof(PairingBackendEvent),
      pairing_event_queue_buffer_.data(), &pairing_event_queue_storage_);
  if (pairing_event_queue_ == nullptr) return false;

  if (!confirmation_protocol_enabled_.load()) {
    if (esp_websocket_unregister_events(client_, WEBSOCKET_EVENT_ANY,
                                        &WebSocketVoiceBackend::event_handler) != ESP_OK) {
      pairing_event_queue_ = nullptr;
      return false;
    }
    if (esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                      &WebSocketVoiceBackend::pairing_event_handler,
                                      this) != ESP_OK) {
      (void)esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                          &WebSocketVoiceBackend::event_handler,
                                          this);
      pairing_event_queue_ = nullptr;
      return false;
    }
  }
  pairing_protocol_enabled_.store(true);
  return true;
}

bool WebSocketVoiceBackend::pairing_discovery_alias(std::array<char, 20>& output) {
  output.fill('\0');
  if (!pairing_protocol_enabled_.load() || !protocol_connected_.load()) return false;
  const std::array<char, 64> snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(snapshot.data(), snapshot.size());
  const std::time_t now = std::time(nullptr);
  if (session.size() < 8 || now < 1'577'836'800) return false;

  std::array<char, 64> message{};
  const long long slot = static_cast<long long>(now / 30);
  const int length = std::snprintf(message.data(), message.size(),
                                   "companion-pairing-v1:%lld", slot);
  if (length <= 0 || static_cast<size_t>(length) >= message.size()) return false;
  std::array<uint8_t, 32> digest{};
  if (!hmac_sha256(session, {message.data(), static_cast<size_t>(length)}, digest)) {
    return false;
  }
  return encode_base32_10(digest.data(), output);
}

bool WebSocketVoiceBackend::create_pairing_session(
    std::string_view candidate_discovery_id,
    std::string_view proximity_evidence_id) {
  if (!valid_pairing_alias(candidate_discovery_id) ||
      proximity_evidence_id.empty() || proximity_evidence_id.size() > 256) {
    return false;
  }
  char candidate[64]{};
  char evidence[600]{};
  size_t candidate_size = 0, evidence_size = 0;
  if (!protocol::encode_json_string(candidate_discovery_id, candidate, candidate_size) ||
      !protocol::encode_json_string(proximity_evidence_id, evidence, evidence_size)) {
    return false;
  }
  std::array<char, 768> payload{};
  const int length = std::snprintf(
      payload.data(), payload.size(),
      "{\"candidate_discovery_id\":%.*s,\"proximity_evidence_id\":%.*s}",
      static_cast<int>(candidate_size), candidate,
      static_cast<int>(evidence_size), evidence);
  return length > 0 && static_cast<size_t>(length) < payload.size() &&
         send_pairing_control(protocol::ControlType::pairing_session_create,
                              {payload.data(), static_cast<size_t>(length)});
}

bool WebSocketVoiceBackend::confirm_pairing_session(
    std::string_view pairing_session_id, std::string_view confirmation_nonce) {
  if (pairing_session_id.empty() || pairing_session_id.size() > 128 ||
      confirmation_nonce.size() < 16 || confirmation_nonce.size() > 256) {
    return false;
  }
  char session[300]{};
  char nonce[600]{};
  size_t session_size = 0, nonce_size = 0;
  if (!protocol::encode_json_string(pairing_session_id, session, session_size) ||
      !protocol::encode_json_string(confirmation_nonce, nonce, nonce_size)) {
    return false;
  }
  std::array<char, 1'024> payload{};
  const int length = std::snprintf(
      payload.data(), payload.size(),
      "{\"session_id\":%.*s,\"confirmation_nonce\":%.*s}",
      static_cast<int>(session_size), session,
      static_cast<int>(nonce_size), nonce);
  return length > 0 && static_cast<size_t>(length) < payload.size() &&
         send_pairing_control(protocol::ControlType::pairing_confirmation,
                              {payload.data(), static_cast<size_t>(length)});
}

bool WebSocketVoiceBackend::reject_pairing_session(std::string_view pairing_session_id) {
  if (pairing_session_id.empty() || pairing_session_id.size() > 128) return false;
  char session[300]{};
  size_t session_size = 0;
  if (!protocol::encode_json_string(pairing_session_id, session, session_size)) return false;
  std::array<char, 420> payload{};
  const int length = std::snprintf(
      payload.data(), payload.size(),
      "{\"session_id\":%.*s,\"reason\":\"user_declined\"}",
      static_cast<int>(session_size), session);
  return length > 0 && static_cast<size_t>(length) < payload.size() &&
         send_pairing_control(protocol::ControlType::pairing_rejected,
                              {payload.data(), static_cast<size_t>(length)});
}

bool WebSocketVoiceBackend::poll_pairing_event(PairingBackendEvent& event) {
  return pairing_event_queue_ != nullptr &&
         xQueueReceive(pairing_event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_pairing_event(const PairingBackendEvent& event) {
  if (pairing_event_queue_ == nullptr) return false;
  if (xQueueSend(pairing_event_queue_, &event, 0) == pdPASS) return true;
  xQueueReset(pairing_event_queue_);
  PairingBackendEvent disconnected{};
  disconnected.type = PairingBackendEventType::disconnected;
  (void)xQueueSend(pairing_event_queue_, &disconnected, 0);
  return false;
}

bool WebSocketVoiceBackend::send_pairing_control(protocol::ControlType type,
                                                 std::string_view payload_json) {
  if (!pairing_protocol_enabled_.load() || !protocol_connected_.load() ||
      payload_json.empty()) {
    return false;
  }
  const std::array<char, 64> session_snapshot = session_id_snapshot();
  const std::string_view session = fixed_view(session_snapshot.data(), session_snapshot.size());
  if (session.empty()) return false;

  std::array<char, 64> message_id{};
  std::array<char, 64> idempotency_key{};
  std::array<char, 36> occurred_at{};
  if (!random_id(message_id, "pair-msg-") ||
      !random_id(idempotency_key, "pair-idem-") ||
      !format_now_rfc3339(occurred_at)) {
    return false;
  }
  std::array<char, 2'048> encoded{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = type,
      .message_id = message_id.data(),
      .payload_json = payload_json,
      .correlation_id = {},
      .session_id = session,
      .turn_id = {},
      .generation_id = 0,
      .has_generation_id = false,
      .idempotency_key = idempotency_key.data(),
      .occurred_at = occurred_at.data(),
  };
  if (!protocol::encode(envelope, encoded, written)) return false;
  return send_text({encoded.data(), written});
}

void WebSocketVoiceBackend::pairing_event_handler(
    void* context, esp_event_base_t, int32_t event_id, void* event_data) {
  auto* self = static_cast<WebSocketVoiceBackend*>(context);
  if (self == nullptr) return;
  self->on_pairing_event(event_id,
                         static_cast<esp_websocket_event_data_t*>(event_data));
}

void WebSocketVoiceBackend::on_pairing_event(
    int32_t event_id, esp_websocket_event_data_t* data) {
  if (event_id == WEBSOCKET_EVENT_DISCONNECTED) {
    if (pairing_protocol_enabled_.load()) {
      PairingBackendEvent event{};
      event.type = PairingBackendEventType::disconnected;
      (void)enqueue_pairing_event(event);
    }
    confirmation_advertised_.store(false);
    clear_user_confirmation();
    on_event(event_id, data);
    return;
  }
  if (event_id == WEBSOCKET_EVENT_CONNECTED) {
    confirmation_advertised_.store(false);
    clear_user_confirmation();
    on_event(event_id, data);
    return;
  }
  if (event_id != WEBSOCKET_EVENT_DATA) {
    on_event(event_id, data);
    return;
  }
  if (data == nullptr || data->data_ptr == nullptr || data->data_len < 0) return;
  if (data->payload_offset == 0) receive_opcode_ = data->op_code;
  if (receive_opcode_ == kTextOpcode) {
    const size_t offset = static_cast<size_t>(data->payload_offset);
    const size_t length = static_cast<size_t>(data->data_len);
    if (offset + length >= text_payload_.size()) {
      enqueue_event(BackendEventType::error, "CONTROL TOO LARGE");
      text_payload_size_ = 0;
      return;
    }
    std::copy_n(data->data_ptr, length, text_payload_.begin() + offset);
    text_payload_size_ = offset + length;
    if (text_payload_size_ == static_cast<size_t>(data->payload_len)) {
      text_payload_[text_payload_size_] = '\0';
      const std::string_view text{text_payload_.data(), text_payload_size_};
      if (!handle_pairing_text(text)) handle_text(text);
      text_payload_size_ = 0;
    }
  } else if (receive_opcode_ == kBinaryOpcode) {
    handle_binary(*data);
  }
}

bool WebSocketVoiceBackend::handle_pairing_text(std::string_view json) {
  cJSON* root = cJSON_ParseWithLength(json.data(), json.size());
  if (root == nullptr || !cJSON_IsObject(root)) {
    if (root != nullptr) cJSON_Delete(root);
    return false;
  }
  protocol::ControlType type{};
  const std::string_view type_name = json_string(root, "type");
  if (!protocol::parse_type(type_name, type)) {
    cJSON_Delete(root);
    return false;
  }

  const bool capability_control =
      type == protocol::ControlType::capability_call ||
      type == protocol::ControlType::capability_cancel;
  const bool pairing_control =
      type == protocol::ControlType::pairing_session_created ||
      type == protocol::ControlType::pairing_succeeded ||
      type == protocol::ControlType::pairing_rejected ||
      type == protocol::ControlType::pairing_expired;
  if (!capability_control && !pairing_control) {
    cJSON_Delete(root);
    return false;
  }

  const std::array<char, 64> session_snapshot = session_id_snapshot();
  const std::string_view expected_session = fixed_view(session_snapshot.data(), session_snapshot.size());
  const cJSON* payload = nullptr;
  std::string_view message_id;
  if (!validate_mux_envelope(root, expected_session, payload, message_id)) {
    enqueue_event(BackendEventType::error,
                  capability_control ? "INVALID CAPABILITY CONTROL"
                                     : "INVALID PAIRING CONTROL");
    cJSON_Delete(root);
    return true;
  }

  if (capability_control) {
    const CapabilityRegistry registry =
        make_capability_registry(confirmation_protocol_enabled_.load());
    const std::string_view capability_name =
        type == protocol::ControlType::capability_call ? json_string(payload, "name") : std::string_view{};
    const std::string_view capability_version =
        type == protocol::ControlType::capability_call ? json_string(payload, "version") : std::string_view{};

    CapabilityDispatch dispatch = CapabilityDispatch::ignored_cancel;
    if (type == protocol::ControlType::capability_call) {
      dispatch = select_capability_call(registry, capability_name,
                                        capability_version);
    } else {
      const std::string_view correlation = json_string(root, "correlation_id");
      const std::string_view turn = json_string(root, "turn_id");
      const cJSON* generation =
          cJSON_GetObjectItemCaseSensitive(root, "generation_id");
      uint64_t generation_id = 0;
      (void)exact_uint64(generation, generation_id);

      UserConfirmationRequest active_confirmation{};
      bool confirmation_active = false;
      taskENTER_CRITICAL(&confirmation_lock_);
      if (confirmation_active_) {
        active_confirmation = active_confirmation_;
        confirmation_active = true;
      }
      taskEXIT_CRITICAL(&confirmation_lock_);

      const CapabilityDefinition* confirmation = registry.find(
          kUserConfirmationCapability, kUserConfirmationCapabilityVersion);
      const PendingCapabilityOperation pending{
          .active = confirmation_active && confirmation != nullptr,
          .handler = CapabilityHandler::user_confirmation,
          .cancelable = confirmation != nullptr && confirmation->cancelable,
          .correlation_id = active_confirmation.correlation_id_view(),
          .turn_id = active_confirmation.turn_id_view(),
          .generation_id = active_confirmation.generation_id,
      };
      dispatch =
          select_capability_cancel(pending, correlation, turn, generation_id);
    }

    switch (dispatch) {
    case CapabilityDispatch::user_confirmation_call:
      (void)handle_confirmation_call(root, payload);
      break;
    case CapabilityDispatch::user_confirmation_cancel:
      (void)handle_confirmation_cancel(root, payload);
      break;
    case CapabilityDispatch::settings_call:
      (void)handle_settings_call(root, payload);
      break;
    case CapabilityDispatch::unsupported_call: {
      const std::string_view correlation = json_string(root, "correlation_id");
      const std::string_view turn = json_string(root, "turn_id");
      const cJSON* generation = cJSON_GetObjectItemCaseSensitive(root, "generation_id");
      uint64_t generation_id = 0;
      const bool valid = !correlation.empty() && correlation.size() <= 128 &&
                         !turn.empty() && turn.size() <= 128 && active_turn_matches(turn) &&
                         exact_uint64(generation, generation_id) && generation_id > 0 &&
                         !capability_name.empty() && capability_name.size() <= 96 &&
                         !capability_version.empty() && capability_version.size() <= 32;
      if (!valid || !enqueue_unsupported_capability_result(
                        correlation, turn, generation_id,
                        capability_name, capability_version)) {
        enqueue_event(BackendEventType::error, "INVALID CAPABILITY CONTROL");
      }
      break;
    }
    case CapabilityDispatch::ignored_cancel:
      break;
    case CapabilityDispatch::not_capability:
      cJSON_Delete(root);
      return false;
    }
    cJSON_Delete(root);
    return true;
  }

  if (!pairing_protocol_enabled_.load()) {
    cJSON_Delete(root);
    return false;
  }
  PairingBackendEvent event{};
  const std::string_view pairing_session_id = json_string(payload, "session_id");
  bool valid = copy_fixed(event.pairing_session_id, pairing_session_id);
  switch (type) {
  case protocol::ControlType::pairing_session_created: {
    event.type = PairingBackendEventType::session_created;
    const std::string_view expires_at = json_string(payload, "expires_at");
    valid = valid && message_id.size() >= 16 && message_id.size() <= 256 &&
            copy_fixed(event.confirmation_nonce, message_id) &&
            parse_rfc3339_ms(expires_at, event.expires_at_unix_ms);
    break;
  }
  case protocol::ControlType::pairing_succeeded:
    event.type = PairingBackendEventType::succeeded;
    break;
  case protocol::ControlType::pairing_rejected: {
    event.type = PairingBackendEventType::rejected;
    const std::string_view reason = json_string(payload, "reason");
    valid = valid && !reason.empty() && reason.size() <= 64 &&
            copy_fixed(event.reason, reason);
    break;
  }
  case protocol::ControlType::pairing_expired:
    event.type = PairingBackendEventType::expired;
    break;
  default:
    valid = false;
    break;
  }
  if (!valid || !enqueue_pairing_event(event)) {
    enqueue_event(BackendEventType::error, "INVALID PAIRING CONTROL");
  }
  cJSON_Delete(root);
  return true;
}

} // namespace companion
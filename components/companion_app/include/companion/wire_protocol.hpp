#pragma once

#include <cstddef>
#include <cstdint>
#include <span>
#include <string_view>

namespace companion::protocol {

constexpr uint32_t kVersion = 2;

enum class ControlType : uint8_t {
  session_hello,
  session_ready,
  session_ping,
  session_pong,
  turn_listen,
  turn_abort,
  turn_state,
  transcript_final,
  tts_lifecycle,
  agent_status,
  ui_card,
  ui_state,
  alarm_fired,
  alarm_ack,
  schedule_updated,
  protocol_error,
  capability_advertise,
  capability_call,
  capability_result,
  capability_cancel,
  gesture_notification,
  voice_mail_available,
  voice_mail_claim,
  voice_mail_claimed,
  voice_mail_playback_result,
  voice_mail_consumed,
  voice_mail_expired,
  pairing_session_create,
  pairing_session_created,
  pairing_confirmation,
  pairing_succeeded,
  pairing_rejected,
  pairing_expired,
};

std::string_view type_name(ControlType type);
bool parse_type(std::string_view name, ControlType& type);

struct Envelope {
  ControlType type{};
  std::string_view message_id;
  std::string_view payload_json;
  std::string_view correlation_id;
  std::string_view session_id;
  std::string_view turn_id;
  uint64_t generation_id{};
  bool has_generation_id{};
  std::string_view idempotency_key;
  std::string_view occurred_at;
};

// Encodes the canonical v2 control envelope into a caller-owned fixed buffer.
// Payload must be a complete JSON object; metadata strings are escaped here.
bool encode(const Envelope& envelope, std::span<char> output, size_t& written);
bool encode_json_string(std::string_view value, std::span<char> output, size_t& written);

} // namespace companion::protocol

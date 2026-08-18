#include "companion/wire_protocol.hpp"

#include <array>
#include <cassert>
#include <fstream>
#include <iostream>
#include <string>

namespace {

constexpr std::array<companion::protocol::ControlType, 29> kTypes{
    companion::protocol::ControlType::session_hello,
    companion::protocol::ControlType::session_ready,
    companion::protocol::ControlType::session_ping,
    companion::protocol::ControlType::session_pong,
    companion::protocol::ControlType::turn_listen,
    companion::protocol::ControlType::turn_abort,
    companion::protocol::ControlType::turn_state,
    companion::protocol::ControlType::transcript_final,
    companion::protocol::ControlType::tts_lifecycle,
    companion::protocol::ControlType::agent_status,
    companion::protocol::ControlType::ui_card,
    companion::protocol::ControlType::ui_state,
    companion::protocol::ControlType::alarm_fired,
    companion::protocol::ControlType::alarm_ack,
    companion::protocol::ControlType::schedule_updated,
    companion::protocol::ControlType::protocol_error,
    companion::protocol::ControlType::gesture_notification,
    companion::protocol::ControlType::voice_mail_available,
    companion::protocol::ControlType::voice_mail_claim,
    companion::protocol::ControlType::voice_mail_claimed,
    companion::protocol::ControlType::voice_mail_playback_result,
    companion::protocol::ControlType::voice_mail_consumed,
    companion::protocol::ControlType::voice_mail_expired,
    companion::protocol::ControlType::pairing_session_create,
    companion::protocol::ControlType::pairing_session_created,
    companion::protocol::ControlType::pairing_confirmation,
    companion::protocol::ControlType::pairing_succeeded,
    companion::protocol::ControlType::pairing_rejected,
    companion::protocol::ControlType::pairing_expired,
};

} // namespace

int main() {
  const std::string path = std::string(COMPANION_SOURCE_DIR) +
                           "/testdata/protocol/v2/golden_envelopes.ndjson";
  std::ifstream fixture(path);
  assert(fixture.good());

  std::array<char, 1024> output{};
  std::string expected;
  size_t index = 0;
  while (std::getline(fixture, expected)) {
    assert(index < kTypes.size());
    constexpr std::string_view kPayloadMarker = "\"payload\":";
    const size_t marker = expected.find(kPayloadMarker);
    assert(marker != std::string::npos);
    assert(!expected.empty() && expected.back() == '}');
    const size_t payload_start = marker + kPayloadMarker.size();
    const std::string payload_json =
        expected.substr(payload_start, expected.size() - payload_start - 1);

    size_t written = 0;
    const companion::protocol::Envelope envelope{
        .type = kTypes[index],
        .message_id = "golden",
        .payload_json = payload_json,
        .correlation_id = {},
        .session_id = {},
        .turn_id = {},
        .generation_id = 0,
        .has_generation_id = false,
        .idempotency_key = {},
        .occurred_at = {},
    };
    assert(companion::protocol::encode(envelope, output, written));
    assert(std::string_view(output.data(), written) == expected);

    companion::protocol::ControlType parsed{};
    assert(companion::protocol::parse_type(companion::protocol::type_name(kTypes[index]), parsed));
    assert(parsed == kTypes[index]);
    ++index;
  }
  assert(index == kTypes.size());
  std::cout << "PASS: shared protocol v2 typed golden vectors\n";
}

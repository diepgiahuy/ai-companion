#!/usr/bin/env python3
from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one guarded target, found {count}")
    p.write_text(text.replace(old, new, 1), encoding="utf-8")


replace_once(
    "host/companion_software_device/main.cpp",
    '#include <boost/beast/http.hpp>\n#include <nlohmann/json.hpp>',
    '#include <boost/beast/http.hpp>\n#include <boost/beast/websocket.hpp>\n#include <nlohmann/json.hpp>',
)

replace_once(
    "host/companion_software_device/main.cpp",
    '  ScenarioResult result{.id = std::move(id)};\n',
    '  ScenarioResult result{};\n  result.id = std::move(id);\n',
)

replace_once(
    "host/companion_software_device/websocket_backend.cpp",
    '''bool WebSocketVoiceBackend::begin_turn(uint64_t, ListenMode mode) {
  if (!protocol_connected_.load()) return false;
  std::string turn_id = "host-turn-" + std::to_string(turn_sequence_.fetch_add(1) + 1);
  std::string wire;
  {
    std::lock_guard lock(state_mutex_);
    if (turn_active_) return false;
    active_turn_id_ = turn_id;
    turn_active_ = true;
    tts_active_ = false;
    clear_turn_media_locked();
    json payload{{"state", "start"},
                 {"mode", mode == ListenMode::auto_vad ? "auto_vad" : "manual"}};
    wire = encode_control(static_cast<int>(protocol::ControlType::turn_listen),
                          payload.dump(), turn_id);
    last_begin_wire_ = wire;
    ++stats_.turns_started;
  }
  return send_text(std::move(wire));
}
''',
    '''bool WebSocketVoiceBackend::begin_turn(uint64_t, ListenMode mode) {
  if (!protocol_connected_.load()) return false;
  const std::string turn_id =
      "host-turn-" + std::to_string(turn_sequence_.fetch_add(1) + 1);
  {
    std::lock_guard lock(state_mutex_);
    if (turn_active_) return false;
    active_turn_id_ = turn_id;
    turn_active_ = true;
    tts_active_ = false;
    clear_turn_media_locked();
    ++stats_.turns_started;
  }
  json payload{{"state", "start"},
               {"mode", mode == ListenMode::auto_vad ? "auto_vad" : "manual"}};
  std::string wire = encode_control(
      static_cast<int>(protocol::ControlType::turn_listen), payload.dump(), turn_id);
  if (wire.empty()) {
    std::lock_guard lock(state_mutex_);
    turn_active_ = false;
    return false;
  }
  {
    std::lock_guard lock(state_mutex_);
    last_begin_wire_ = wire;
  }
  return send_text(std::move(wire));
}
''',
)

#!/usr/bin/env python3
from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one target, found {count}")
    p.write_text(text.replace(old, new, 1))


replace_once(
    "backend/internal/server/server.go",
    '''\toutcome := action()
\tconst maximumRememberedMessages = 256
\tif len(s.seenOrder) == maximumRememberedMessages {
\t\tdelete(s.seenInbound, s.seenOrder[0])
\t\tcopy(s.seenOrder, s.seenOrder[1:])
\t\ts.seenOrder = s.seenOrder[:maximumRememberedMessages-1]
\t}
\ts.seenInbound[messageID] = inboundRecord{digest: digest, outcome: outcome}
\ts.seenOrder = append(s.seenOrder, messageID)
\treturn outcome
''',
    '''\toutcome := action()
\tif outcome != nil {
\t\t// Failed actions are not replay records: transient pre-commit failures
\t\t// must be retryable with the same message_id in the live session.
\t\treturn outcome
\t}
\tconst maximumRememberedMessages = 256
\tif len(s.seenOrder) == maximumRememberedMessages {
\t\tdelete(s.seenInbound, s.seenOrder[0])
\t\tcopy(s.seenOrder, s.seenOrder[1:])
\t\ts.seenOrder = s.seenOrder[:maximumRememberedMessages-1]
\t}
\ts.seenInbound[messageID] = inboundRecord{digest: digest}
\ts.seenOrder = append(s.seenOrder, messageID)
\treturn nil
''',
)

replace_once(
    "backend/internal/protocol/envelope.go",
    '''func (p ReadyPayload) Validate() error {
\tif p.Transport != Transport {
\t\treturn fmt.Errorf("unsupported transport %q", p.Transport)
\t}
\tif p.AudioParams != DownlinkAudioParams() {
\t\treturn fmt.Errorf("unsupported audio params: got %+v, want %+v", p.AudioParams, DownlinkAudioParams())
\t}
\tif p.ConfigVersion < 0 {
\t\treturn fmt.Errorf("config_version must be non-negative")
\t}
\tif p.Config != nil {
\t\treturn p.Config.ValidateDeviceSnapshot()
\t}
\treturn nil
}
''',
    '''func validateConfigVersion(version int64) error {
\tconst maximumExactJSONInteger int64 = 9_007_199_254_740_991
\tif version < 0 || version > maximumExactJSONInteger {
\t\treturn fmt.Errorf("config_version must be within 0..%d", maximumExactJSONInteger)
\t}
\treturn nil
}

func (p ReadyPayload) Validate() error {
\tif p.Transport != Transport {
\t\treturn fmt.Errorf("unsupported transport %q", p.Transport)
\t}
\tif p.AudioParams != DownlinkAudioParams() {
\t\treturn fmt.Errorf("unsupported audio params: got %+v, want %+v", p.AudioParams, DownlinkAudioParams())
\t}
\tif err := validateConfigVersion(p.ConfigVersion); err != nil {
\t\treturn err
\t}
\tif p.Config != nil {
\t\treturn p.Config.ValidateDeviceSnapshot()
\t}
\treturn nil
}
''',
)

replace_once(
    "backend/internal/protocol/envelope.go",
    '''func (p ConfigReportPayload) Validate() error {
\tif p.ConfigVersion < 0 {
\t\treturn fmt.Errorf("config_version must be non-negative")
\t}
\treturn p.Config.ValidateDeviceSnapshot()
}
''',
    '''func (p ConfigReportPayload) Validate() error {
\tif err := validateConfigVersion(p.ConfigVersion); err != nil {
\t\treturn err
\t}
\treturn p.Config.ValidateDeviceSnapshot()
}
''',
)

replace_once(
    "backend/internal/protocol/envelope.go",
    '''func (p ConfigUpdatePayload) Validate() error {
\tif p.ConfigVersion < 0 {
\t\treturn fmt.Errorf("config_version must be non-negative")
\t}
\treturn p.Config.ValidateDeviceSnapshot()
}
''',
    '''func (p ConfigUpdatePayload) Validate() error {
\tif err := validateConfigVersion(p.ConfigVersion); err != nil {
\t\treturn err
\t}
\treturn p.Config.ValidateDeviceSnapshot()
}
''',
)

replace_once(
    "backend/internal/protocol/envelope.go",
    '''func (p UICardPayload) Validate() error {
\tif p.UI == nil {
\t\treturn fmt.Errorf("ui is required")
\t}
\treturn nil
}
''',
    '''func (p UICardPayload) Validate() error {
\tif p.UI == nil {
\t\treturn fmt.Errorf("ui is required")
\t}
\traw, err := json.Marshal(p.UI)
\tif err != nil {
\t\treturn fmt.Errorf("encode ui: %w", err)
\t}
\ttrimmed := bytes.TrimSpace(raw)
\tif len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
\t\treturn fmt.Errorf("ui must be a JSON object")
\t}
\treturn nil
}
''',
)

replace_once(
    "components/esp32_network/src/websocket_voice_backend.cpp",
    '''bool optional_bounded_string(const cJSON* object, const char* key,
                             size_t maximum_size) {
  const cJSON* value = cJSON_GetObjectItemCaseSensitive(object, key);
  return value == nullptr ||
         (cJSON_IsString(value) && value->valuestring != nullptr &&
          std::strlen(value->valuestring) <= maximum_size);
}
''',
    '''bool optional_bounded_string(const cJSON* object, const char* key,
                             size_t maximum_size) {
  const cJSON* value = cJSON_GetObjectItemCaseSensitive(object, key);
  return value == nullptr ||
         (cJSON_IsString(value) && value->valuestring != nullptr &&
          std::strlen(value->valuestring) <= maximum_size);
}

bool bounded_nonempty_string(const cJSON* object, const char* key,
                             size_t maximum_size) {
  const std::string_view value = json_string(object, key);
  return !value.empty() && value.size() <= maximum_size;
}

bool string_in(std::string_view value,
               std::initializer_list<std::string_view> allowed) {
  return std::find(allowed.begin(), allowed.end(), value) != allowed.end();
}
''',
)

replace_once(
    "components/esp32_network/src/websocket_voice_backend.cpp",
    '''bool payload_semantics_valid(protocol::ControlType type, const cJSON* payload) {
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
''',
    '''bool payload_semantics_valid(protocol::ControlType type, const cJSON* payload) {
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
    return bounded_nonempty_string(payload, "reason", 64);
  case ControlType::turn_state: {
    const std::string_view state = json_string(payload, "state");
    if (!string_in(state, {"listening", "processing", "speaking", "completed", "interrupted"})) return false;
    const cJSON* reason_item = cJSON_GetObjectItemCaseSensitive(payload, "reason");
    const std::string_view reason = json_string(payload, "reason");
    if (state == "interrupted") return !reason.empty() && reason.size() <= 64;
    return reason_item == nullptr ||
           (cJSON_IsString(reason_item) && reason_item->valuestring != nullptr && reason.empty());
  }
  case ControlType::transcript_final:
    return nonempty("text");
  case ControlType::tts_lifecycle: {
    const std::string_view state = json_string(payload, "state");
    const std::string_view text = json_string(payload, "text");
    if (state == "start" || state == "stop") return text.empty();
    return (state == "sentence_start" || state == "sentence_end") && !text.empty();
  }
  case ControlType::agent_status:
    return bounded_nonempty_string(payload, "state", 64);
  case ControlType::ui_card:
    return json_object(payload, "ui") != nullptr;
  case ControlType::ui_state: {
    const std::string_view emotion = json_string(payload, "emotion");
    if (!string_in(emotion, {"idle", "listening", "thinking", "speaking", "tool_executing", "interrupted", "error"})) return false;
    const cJSON* tool_item = cJSON_GetObjectItemCaseSensitive(payload, "tool_name");
    const std::string_view tool_name = json_string(payload, "tool_name");
    if (emotion == "tool_executing") return !tool_name.empty();
    return tool_item == nullptr ||
           (cJSON_IsString(tool_item) && tool_item->valuestring != nullptr && tool_name.empty());
  }
  case ControlType::alarm_fired:
    return bounded_nonempty_string(payload, "alarm_id", 128) &&
           bounded_nonempty_string(payload, "message", 512) && nonempty("fire_at");
  case ControlType::schedule_updated:
    return bounded_nonempty_string(payload, "message", 512) && nonempty("fire_at");
  case ControlType::config_update: {
    RuntimeConfigPatch ignored{};
    return parse_runtime_config(payload, ignored);
  }
  case ControlType::protocol_error:
    return bounded_nonempty_string(payload, "code", 64) &&
           bounded_nonempty_string(payload, "message", 1024);
  default:
    return true;
  }
}
''',
)

#include "companion/wire_protocol.hpp"

#include <array>

namespace companion::protocol {
namespace {

constexpr std::array<std::string_view, 35> kTypeNames{
    "session.hello", "session.ready", "session.ping", "session.pong",
    "turn.listen", "turn.abort", "turn.state", "transcript.final",
    "tts.lifecycle", "agent.status", "ui.card", "ui.state", "alarm.fired",
    "alarm.ack", "schedule.updated", "config.update", "config.report",
    "protocol.error",
    "capability.advertise", "capability.call", "capability.result",
    "capability.cancel",
    "gesture.notification", "voice_mail.available", "voice_mail.claim",
    "voice_mail.claimed", "voice_mail.playback_result", "voice_mail.consumed",
    "voice_mail.expired", "pairing.session_create", "pairing.session_created",
    "pairing.confirmation", "pairing.succeeded", "pairing.rejected",
    "pairing.expired",
};

class Writer final {
public:
  explicit Writer(std::span<char> output) : output_(output) {}

  bool append(std::string_view text) {
    if (text.size() > output_.size() - size_) return false;
    for (char character : text) output_[size_++] = character;
    return true;
  }

  bool append_uint(uint64_t value) {
    char digits[20]{};
    size_t count = 0;
    do {
      digits[count++] = static_cast<char>('0' + value % 10);
      value /= 10;
    } while (value != 0 && count < sizeof(digits));
    if (value != 0 || count > output_.size() - size_) return false;
    while (count != 0) output_[size_++] = digits[--count];
    return true;
  }

  bool append_escaped(std::string_view text) {
    if (!append("\"")) return false;
    for (const unsigned char character : text) {
      switch (character) {
      case '\"': if (!append("\\\"")) return false; break;
      case '\\': if (!append("\\\\")) return false; break;
      case '\b': if (!append("\\b")) return false; break;
      case '\f': if (!append("\\f")) return false; break;
      case '\n': if (!append("\\n")) return false; break;
      case '\r': if (!append("\\r")) return false; break;
      case '\t': if (!append("\\t")) return false; break;
      default:
        if (character < 0x20) {
          constexpr char kHex[] = "0123456789abcdef";
          const char escaped[] = {'\\', 'u', '0', '0',
                                  kHex[character >> 4], kHex[character & 0x0f]};
          if (!append({escaped, sizeof(escaped)})) return false;
        } else {
          const char normal = static_cast<char>(character);
          if (!append({&normal, 1})) return false;
        }
      }
    }
    return append("\"");
  }

  size_t size() const { return size_; }

private:
  std::span<char> output_;
  size_t size_{};
};

bool is_json_object(std::string_view json) {
  constexpr size_t kMaximumJSONDepth = 16;
  class Parser final {
  public:
    Parser(std::string_view input, size_t maximum_depth)
        : input_(input), maximum_depth_(maximum_depth) {}

    bool parse_object_document() {
      skip_whitespace();
      if (!parse_object(0)) return false;
      skip_whitespace();
      return position_ == input_.size();
    }

  private:
    void skip_whitespace() {
      while (position_ < input_.size()) {
        const char value = input_[position_];
        if (value != ' ' && value != '\t' && value != '\n' && value != '\r') break;
        ++position_;
      }
    }

    bool consume(char expected) {
      if (position_ >= input_.size() || input_[position_] != expected) return false;
      ++position_;
      return true;
    }

    bool parse_value(size_t depth) {
      if (depth > maximum_depth_) return false;
      skip_whitespace();
      if (position_ >= input_.size()) return false;
      switch (input_[position_]) {
      case '{': return parse_object(depth);
      case '[': return parse_array(depth);
      case '"': return parse_string();
      case 't': return parse_literal("true");
      case 'f': return parse_literal("false");
      case 'n': return parse_literal("null");
      default: return parse_number();
      }
    }

    bool parse_object(size_t depth) {
      if (depth > maximum_depth_ || !consume('{')) return false;
      skip_whitespace();
      if (consume('}')) return true;
      while (true) {
        if (!parse_string()) return false;
        skip_whitespace();
        if (!consume(':') || !parse_value(depth + 1)) return false;
        skip_whitespace();
        if (consume('}')) return true;
        if (!consume(',')) return false;
        skip_whitespace();
      }
    }

    bool parse_array(size_t depth) {
      if (depth > maximum_depth_ || !consume('[')) return false;
      skip_whitespace();
      if (consume(']')) return true;
      while (true) {
        if (!parse_value(depth + 1)) return false;
        skip_whitespace();
        if (consume(']')) return true;
        if (!consume(',')) return false;
        skip_whitespace();
      }
    }

    bool parse_string() {
      if (!consume('"')) return false;
      while (position_ < input_.size()) {
        const unsigned char value = static_cast<unsigned char>(input_[position_++]);
        if (value == '"') return true;
        if (value < 0x20) return false;
        if (value != '\\') continue;
        if (position_ >= input_.size()) return false;
        const char escape = input_[position_++];
        if (escape == '"' || escape == '\\' || escape == '/' || escape == 'b' ||
            escape == 'f' || escape == 'n' || escape == 'r' || escape == 't') {
          continue;
        }
        if (escape != 'u' || position_ + 4 > input_.size()) return false;
        for (size_t index = 0; index < 4; ++index) {
          const char digit = input_[position_++];
          const bool hexadecimal = (digit >= '0' && digit <= '9') ||
                                   (digit >= 'a' && digit <= 'f') ||
                                   (digit >= 'A' && digit <= 'F');
          if (!hexadecimal) return false;
        }
      }
      return false;
    }

    bool parse_literal(std::string_view literal) {
      if (input_.substr(position_, literal.size()) != literal) return false;
      position_ += literal.size();
      return true;
    }

    bool parse_number() {
      const size_t start = position_;
      consume('-');
      if (consume('0')) {
        if (position_ < input_.size() && input_[position_] >= '0' &&
            input_[position_] <= '9') return false;
      } else {
        if (position_ >= input_.size() || input_[position_] < '1' ||
            input_[position_] > '9') return false;
        while (position_ < input_.size() && input_[position_] >= '0' &&
               input_[position_] <= '9') ++position_;
      }
      if (consume('.')) {
        const size_t fraction = position_;
        while (position_ < input_.size() && input_[position_] >= '0' &&
               input_[position_] <= '9') ++position_;
        if (position_ == fraction) return false;
      }
      if (position_ < input_.size() &&
          (input_[position_] == 'e' || input_[position_] == 'E')) {
        ++position_;
        if (position_ < input_.size() &&
            (input_[position_] == '+' || input_[position_] == '-')) ++position_;
        const size_t exponent = position_;
        while (position_ < input_.size() && input_[position_] >= '0' &&
               input_[position_] <= '9') ++position_;
        if (position_ == exponent) return false;
      }
      return position_ > start;
    }

    std::string_view input_;
    size_t maximum_depth_{};
    size_t position_{};
  };

  return Parser(json, kMaximumJSONDepth).parse_object_document();
}

bool append_string_field(Writer& writer, std::string_view key, std::string_view value) {
  return writer.append(",\"") && writer.append(key) && writer.append("\":") &&
         writer.append_escaped(value);
}

} // namespace

std::string_view type_name(ControlType type) {
  const size_t index = static_cast<size_t>(type);
  return index < kTypeNames.size() ? kTypeNames[index] : std::string_view{};
}

bool parse_type(std::string_view name, ControlType& type) {
  for (size_t index = 0; index < kTypeNames.size(); ++index) {
    if (kTypeNames[index] == name) {
      type = static_cast<ControlType>(index);
      return true;
    }
  }
  return false;
}

bool encode(const Envelope& envelope, std::span<char> output, size_t& written) {
  written = 0;
  const std::string_view name = type_name(envelope.type);
  if (name.empty() || envelope.message_id.empty() || !is_json_object(envelope.payload_json)) {
    return false;
  }

  Writer writer(output);
  if (!writer.append("{\"version\":2,\"type\":") || !writer.append_escaped(name) ||
      !writer.append(",\"message_id\":") || !writer.append_escaped(envelope.message_id)) {
    return false;
  }
  if (!envelope.correlation_id.empty() &&
      !append_string_field(writer, "correlation_id", envelope.correlation_id)) return false;
  if (!envelope.session_id.empty() &&
      !append_string_field(writer, "session_id", envelope.session_id)) return false;
  if (!envelope.turn_id.empty() &&
      !append_string_field(writer, "turn_id", envelope.turn_id)) return false;
  if (envelope.has_generation_id &&
      (!writer.append(",\"generation_id\":") || !writer.append_uint(envelope.generation_id))) {
    return false;
  }
  if (!envelope.idempotency_key.empty() &&
      !append_string_field(writer, "idempotency_key", envelope.idempotency_key)) return false;
  if (!envelope.occurred_at.empty() &&
      !append_string_field(writer, "occurred_at", envelope.occurred_at)) return false;
  if (!writer.append(",\"payload\":") || !writer.append(envelope.payload_json) ||
      !writer.append("}")) return false;
  written = writer.size();
  return true;
}

bool encode_json_string(std::string_view value, std::span<char> output, size_t& written) {
  written = 0;
  Writer writer(output);
  if (!writer.append_escaped(value)) return false;
  written = writer.size();
  return true;
}

} // namespace companion::protocol

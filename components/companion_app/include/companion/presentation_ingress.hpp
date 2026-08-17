#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>

namespace companion {

inline constexpr int kPresentationCardVersion = 1;
inline constexpr size_t kPresentationCardKindBytes = 32;
inline constexpr size_t kPresentationCardTitleBytes = 96;
inline constexpr size_t kPresentationCardTextBytes = 192;
inline constexpr size_t kPresentationHintTextBytes = 96;
inline constexpr size_t kAgentStatusBytes = 64;

namespace presentation_ingress {

inline bool contains_unsupported_json_nul(std::string_view json) {
  bool in_string = false;
  bool escaped = false;
  for (size_t index = 0; index < json.size(); ++index) {
    const char ch = json[index];
    if (ch == '\0') return true;
    if (!in_string) {
      if (ch == '"') in_string = true;
      continue;
    }
    if (escaped) {
      if (ch == 'u' && index + 4 < json.size() &&
          json.substr(index + 1, 4) == "0000") {
        return true;
      }
      escaped = false;
      continue;
    }
    if (ch == '\\') {
      escaped = true;
    } else if (ch == '"') {
      in_string = false;
    }
  }
  return false;
}

inline bool valid_kind(std::string_view value) {
  if (value.empty() || value.size() > kPresentationCardKindBytes) return false;
  for (const char ch : value) {
    const bool allowed = (ch >= 'a' && ch <= 'z') ||
                         (ch >= 'A' && ch <= 'Z') ||
                         (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' ||
                         ch == '.';
    if (!allowed) return false;
  }
  return true;
}

inline bool valid_utf8_text(std::string_view value, size_t maximum_bytes) {
  if (value.size() > maximum_bytes) return false;
  size_t offset = 0;
  while (offset < value.size()) {
    const auto first = static_cast<uint8_t>(value[offset]);
    uint32_t codepoint = 0;
    size_t width = 0;
    uint32_t minimum = 0;
    if (first <= 0x7FU) {
      codepoint = first;
      width = 1;
    } else if ((first & 0xE0U) == 0xC0U) {
      codepoint = first & 0x1FU;
      width = 2;
      minimum = 0x80U;
    } else if ((first & 0xF0U) == 0xE0U) {
      codepoint = first & 0x0FU;
      width = 3;
      minimum = 0x800U;
    } else if ((first & 0xF8U) == 0xF0U) {
      codepoint = first & 0x07U;
      width = 4;
      minimum = 0x10000U;
    } else {
      return false;
    }
    if (offset + width > value.size()) return false;
    for (size_t index = 1; index < width; ++index) {
      const auto next = static_cast<uint8_t>(value[offset + index]);
      if ((next & 0xC0U) != 0x80U) return false;
      codepoint = (codepoint << 6U) | (next & 0x3FU);
    }
    if ((width > 1 && codepoint < minimum) || codepoint > 0x10FFFFU ||
        (codepoint >= 0xD800U && codepoint <= 0xDFFFU)) {
      return false;
    }
    // Match Go unicode.IsControl for the Unicode Cc category. Card text is
    // semantic display data, never a terminal/control channel.
    if (codepoint <= 0x1FU || (codepoint >= 0x7FU && codepoint <= 0x9FU)) {
      return false;
    }
    offset += width;
  }
  return true;
}

template <size_t N>
inline void copy_bounded(std::array<char, N>& destination, std::string_view source) {
  destination.fill('\0');
  std::copy(source.begin(), source.end(), destination.begin());
}

template <size_t N>
inline std::string_view bounded_view(const std::array<char, N>& value) {
  const auto end = std::find(value.begin(), value.end(), '\0');
  return {value.data(), static_cast<size_t>(end - value.begin())};
}

} // namespace presentation_ingress

struct PresentationCardV1 {
  std::array<char, kPresentationCardKindBytes + 1> kind{};
  std::array<char, kPresentationCardTitleBytes + 1> title{};
  std::array<char, kPresentationCardTextBytes + 1> primary{};
  std::array<char, kPresentationCardTextBytes + 1> secondary{};
  uint8_t progress{};

  bool assign(int version, std::string_view new_kind, std::string_view new_title,
              std::string_view new_primary, std::string_view new_secondary,
              int new_progress) {
    if (version != kPresentationCardVersion ||
        !presentation_ingress::valid_kind(new_kind) ||
        !presentation_ingress::valid_utf8_text(new_title, kPresentationCardTitleBytes) ||
        !presentation_ingress::valid_utf8_text(new_primary, kPresentationCardTextBytes) ||
        !presentation_ingress::valid_utf8_text(new_secondary, kPresentationCardTextBytes) ||
        new_progress < 0 || new_progress > 100) {
      return false;
    }
    presentation_ingress::copy_bounded(kind, new_kind);
    presentation_ingress::copy_bounded(title, new_title);
    presentation_ingress::copy_bounded(primary, new_primary);
    presentation_ingress::copy_bounded(secondary, new_secondary);
    progress = static_cast<uint8_t>(new_progress);
    return true;
  }

  std::string_view kind_view() const { return presentation_ingress::bounded_view(kind); }
  std::string_view title_view() const { return presentation_ingress::bounded_view(title); }
  std::string_view primary_view() const { return presentation_ingress::bounded_view(primary); }
  std::string_view secondary_view() const { return presentation_ingress::bounded_view(secondary); }
};

enum class PresentationHintEmotion : uint8_t {
  idle,
  listening,
  thinking,
  speaking,
  tool_executing,
  interrupted,
  error,
};

struct PresentationHint {
  PresentationHintEmotion emotion{PresentationHintEmotion::idle};
  std::array<char, kPresentationHintTextBytes + 1> tool_name{};

  bool assign(std::string_view new_emotion, std::string_view new_tool_name) {
    PresentationHintEmotion parsed{};
    if (new_emotion == "idle") {
      parsed = PresentationHintEmotion::idle;
    } else if (new_emotion == "listening") {
      parsed = PresentationHintEmotion::listening;
    } else if (new_emotion == "thinking") {
      parsed = PresentationHintEmotion::thinking;
    } else if (new_emotion == "speaking") {
      parsed = PresentationHintEmotion::speaking;
    } else if (new_emotion == "tool_executing") {
      parsed = PresentationHintEmotion::tool_executing;
    } else if (new_emotion == "interrupted") {
      parsed = PresentationHintEmotion::interrupted;
    } else if (new_emotion == "error") {
      parsed = PresentationHintEmotion::error;
    } else {
      return false;
    }
    if ((parsed == PresentationHintEmotion::tool_executing) != !new_tool_name.empty() ||
        !presentation_ingress::valid_utf8_text(new_tool_name,
                                               kPresentationHintTextBytes)) {
      return false;
    }
    emotion = parsed;
    presentation_ingress::copy_bounded(tool_name, new_tool_name);
    return true;
  }

  std::string_view tool_name_view() const {
    return presentation_ingress::bounded_view(tool_name);
  }
};

struct AgentPresentationStatus {
  std::array<char, kAgentStatusBytes + 1> state{};

  bool assign(std::string_view new_state) {
    if (new_state.empty() ||
        !presentation_ingress::valid_utf8_text(new_state, kAgentStatusBytes)) {
      return false;
    }
    presentation_ingress::copy_bounded(state, new_state);
    return true;
  }

  std::string_view state_view() const {
    return presentation_ingress::bounded_view(state);
  }
};

} // namespace companion

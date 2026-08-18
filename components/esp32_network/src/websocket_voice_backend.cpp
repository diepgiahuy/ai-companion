#include "companion/websocket_voice_backend.hpp"

#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <initializer_list>
#include <ctime>

#include "cJSON.h"
#include "esp_crt_bundle.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_random.h"
#include "psa/crypto.h"

namespace companion {
namespace {
constexpr char kTag[] = "voice_ws";
constexpr int kTextOpcode = 0x1;
constexpr int kBinaryOpcode = 0x2;
constexpr size_t kMaximumVoiceMailBytes = 33'554'432;
constexpr size_t kMaximumOggPageBytes = 65'307;
constexpr size_t kMaximumOggPacketBytes = 8'192;
constexpr uint64_t kMaximumOpusGranule = 48'000ULL * 600ULL + 65'535ULL;

uint32_t read_le32(const uint8_t* value) {
  return static_cast<uint32_t>(value[0]) |
         static_cast<uint32_t>(value[1]) << 8 |
         static_cast<uint32_t>(value[2]) << 16 |
         static_cast<uint32_t>(value[3]) << 24;
}

uint64_t read_le64(const uint8_t* value) {
  return static_cast<uint64_t>(read_le32(value)) |
         static_cast<uint64_t>(read_le32(value + 4)) << 32;
}

uint32_t ogg_crc(const uint8_t* data, size_t size) {
  uint32_t crc = 0;
  for (size_t index = 0; index < size; ++index) {
    const uint8_t byte = index >= 22 && index < 26 ? 0 : data[index];
    crc ^= static_cast<uint32_t>(byte) << 24;
    for (int bit = 0; bit < 8; ++bit) {
      crc = (crc & 0x80000000U) != 0 ? (crc << 1) ^ 0x04c11db7U
                                      : crc << 1;
    }
  }
  return crc;
}

class OggOpusParser final {
public:
  using PacketHandler = bool (*)(void*, std::span<const uint8_t>);

  OggOpusParser(PacketHandler handler, void* context)
      : handler_(handler), context_(context) {
    page_ = static_cast<uint8_t*>(heap_caps_malloc(
        kMaximumOggPageBytes, MALLOC_CAP_8BIT | MALLOC_CAP_SPIRAM));
    if (page_ == nullptr) {
      page_ = static_cast<uint8_t*>(heap_caps_malloc(kMaximumOggPageBytes,
                                                     MALLOC_CAP_8BIT));
    }
  }

  ~OggOpusParser() { heap_caps_free(page_); }
  OggOpusParser(const OggOpusParser&) = delete;
  OggOpusParser& operator=(const OggOpusParser&) = delete;

  bool ready() const { return page_ != nullptr; }

  bool feed(std::span<const uint8_t> bytes) {
    while (!bytes.empty()) {
      if (page_size_ == page_expected_) {
        if (!finish_page()) return false;
      }
      size_t target = page_expected_;
      if (page_expected_ == 27 && page_size_ < 27) target = 27;
      const size_t count = std::min(bytes.size(), target - page_size_);
      std::memcpy(page_ + page_size_, bytes.data(), count);
      page_size_ += count;
      bytes = bytes.subspan(count);
      if (page_size_ == 27 && page_expected_ == 27) {
        if (std::memcmp(page_, "OggS", 4) != 0 || page_[4] != 0) return false;
        page_expected_ = 27 + page_[26];
      }
      if (page_size_ == page_expected_ && page_expected_ >= 27 &&
          page_expected_ == 27 + page_[26]) {
        size_t body_size = 0;
        for (size_t index = 0; index < page_[26]; ++index) {
          body_size += page_[27 + index];
        }
        if (body_size > kMaximumOggPageBytes - page_expected_) return false;
        page_expected_ += body_size;
      }
    }
    return true;
  }

  bool finish() {
    if (page_size_ == page_expected_ && page_size_ > 27 && !finish_page()) {
      return false;
    }
    return page_size_ == 0 && packet_size_ == 0 && page_count_ > 0 &&
           packet_count_ >= 3 && saw_head_ && saw_tags_ && saw_eos_;
  }

private:
  uint8_t* page_{};
  std::array<uint8_t, kMaximumOggPacketBytes> packet_{};
  size_t page_size_{};
  size_t page_expected_{27};
  size_t packet_size_{};
  uint32_t serial_{};
  uint32_t sequence_{};
  size_t page_count_{};
  size_t packet_count_{};
  bool saw_head_{};
  bool saw_tags_{};
  bool saw_eos_{};
  PacketHandler handler_{};
  void* context_{};

  bool finish_packet() {
    ++packet_count_;
    const std::span<const uint8_t> packet(packet_.data(), packet_size_);
    if (packet_count_ == 1) {
      saw_head_ = packet.size() >= 19 &&
                  std::memcmp(packet.data(), "OpusHead", 8) == 0 &&
                  packet[8] == 1 && packet[9] == 1 && packet[18] == 0;
      if (!saw_head_) return false;
    } else if (packet_count_ == 2) {
      saw_tags_ = packet.size() >= 8 &&
                  std::memcmp(packet.data(), "OpusTags", 8) == 0;
      if (!saw_tags_) return false;
    } else if (packet.empty() || packet.size() > 1'275 ||
               (handler_ != nullptr && !handler_(context_, packet))) {
      return false;
    }
    packet_size_ = 0;
    return true;
  }

  bool finish_page() {
    if (page_size_ < 27 || read_le32(page_ + 22) != ogg_crc(page_, page_size_)) {
      return false;
    }
    const uint8_t flags = page_[5];
    if ((page_count_ == 0) != ((flags & 0x02U) != 0) ||
        (page_count_ != 0 && (flags & 0x02U) != 0) ||
        (page_count_ == 0 && (flags & 0x01U) != 0) ||
        (page_count_ != 0 && read_le32(page_ + 14) != serial_) ||
        (page_count_ != 0 && read_le32(page_ + 18) != sequence_ + 1)) {
      return false;
    }
    const uint64_t granule = read_le64(page_ + 6);
    if (granule != UINT64_MAX && granule > kMaximumOpusGranule) return false;
    serial_ = read_le32(page_ + 14);
    sequence_ = read_le32(page_ + 18);
    const bool continuation = (flags & 0x01U) != 0;
    if (continuation != (packet_size_ != 0)) return false;
    size_t body_offset = 27 + page_[26];
    for (size_t index = 0; index < page_[26]; ++index) {
      const size_t segment = page_[27 + index];
      if (segment > packet_.size() - packet_size_) return false;
      std::memcpy(packet_.data() + packet_size_, page_ + body_offset, segment);
      packet_size_ += segment;
      body_offset += segment;
      if (segment < 255 && !finish_packet()) return false;
    }
    saw_eos_ = saw_eos_ || (flags & 0x04U) != 0;
    ++page_count_;
    page_size_ = 0;
    page_expected_ = 27;
    return true;
  }
};

std::string_view json_string(const cJSON* object, const char* key) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, key);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return {};
  return item->valuestring;
}

bool has_only_fields(const cJSON* object,
                     std::initializer_list<std::string_view> allowed);
bool parse_rfc3339_utc_ms(std::string_view value, uint64_t& output);

bool parse_uint32(const cJSON* value, uint32_t& output) {
  if (!cJSON_IsNumber(value) || value->valuedouble < 0 ||
      value->valuedouble > UINT32_MAX) {
    return false;
  }
  const auto parsed = static_cast<uint32_t>(value->valuedouble);
  if (value->valuedouble != static_cast<double>(parsed)) return false;
  output = parsed;
  return true;
}

bool parse_uint64(const cJSON* value, uint64_t& output) {
  constexpr double kMaximumExactJSONInteger = 9'007'199'254'740'991.0;
  if (!cJSON_IsNumber(value) || value->valuedouble < 0 ||
      value->valuedouble > kMaximumExactJSONInteger) {
    return false;
  }
  const auto parsed = static_cast<uint64_t>(value->valuedouble);
  if (value->valuedouble != static_cast<double>(parsed)) return false;
  output = parsed;
  return true;
}

bool optional_bounded_string(const cJSON* object, const char* key,
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

bool optional_features_valid(const cJSON* payload) {
  const cJSON* features = cJSON_GetObjectItemCaseSensitive(payload, "features");
  if (features == nullptr) return true;
  if (!has_only_fields(features, {"streaming_tts", "button_barge_in"})) return false;
  for (const char* key : {"streaming_tts", "button_barge_in"}) {
    const cJSON* value = cJSON_GetObjectItemCaseSensitive(features, key);
    if (value != nullptr && !cJSON_IsBool(value)) return false;
  }
  return true;
}

const cJSON* json_object(const cJSON* object, const char* key) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(object, key);
  return cJSON_IsObject(item) ? item : nullptr;
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

bool json_integer_equals(const cJSON* value, int expected) {
  return cJSON_IsNumber(value) && value->valuedouble == expected &&
         value->valueint == expected;
}

bool optional_json_string(const cJSON* object, const char* key,
                          std::string_view& output) {
  const cJSON* value = cJSON_GetObjectItemCaseSensitive(object, key);
  if (value == nullptr) {
    output = {};
    return true;
  }
  if (!cJSON_IsString(value) || value->valuestring == nullptr) return false;
  output = value->valuestring;
  return true;
}

bool parse_presentation_card(const cJSON* payload, PresentationCardV1& output) {
  const cJSON* ui = json_object(payload, "ui");
  if (ui == nullptr ||
      !has_only_fields(ui, {"version", "kind", "title", "primary",
                            "secondary", "progress"}) ||
      !json_integer_equals(cJSON_GetObjectItemCaseSensitive(ui, "version"),
                           kPresentationCardVersion)) {
    return false;
  }
  const cJSON* kind = cJSON_GetObjectItemCaseSensitive(ui, "kind");
  if (!cJSON_IsString(kind) || kind->valuestring == nullptr) return false;
  std::string_view title;
  std::string_view primary;
  std::string_view secondary;
  if (!optional_json_string(ui, "title", title) ||
      !optional_json_string(ui, "primary", primary) ||
      !optional_json_string(ui, "secondary", secondary)) {
    return false;
  }
  uint32_t progress = 0;
  const cJSON* progress_item = cJSON_GetObjectItemCaseSensitive(ui, "progress");
  if (progress_item != nullptr && !parse_uint32(progress_item, progress)) return false;
  return progress <= 100 &&
         output.assign(kPresentationCardVersion, kind->valuestring, title, primary,
                       secondary, static_cast<int>(progress));
}

bool parse_presentation_hint(const cJSON* payload, PresentationHint& output) {
  const cJSON* emotion = cJSON_GetObjectItemCaseSensitive(payload, "emotion");
  if (!cJSON_IsString(emotion) || emotion->valuestring == nullptr) return false;
  std::string_view tool_name;
  if (!optional_json_string(payload, "tool_name", tool_name)) return false;
  return output.assign(emotion->valuestring, tool_name);
}

bool parse_agent_presentation_status(const cJSON* payload,
                                     AgentPresentationStatus& output) {
  const cJSON* state = cJSON_GetObjectItemCaseSensitive(payload, "state");
  return cJSON_IsString(state) && state->valuestring != nullptr &&
         output.assign(state->valuestring);
}

bool payload_fields_valid(protocol::ControlType type, const cJSON* payload) {
  using protocol::ControlType;
  switch (type) {
  case ControlType::session_ready:
    return has_only_fields(payload, {"transport", "audio_params", "features"});
  case ControlType::session_ping:
  case ControlType::session_pong:
    return has_only_fields(payload, {});
  case ControlType::turn_abort:
    return has_only_fields(payload, {"reason"});
  case ControlType::turn_state:
    return has_only_fields(payload, {"state", "reason"});
  case ControlType::transcript_final:
    return has_only_fields(payload, {"text"});
  case ControlType::tts_lifecycle:
    return has_only_fields(payload, {"state", "text"});
  case ControlType::agent_status:
    return has_only_fields(payload, {"state"});
  case ControlType::ui_card:
    return has_only_fields(payload, {"ui"});
  case ControlType::ui_state:
    return has_only_fields(payload, {"emotion", "tool_name"});
  case ControlType::alarm_fired:
    return has_only_fields(payload, {"alarm_id", "message", "fire_at"});
  case ControlType::schedule_updated:
    return has_only_fields(payload, {"message", "fire_at"});
  case ControlType::protocol_error:
    return has_only_fields(payload, {"code", "message"});
  case ControlType::voice_mail_available:
    return has_only_fields(payload, {"voice_mail_id", "from_device_id",
                                     "media_format", "duration_ms", "size_bytes",
                                     "checksum_sha256", "expires_at", "policy"});
  case ControlType::voice_mail_claimed:
    return has_only_fields(payload, {"voice_mail_id", "playback_id", "media_ref",
                                     "lease_expires_at"});
  case ControlType::voice_mail_consumed:
    return has_only_fields(payload, {"voice_mail_id", "playback_id"});
  case ControlType::voice_mail_expired:
    return has_only_fields(payload, {"voice_mail_id"});
  default:
    return true;
  }
}

bool payload_semantics_valid(protocol::ControlType type, const cJSON* payload) {
  using protocol::ControlType;
  const auto nonempty = [payload](const char* field) {
    return !json_string(payload, field).empty();
  };
  switch (type) {
  case ControlType::session_ready:
    return optional_features_valid(payload);
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
  case ControlType::agent_status: {
    AgentPresentationStatus ignored{};
    return parse_agent_presentation_status(payload, ignored);
  }
  case ControlType::ui_card: {
    PresentationCardV1 ignored{};
    return parse_presentation_card(payload, ignored);
  }
  case ControlType::ui_state: {
    PresentationHint ignored{};
    return parse_presentation_hint(payload, ignored);
  }
  case ControlType::alarm_fired:
    return bounded_nonempty_string(payload, "alarm_id", 128) &&
           bounded_nonempty_string(payload, "message", 512) && nonempty("fire_at");
  case ControlType::schedule_updated:
    return bounded_nonempty_string(payload, "message", 512) && nonempty("fire_at");
  case ControlType::protocol_error:
    return bounded_nonempty_string(payload, "code", 64) &&
           bounded_nonempty_string(payload, "message", 1024);
  case ControlType::voice_mail_available: {
    uint32_t duration = 0, size = 0;
    uint64_t expires = 0;
    const std::string_view checksum = json_string(payload, "checksum_sha256");
    if (!bounded_nonempty_string(payload, "voice_mail_id", 128) ||
        !bounded_nonempty_string(payload, "from_device_id", 128) ||
        json_string(payload, "media_format") != "ogg_opus" ||
        !parse_uint32(cJSON_GetObjectItemCaseSensitive(payload, "duration_ms"), duration) ||
        !parse_uint32(cJSON_GetObjectItemCaseSensitive(payload, "size_bytes"), size) ||
        duration == 0 || duration > 600'000 || size == 0 ||
        size > kMaximumVoiceMailBytes || checksum.size() != 64 ||
        !parse_rfc3339_utc_ms(json_string(payload, "expires_at"), expires) ||
        !string_in(json_string(payload, "policy"), {"ephemeral", "retained"})) {
      return false;
    }
    for (const unsigned char character : checksum) {
      if (!std::isxdigit(character)) return false;
    }
    const std::time_t now = std::time(nullptr);
    return now < 1'577'836'800 || expires > static_cast<uint64_t>(now) * 1'000;
  }
  case ControlType::voice_mail_claimed: {
    const std::string_view media_ref = json_string(payload, "media_ref");
    uint64_t lease = 0;
    return bounded_nonempty_string(payload, "voice_mail_id", 128) &&
           bounded_nonempty_string(payload, "playback_id", 128) &&
           media_ref.starts_with("/v1/voice-mail/") && media_ref.size() <= 256 &&
           media_ref.find("://") == std::string_view::npos &&
           media_ref.find("..") == std::string_view::npos &&
           media_ref.find('\\') == std::string_view::npos &&
           media_ref.find('?') == std::string_view::npos &&
           media_ref.find('#') == std::string_view::npos &&
           parse_rfc3339_utc_ms(json_string(payload, "lease_expires_at"), lease);
  }
  case ControlType::voice_mail_consumed:
    return bounded_nonempty_string(payload, "voice_mail_id", 128) &&
           optional_bounded_string(payload, "playback_id", 128);
  case ControlType::voice_mail_expired:
    return bounded_nonempty_string(payload, "voice_mail_id", 128);
  default:
    return true;
  }
}

enum class VersionStatus : uint8_t { valid, unsupported, malformed };

VersionStatus protocol_version_status(const cJSON* root) {
  constexpr double kMaximumExactJSONInteger = 9'007'199'254'740'991.0;
  const cJSON* version = cJSON_GetObjectItemCaseSensitive(root, "version");
  if (!cJSON_IsNumber(version) ||
      std::abs(version->valuedouble) > kMaximumExactJSONInteger ||
      std::trunc(version->valuedouble) != version->valuedouble) {
    return VersionStatus::malformed;
  }
  return version->valuedouble == static_cast<double>(protocol::kVersion)
             ? VersionStatus::valid
             : VersionStatus::unsupported;
}

template <size_t N>
bool copy_string(std::array<char, N>& destination, std::string_view source) {
  if (source.size() >= N) return false;
  destination.fill('\0');
  std::copy(source.begin(), source.end(), destination.begin());
  return true;
}

bool parse_two_digits(std::string_view value, size_t offset, unsigned& output) {
  if (offset + 2 > value.size() || value[offset] < '0' || value[offset] > '9' ||
      value[offset + 1] < '0' || value[offset + 1] > '9') return false;
  output = static_cast<unsigned>((value[offset] - '0') * 10 + value[offset + 1] - '0');
  return true;
}

int64_t days_from_civil(int year, unsigned month, unsigned day) {
  year -= month <= 2;
  const int era = (year >= 0 ? year : year - 399) / 400;
  const unsigned year_of_era = static_cast<unsigned>(year - era * 400);
  const unsigned day_of_year = (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 +
                               day - 1;
  const unsigned day_of_era = year_of_era * 365 + year_of_era / 4 -
                              year_of_era / 100 + day_of_year;
  return era * 146097 + static_cast<int64_t>(day_of_era) - 719468;
}

bool parse_rfc3339_utc_ms(std::string_view value, uint64_t& output) {
  if (value.size() < 20 || value[4] != '-' || value[7] != '-' ||
      value[10] != 'T' || value[13] != ':' || value[16] != ':') return false;
  unsigned year_hi = 0, year_lo = 0, month = 0, day = 0;
  unsigned hour = 0, minute = 0, second = 0;
  if (!parse_two_digits(value, 0, year_hi) || !parse_two_digits(value, 2, year_lo) ||
      !parse_two_digits(value, 5, month) || !parse_two_digits(value, 8, day) ||
      !parse_two_digits(value, 11, hour) || !parse_two_digits(value, 14, minute) ||
      !parse_two_digits(value, 17, second)) return false;
  const unsigned year = year_hi * 100 + year_lo;
  size_t offset = 19;
  unsigned milliseconds = 0;
  if (offset < value.size() && value[offset] == '.') {
    ++offset;
    size_t digits = 0;
    while (offset < value.size() && value[offset] >= '0' && value[offset] <= '9') {
      if (digits < 3) milliseconds = milliseconds * 10 + value[offset] - '0';
      ++digits;
      ++offset;
    }
    if (digits == 0) return false;
    while (digits++ < 3) milliseconds *= 10;
  }
  constexpr unsigned kMonthDays[]{0, 31, 28, 31, 30, 31, 30,
                                   31, 31, 30, 31, 30, 31};
  const bool leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
  const unsigned maximum_day = month >= 1 && month <= 12
                                   ? kMonthDays[month] + (month == 2 && leap ? 1 : 0)
                                   : 0;
  if (offset + 1 != value.size() || value[offset] != 'Z' || year < 2020 ||
      month < 1 || month > 12 || day < 1 || day > maximum_day || hour > 23 ||
      minute > 59 || second > 60) return false;
  const int64_t days = days_from_civil(static_cast<int>(year), month, day);
  if (days < 0) return false;
  output = (static_cast<uint64_t>(days) * 86'400 + hour * 3'600 +
            minute * 60 + std::min(second, 59U)) * 1'000 + milliseconds;
  return true;
}

bool format_now_rfc3339(std::array<char, 36>& output) {
  const std::time_t now = std::time(nullptr);
  if (now < 1'577'836'800) return false;
  std::tm utc{};
  if (gmtime_r(&now, &utc) == nullptr) return false;
  return std::strftime(output.data(), output.size(), "%Y-%m-%dT%H:%M:%SZ", &utc) > 0;
}

bool sha256_matches(const unsigned char digest[32], std::string_view expected) {
  if (expected.size() != 64) return false;
  constexpr char kHex[] = "0123456789abcdef";
  for (size_t index = 0; index < 32; ++index) {
    const char high = static_cast<char>(std::tolower(
        static_cast<unsigned char>(expected[index * 2])));
    const char low = static_cast<char>(std::tolower(
        static_cast<unsigned char>(expected[index * 2 + 1])));
    if (high != kHex[digest[index] >> 4] || low != kHex[digest[index] & 0x0f]) {
      return false;
    }
  }
  return true;
}

bool url_encode(std::string_view value, std::span<char> output, size_t& written) {
  constexpr char kHex[] = "0123456789ABCDEF";
  written = 0;
  for (const unsigned char character : value) {
    const bool safe = (character >= 'a' && character <= 'z') ||
                      (character >= 'A' && character <= 'Z') ||
                      (character >= '0' && character <= '9') ||
                      character == '-' || character == '_' || character == '.' ||
                      character == '~';
    const size_t needed = safe ? 1 : 3;
    if (needed > output.size() - written) return false;
    if (safe) {
      output[written++] = static_cast<char>(character);
    } else {
      output[written++] = '%';
      output[written++] = kHex[character >> 4];
      output[written++] = kHex[character & 0x0f];
    }
  }
  return true;
}

template <size_t N>
bool random_opaque_id(std::array<char, N>& output, std::string_view prefix) {
  constexpr char kHex[] = "0123456789abcdef";
  std::array<uint8_t, 16> bytes{};
  if (prefix.size() + bytes.size() * 2 >= output.size()) return false;
  esp_fill_random(bytes.data(), bytes.size());
  output.fill('\0');
  std::copy(prefix.begin(), prefix.end(), output.begin());
  size_t offset = prefix.size();
  for (const uint8_t byte : bytes) {
    output[offset++] = kHex[byte >> 4];
    output[offset++] = kHex[byte & 0x0f];
  }
  return true;
}
} // namespace

WebSocketVoiceBackend::WebSocketVoiceBackend() {
  opus_decoder_mutex_ = xSemaphoreCreateMutexStatic(&opus_decoder_mutex_storage_);
  outbound_queue_ = xQueueCreateStatic(kOutboundQueueCapacity, sizeof(Outbound),
                                       outbound_queue_buffer_.data(),
                                       &outbound_queue_storage_);
  playback_queue_ = xQueueCreateStatic(kPlaybackQueueCapacity, sizeof(AudioFrame),
                                       playback_queue_buffer_.data(),
                                       &playback_queue_storage_);
  event_queue_ = xQueueCreateStatic(kEventQueueCapacity, sizeof(BackendEvent),
                                    event_queue_buffer_.data(),
                                    &event_queue_storage_);
  media_queue_ = xQueueCreateStatic(kMediaQueueCapacity, sizeof(MediaJob),
                                    media_queue_buffer_.data(),
                                    &media_queue_storage_);
}

WebSocketVoiceBackend::~WebSocketVoiceBackend() {
  stopping_.store(true);
  voice_mail_generation_.fetch_add(1);
  if (writer_task_ != nullptr && outbound_queue_ != nullptr) {
    Outbound dummy{};
    (void)xQueueSend(outbound_queue_, &dummy, 0);
  }
  if (media_task_ != nullptr && media_queue_ != nullptr) {
    MediaJob dummy{};
    (void)xQueueSend(media_queue_, &dummy, 0);
  }
  for (int i = 0; i < 30 && (writer_task_ != nullptr || media_task_ != nullptr); ++i) {
    vTaskDelay(pdMS_TO_TICKS(10));
  }
  if (media_task_ != nullptr) {
    vTaskDelete(media_task_);
    media_task_ = nullptr;
  }
  if (writer_task_ != nullptr) {
    vTaskDelete(writer_task_);
    writer_task_ = nullptr;
  }
  if (client_ != nullptr) {
    esp_websocket_client_stop(client_);
    esp_websocket_client_destroy(client_);
    client_ = nullptr;
  }
  if (opus_encoder_ != nullptr) {
    esp_opus_enc_close(opus_encoder_);
    opus_encoder_ = nullptr;
  }
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreTake(opus_decoder_mutex_, portMAX_DELAY);
  }
  if (opus_decoder_ != nullptr) {
    esp_opus_dec_close(opus_decoder_);
    opus_decoder_ = nullptr;
  }
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreGive(opus_decoder_mutex_);
  }
}

bool WebSocketVoiceBackend::initialize(std::string_view url,
                                       std::string_view token,
                                       std::string_view device_id,
                                       std::string_view client_id) {
  if (client_ != nullptr || !copy_string(url_, url) || !copy_string(token_, token) ||
      !copy_string(device_id_, device_id) || !copy_string(client_id_, client_id) ||
      !build_http_origin(url)) {
    return false;
  }
  const int header_size = std::snprintf(
      headers_.data(), headers_.size(),
      "Authorization: Bearer %.*s\r\nProtocol-Version: 2\r\n"
      "Device-Id: %s\r\nClient-Id: %s\r\n",
      static_cast<int>(token.size()), token.data(), device_id_.data(),
      client_id_.data());
  if (header_size < 0 || static_cast<size_t>(header_size) >= headers_.size()) {
    return false;
  }

  esp_opus_enc_config_t encoder_config{
      .sample_rate = ESP_AUDIO_SAMPLE_RATE_16K,
      .channel = ESP_AUDIO_MONO,
      .bits_per_sample = ESP_AUDIO_BIT16,
      .bitrate = ESP_OPUS_BITRATE_AUTO,
      .frame_duration = ESP_OPUS_ENC_FRAME_DURATION_60_MS,
      .application_mode = ESP_OPUS_ENC_APPLICATION_AUDIO,
      .complexity = 0,
      .enable_fec = false,
      .enable_dtx = true,
      .enable_vbr = true,
  };
  int encoder_frame_bytes = 0;
  if (esp_opus_enc_open(&encoder_config, sizeof(encoder_config), &opus_encoder_) !=
          ESP_AUDIO_ERR_OK ||
      opus_encoder_ == nullptr ||
      esp_opus_enc_get_frame_size(opus_encoder_, &encoder_frame_bytes,
                                  &encoder_output_bytes_) != ESP_AUDIO_ERR_OK ||
      encoder_frame_bytes != static_cast<int>(kOpusFrameSamples * sizeof(int16_t)) ||
      encoder_output_bytes_ > static_cast<int>(kMaximumOpusPacketBytes) ||
      !configure_decoder(playback_sample_rate_hz_.load())) {
    return false;
  }

  esp_websocket_client_config_t config{};
  config.uri = url_.data();
  config.headers = headers_.data();
  config.disable_auto_reconnect = false;
  config.reconnect_timeout_ms = 2'000;
  config.network_timeout_ms = 5'000;
  config.crt_bundle_attach = esp_crt_bundle_attach;
  client_ = esp_websocket_client_init(&config);
  if (client_ == nullptr) return false;
  if (esp_websocket_register_events(client_, WEBSOCKET_EVENT_ANY,
                                    &WebSocketVoiceBackend::event_handler,
                                    this) != ESP_OK) {
    esp_websocket_client_destroy(client_);
    client_ = nullptr;
    return false;
  }
  writer_task_ = xTaskCreateStatic(&WebSocketVoiceBackend::writer_entry,
                                   "voice-writer", kWriterStackDepth, this, 5,
                                   writer_stack_.data(), &writer_task_storage_);
  media_task_ = xTaskCreateStatic(&WebSocketVoiceBackend::media_entry,
                                  "voice-mail", kMediaStackDepth, this, 4,
                                  media_stack_.data(), &media_task_storage_);
  if (writer_task_ == nullptr || media_task_ == nullptr) {
    if (writer_task_ != nullptr) vTaskDelete(writer_task_);
    if (media_task_ != nullptr) vTaskDelete(media_task_);
    writer_task_ = nullptr;
    media_task_ = nullptr;
    return false;
  }
  return true;
}

bool WebSocketVoiceBackend::start(uint64_t) {
  if (client_ == nullptr) return false;
  if (client_started_.load()) return true;
  const bool started = esp_websocket_client_start(client_) == ESP_OK;
  client_started_.store(started);
  return started;
}

void WebSocketVoiceBackend::tick(uint64_t) {}

bool WebSocketVoiceBackend::begin_turn(uint64_t, ListenMode mode) {
  if (!protocol_connected_.load()) return false;
  std::array<char, 40> turn_id{};
  const int length = std::snprintf(turn_id.data(), turn_id.size(),
                                   "turn-%llu",
                                   static_cast<unsigned long long>(++turn_sequence_));
  if (length < 0 || static_cast<size_t>(length) >= turn_id.size()) return false;
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool already_active = turn_active_.exchange(true);
  if (!already_active) active_turn_id_ = turn_id;
  taskEXIT_CRITICAL(&turn_id_lock_);
  if (already_active) return false;
  reset_turn_queues();
  if (!enqueue_command(CommandType::listen_start, turn_id.data(), mode)) {
    turn_active_.store(false);
    return false;
  }
  return true;
}

bool WebSocketVoiceBackend::send_audio(std::span<const int16_t> pcm) {
  if (!turn_active_.load() || pcm.empty()) return false;
  size_t source_offset = 0;
  while (source_offset < pcm.size()) {
    std::array<int16_t, kOpusFrameSamples> frame{};
    bool frame_ready = false;
    uint64_t generation = 0;
    taskENTER_CRITICAL(&media_buffer_lock_);
    if (!turn_active_.load()) {
      taskEXIT_CRITICAL(&media_buffer_lock_);
      return false;
    }
    generation = media_generation_.load();
    const size_t count = std::min(pcm.size() - source_offset,
                                  kOpusFrameSamples - upload_payload_size_);
    std::copy_n(pcm.begin() + source_offset, count,
                upload_payload_.begin() + upload_payload_size_);
    source_offset += count;
    upload_payload_size_ += count;
    if (upload_payload_size_ == kOpusFrameSamples) {
      frame = upload_payload_;
      upload_payload_ = {};
      upload_payload_size_ = 0;
      frame_ready = true;
    }
    taskEXIT_CRITICAL(&media_buffer_lock_);
    if (frame_ready &&
        (!turn_active_.load() || generation != media_generation_.load() ||
         !encode_and_enqueue(frame, generation))) return false;
  }
  return turn_active_.load();
}

bool WebSocketVoiceBackend::finish_turn(uint64_t) {
  if (!turn_active_.load()) return false;
  std::array<int16_t, kOpusFrameSamples> frame{};
  bool frame_ready = false;
  uint64_t generation = 0;
  taskENTER_CRITICAL(&media_buffer_lock_);
  if (!turn_active_.load()) {
    taskEXIT_CRITICAL(&media_buffer_lock_);
    return false;
  }
  generation = media_generation_.load();
  if (upload_payload_size_ != 0) {
    std::copy_n(upload_payload_.begin(), upload_payload_size_, frame.begin());
    upload_payload_ = {};
    upload_payload_size_ = 0;
    frame_ready = true;
  }
  taskEXIT_CRITICAL(&media_buffer_lock_);
  if (frame_ready &&
      (!turn_active_.load() || generation != media_generation_.load() ||
       !encode_and_enqueue(frame, generation))) return false;
  if (!turn_active_.load() || generation != media_generation_.load()) return false;
  const std::array<char, 40> turn_id = active_turn_id_snapshot();
  return enqueue_command(CommandType::listen_stop, turn_id.data());
}

void WebSocketVoiceBackend::cancel_turn() {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool was_turn_active = turn_active_.exchange(false);
  const bool was_tts_active = tts_active_.exchange(false);
  const bool had_active_turn = was_turn_active || was_tts_active;
  const std::array<char, 40> turn_id = active_turn_id_;
  taskEXIT_CRITICAL(&turn_id_lock_);
  if (had_active_turn) {
    xQueueReset(outbound_queue_);
    enqueue_command(CommandType::abort, turn_id.data());
  }
  reset_turn_queues();
}

bool WebSocketVoiceBackend::poll_event(BackendEvent& event) {
  while (xQueueReceive(event_queue_, &event, 0) == pdPASS) {
    if (event.scope == BackendEventScope::generation) {
      const uint64_t expected_generation =
          (event.type == BackendEventType::voice_mail_playback_ready ||
           event.type == BackendEventType::voice_mail_playback_finished ||
           event.type == BackendEventType::voice_mail_failed)
              ? voice_mail_generation_.load()
              : media_generation_.load();
      if (event.session_epoch != session_epoch_.load() ||
          event.generation != expected_generation) {
        continue;
      }
    } else if (event.scope == BackendEventScope::session) {
      if (event.type != BackendEventType::disconnected &&
          event.session_epoch != session_epoch_.load()) {
        continue;
      }
    }
    return true;
  }
  return false;
}

bool WebSocketVoiceBackend::claim_voice_mail(const VoiceMailMetadata& item,
                                             uint64_t) {
  if (!protocol_connected_.load() || !item.valid()) return false;
  const std::time_t wall_now = std::time(nullptr);
  if (wall_now < 1'577'836'800 ||
      item.expires_at_unix_ms <= static_cast<uint64_t>(wall_now) * 1'000) {
    return false;
  }

  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::voice_mail_claim;
  outbound.command.voice_mail = item;
  if (!format_now_rfc3339(outbound.command.occurred_at)) return false;

  taskENTER_CRITICAL(&voice_mail_lock_);
  if (voice_mail_claim_pending_ || voice_mail_result_pending_) {
    const bool same = active_voice_mail_.voice_mail_id_view() ==
                      item.voice_mail_id_view();
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return same;
  }
  if (!random_opaque_id(outbound.command.playback_id, "vm-playback-") ||
      !random_opaque_id(outbound.command.idempotency_key, "vm-claim-")) {
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return false;
  }
  active_voice_mail_ = item;
  active_playback_id_ = outbound.command.playback_id;
  active_claim_key_ = outbound.command.idempotency_key;
  active_result_key_.fill('\0');
  voice_mail_claim_pending_ = true;
  voice_mail_result_pending_ = false;
  taskEXIT_CRITICAL(&voice_mail_lock_);
  xQueueReset(playback_queue_);
  if (xQueueSend(outbound_queue_, &outbound, 0) != pdPASS) {
    clear_voice_mail(false);
    return false;
  }
  return true;
}

bool WebSocketVoiceBackend::report_voice_mail_playback(
    const VoiceMailMetadata& item, bool succeeded,
    std::string_view failure_code, uint64_t) {
  if (!protocol_connected_.load() || !item.valid() ||
      (succeeded && !failure_code.empty()) || failure_code.size() > 64) {
    return false;
  }
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::voice_mail_playback_result;
  outbound.command.voice_mail = item;
  outbound.command.succeeded = succeeded;
  if (!copy_string(outbound.command.message, failure_code) ||
      !format_now_rfc3339(outbound.command.occurred_at)) return false;

  taskENTER_CRITICAL(&voice_mail_lock_);
  if (active_voice_mail_.voice_mail_id_view() != item.voice_mail_id_view() ||
      active_playback_id_[0] == '\0') {
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return false;
  }
  if (voice_mail_result_pending_) {
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return true;
  }
  outbound.command.playback_id = active_playback_id_;
  if (!random_opaque_id(outbound.command.idempotency_key, "vm-result-")) {
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return false;
  }
  active_result_key_ = outbound.command.idempotency_key;
  voice_mail_result_pending_ = true;
  taskEXIT_CRITICAL(&voice_mail_lock_);
  if (xQueueSend(outbound_queue_, &outbound, 0) != pdPASS) {
    taskENTER_CRITICAL(&voice_mail_lock_);
    voice_mail_result_pending_ = false;
    active_result_key_.fill('\0');
    taskEXIT_CRITICAL(&voice_mail_lock_);
    return false;
  }
  return true;
}

void WebSocketVoiceBackend::cancel_voice_mail(const VoiceMailMetadata& item,
                                              std::string_view failure_code,
                                              uint64_t now_ms) {
  voice_mail_generation_.fetch_add(1);
  xQueueReset(media_queue_);
  if (item.valid() && !failure_code.empty()) {
    report_voice_mail_playback(item, false, failure_code, now_ms);
  }
  clear_voice_mail(true);
}

size_t WebSocketVoiceBackend::read_playback(std::span<int16_t> destination) {
  AudioFrame frame{};
  if (xQueueReceive(playback_queue_, &frame, 0) != pdPASS) return 0;
  const size_t count = std::min(destination.size(), static_cast<size_t>(frame.count));
  std::copy_n(frame.samples.begin(), count, destination.begin());
  return count;
}

bool WebSocketVoiceBackend::playback_empty() const {
  return uxQueueMessagesWaiting(playback_queue_) == 0;
}

void WebSocketVoiceBackend::event_handler(void* context, esp_event_base_t,
                                          int32_t event_id, void* event_data) {
  static_cast<WebSocketVoiceBackend*>(context)->on_event(
      event_id, static_cast<esp_websocket_event_data_t*>(event_data));
}

void WebSocketVoiceBackend::writer_entry(void* context) {
  if (context == nullptr) return;
  auto* self = static_cast<WebSocketVoiceBackend*>(context);
  self->writer_loop();
  self->writer_task_ = nullptr;
  vTaskDelete(nullptr);
}

void WebSocketVoiceBackend::media_entry(void* context) {
  if (context == nullptr) return;
  auto* self = static_cast<WebSocketVoiceBackend*>(context);
  self->media_loop();
  self->media_task_ = nullptr;
  vTaskDelete(nullptr);
}

void WebSocketVoiceBackend::on_event(int32_t event_id,
                                     esp_websocket_event_data_t* data) {
  switch (event_id) {
  case WEBSOCKET_EVENT_CONNECTED:
    socket_connected_.store(true);
    protocol_connected_.store(false);
    clear_session_id();
    reset_turn_queues();
    enqueue_command(CommandType::hello);
    break;
  case WEBSOCKET_EVENT_DISCONNECTED:
    socket_connected_.store(false);
    protocol_connected_.store(false);
    clear_session_id();
    taskENTER_CRITICAL(&turn_id_lock_);
    turn_active_.store(false);
    tts_active_.store(false);
    taskEXIT_CRITICAL(&turn_id_lock_);
    xQueueReset(outbound_queue_);
    session_epoch_.fetch_add(1);
    reset_turn_queues();
    clear_voice_mail(true);
    enqueue_event(BackendEventType::disconnected);
    break;
  case WEBSOCKET_EVENT_DATA:
    if (data == nullptr || data->data_ptr == nullptr || data->data_len < 0) break;
    if (data->payload_offset == 0) receive_opcode_ = data->op_code;
    if (receive_opcode_ == kTextOpcode) {
      const size_t offset = static_cast<size_t>(data->payload_offset);
      const size_t length = static_cast<size_t>(data->data_len);
      if (offset + length >= text_payload_.size()) {
        enqueue_event(BackendEventType::error, "CONTROL TOO LARGE");
        text_payload_size_ = 0;
        break;
      }
      std::copy_n(data->data_ptr, length, text_payload_.begin() + offset);
      text_payload_size_ = offset + length;
      if (text_payload_size_ == static_cast<size_t>(data->payload_len)) {
        text_payload_[text_payload_size_] = '\0';
        handle_text({text_payload_.data(), text_payload_size_});
        text_payload_size_ = 0;
      }
    } else if (receive_opcode_ == kBinaryOpcode) {
      handle_binary(*data);
    }
    break;
  case WEBSOCKET_EVENT_ERROR:
    enqueue_event(BackendEventType::error, "WEBSOCKET ERROR");
    break;
  default:
    break;
  }
}

void WebSocketVoiceBackend::writer_loop() {
  while (!stopping_.load()) {
    Outbound outbound{};
    if (xQueueReceive(outbound_queue_, &outbound, pdMS_TO_TICKS(100)) != pdPASS) continue;
    if (stopping_.load()) break;
    if (outbound.type == OutboundType::control) {
      const Command& command = outbound.command;
      char payload[2'048]{};
      protocol::ControlType type{};
      std::string_view turn_id;
      std::string_view correlation_id;
      std::string_view idempotency_key;
      std::string_view occurred_at;
      switch (command.type) {
      case CommandType::hello:
        type = protocol::ControlType::session_hello;
        std::snprintf(payload, sizeof(payload),
                      "{\"transport\":\"websocket\",\"audio_params\":{"
                      "\"format\":\"opus\",\"sample_rate\":16000,"
                      "\"channels\":1,\"frame_duration\":60}}");
        break;
      case CommandType::session_pong:
        type = protocol::ControlType::session_pong;
        correlation_id = command.correlation_id.data();
        std::snprintf(payload, sizeof(payload), "{}");
        break;
      case CommandType::listen_start: {
        type = protocol::ControlType::turn_listen;
        turn_id = command.turn_id.data();
        const char* mode = command.mode == ListenMode::auto_vad ? "auto_vad" : "manual";
        std::snprintf(payload, sizeof(payload),
                      "{\"state\":\"start\",\"mode\":\"%s\"}", mode);
        break;
      }
      case CommandType::listen_stop:
        type = protocol::ControlType::turn_listen;
        turn_id = command.turn_id.data();
        std::snprintf(payload, sizeof(payload), "{\"state\":\"stop\"}");
        break;
      case CommandType::abort:
        type = protocol::ControlType::turn_abort;
        turn_id = command.turn_id.data();
        std::snprintf(payload, sizeof(payload),
                      "{\"reason\":\"button_barge_in\"}");
        break;
      case CommandType::alarm_ack:
        type = protocol::ControlType::alarm_ack;
        {
          char alarm_id[128]{};
          size_t alarm_id_size = 0;
          if (!protocol::encode_json_string(command.turn_id.data(), alarm_id,
                                            alarm_id_size) ||
              std::snprintf(payload, sizeof(payload), "{\"alarm_id\":%.*s}",
                            static_cast<int>(alarm_id_size), alarm_id) < 0) {
            payload[0] = '\0';
          }
        }
        break;
      case CommandType::protocol_error:
        type = protocol::ControlType::protocol_error;
        {
          char code[256]{};
          char message[256]{};
          size_t code_size = 0;
          size_t message_size = 0;
          if (!protocol::encode_json_string(command.code.data(), code, code_size) ||
              !protocol::encode_json_string(command.message.data(), message,
                                            message_size)) {
            payload[0] = '\0';
            break;
          }
          const int payload_size = std::snprintf(
              payload, sizeof(payload), "{\"code\":%.*s,\"message\":%.*s}",
              static_cast<int>(code_size), code,
              static_cast<int>(message_size), message);
          if (payload_size < 0 ||
              static_cast<size_t>(payload_size) >= sizeof(payload)) {
            payload[0] = '\0';
          }
        }
        break;
      case CommandType::voice_mail_claim:
      case CommandType::voice_mail_playback_result: {
        const bool claim = command.type == CommandType::voice_mail_claim;
        type = claim ? protocol::ControlType::voice_mail_claim
                     : protocol::ControlType::voice_mail_playback_result;
        idempotency_key = command.idempotency_key.data();
        occurred_at = command.occurred_at.data();
        char mail_id[800]{};
        char playback_id[800]{};
        char failure[400]{};
        size_t mail_size = 0, playback_size = 0, failure_size = 0;
        if (!protocol::encode_json_string(
                command.voice_mail.voice_mail_id_view(), mail_id, mail_size) ||
            !protocol::encode_json_string(command.playback_id.data(), playback_id,
                                          playback_size) ||
            (!claim && !protocol::encode_json_string(command.message.data(), failure,
                                                      failure_size))) {
          payload[0] = '\0';
          break;
        }
        int payload_size = 0;
        if (claim) {
          payload_size = std::snprintf(
              payload, sizeof(payload),
              "{\"voice_mail_id\":%.*s,\"playback_id\":%.*s}",
              static_cast<int>(mail_size), mail_id,
              static_cast<int>(playback_size), playback_id);
        } else if (command.succeeded) {
          payload_size = std::snprintf(
              payload, sizeof(payload),
              "{\"voice_mail_id\":%.*s,\"playback_id\":%.*s,"
              "\"result\":\"succeeded\"}",
              static_cast<int>(mail_size), mail_id,
              static_cast<int>(playback_size), playback_id);
        } else {
          payload_size = std::snprintf(
              payload, sizeof(payload),
              "{\"voice_mail_id\":%.*s,\"playback_id\":%.*s,"
              "\"result\":\"failed\",\"failure_code\":%.*s}",
              static_cast<int>(mail_size), mail_id,
              static_cast<int>(playback_size), playback_id,
              static_cast<int>(failure_size), failure);
        }
        if (payload_size < 0 ||
            static_cast<size_t>(payload_size) >= sizeof(payload)) payload[0] = '\0';
        break;
      }
      }
      if (payload[0] == '\0' || std::strlen(payload) >= sizeof(payload) ||
          ((command.type == CommandType::voice_mail_claim ||
            command.type == CommandType::voice_mail_playback_result) &&
           (idempotency_key.empty() || occurred_at.empty()))) {
        ESP_LOGW(kTag, "control payload too large");
        continue;
      }
      char message_id[32]{};
      const int message_id_size = std::snprintf(
          message_id, sizeof(message_id), "firmware-%llu",
          static_cast<unsigned long long>(message_sequence_.fetch_add(1) + 1));
      char json[3'072]{};
      size_t json_size = 0;
      const std::array<char, 64> session_id = session_id_snapshot();
      const protocol::Envelope envelope{
          .type = type,
          .message_id = message_id_size > 0 &&
                        static_cast<size_t>(message_id_size) < sizeof(message_id)
                            ? std::string_view(message_id)
                            : std::string_view{},
          .payload_json = payload,
          .correlation_id = correlation_id,
          .session_id = session_id.data(),
          .turn_id = turn_id,
          .generation_id = 0,
          .has_generation_id = false,
          .idempotency_key = idempotency_key,
          .occurred_at = occurred_at,
      };
      if (!protocol::encode(envelope, json, json_size)) {
        ESP_LOGW(kTag, "control envelope encode failed");
        continue;
      }
      bool sent = false;
      for (int attempt = 0; attempt < 2 && !sent; ++attempt) {
        sent = send_text({json, json_size});
        if (!sent && attempt == 0) vTaskDelay(pdMS_TO_TICKS(100));
      }
      if (!sent) ESP_LOGW(kTag, "control send failed");
    } else if (socket_connected_.load() &&
               outbound.media_generation == media_generation_.load()) {
      const int bytes = static_cast<int>(outbound.audio.count);
      const int written = esp_websocket_client_send_bin(
          client_, reinterpret_cast<const char*>(outbound.audio.bytes.data()), bytes,
          pdMS_TO_TICKS(100));
      if (written != bytes) {
        enqueue_event(BackendEventType::error, "AUDIO SEND FAILED");
      }
    }
  }
}

void WebSocketVoiceBackend::handle_text(std::string_view json) {
  cJSON* root = cJSON_ParseWithLength(json.data(), json.size());
  if (root == nullptr) {
    enqueue_event(BackendEventType::error, "INVALID CONTROL");
    return;
  }
  if (!cJSON_IsObject(root)) {
    enqueue_protocol_error("invalid_envelope", "control envelope must be an object");
    enqueue_event(BackendEventType::error, "INVALID CONTROL ENVELOPE");
    cJSON_Delete(root);
    return;
  }
  if (!has_only_fields(root, {"version", "type", "message_id",
                              "correlation_id", "session_id", "turn_id",
                              "generation_id", "idempotency_key",
                              "occurred_at", "payload"})) {
    enqueue_protocol_error("invalid_envelope", "control envelope has unknown fields");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL FIELD");
    cJSON_Delete(root);
    return;
  }
  const VersionStatus version_status = protocol_version_status(root);
  if (version_status != VersionStatus::valid) {
    const bool unsupported = version_status == VersionStatus::unsupported;
    enqueue_protocol_error(unsupported ? "unsupported_protocol_version" : "invalid_envelope",
                           unsupported ? "only protocol version 2 is supported"
                                       : "version must be an integer");
    enqueue_event(BackendEventType::error,
                  unsupported ? "UNSUPPORTED PROTOCOL VERSION"
                              : "INVALID CONTROL VERSION");
    cJSON_Delete(root);
    return;
  }
  const std::string_view message_id = json_string(root, "message_id");
  const std::string_view type_name = json_string(root, "type");
  const cJSON* payload = json_object(root, "payload");
  protocol::ControlType type{};
  if (message_id.empty() || type_name.empty() || payload == nullptr ||
      message_id.size() >= Command{}.correlation_id.size()) {
    enqueue_protocol_error("invalid_envelope", "message_id, type, and payload object are required");
    enqueue_event(BackendEventType::error, "INVALID CONTROL ENVELOPE");
    cJSON_Delete(root);
    return;
  }
  if (!protocol::parse_type(type_name, type)) {
    enqueue_protocol_error("unknown_message_type", "control type is not supported");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL TYPE");
    cJSON_Delete(root);
    return;
  }
  const bool presentation_control =
      type == protocol::ControlType::ui_card ||
      type == protocol::ControlType::ui_state ||
      type == protocol::ControlType::agent_status;
  if (presentation_control &&
      presentation_ingress::contains_unsupported_json_nul(json)) {
    enqueue_protocol_error(
        "invalid_envelope",
        "presentation control contains an unsupported zero character");
    enqueue_event(BackendEventType::error, "INVALID PRESENTATION CONTROL");
    cJSON_Delete(root);
    return;
  }
  if (!payload_fields_valid(type, payload)) {
    enqueue_protocol_error("invalid_envelope", "control payload has unknown fields");
    enqueue_event(BackendEventType::error, "UNKNOWN CONTROL PAYLOAD FIELD");
    cJSON_Delete(root);
    return;
  }
  if (!payload_semantics_valid(type, payload)) {
    enqueue_protocol_error("invalid_envelope", "control payload is malformed");
    enqueue_event(BackendEventType::error, "INVALID CONTROL PAYLOAD");
    cJSON_Delete(root);
    return;
  }

  const bool voice_mail_interaction =
      type == protocol::ControlType::voice_mail_available ||
      type == protocol::ControlType::voice_mail_claimed ||
      type == protocol::ControlType::voice_mail_consumed ||
      type == protocol::ControlType::voice_mail_expired;
  uint64_t occurred_at_ms = 0;
  const std::string_view interaction_key = json_string(root, "idempotency_key");
  if (voice_mail_interaction &&
      (interaction_key.empty() || interaction_key.size() > 128 ||
       !parse_rfc3339_utc_ms(json_string(root, "occurred_at"), occurred_at_ms))) {
    enqueue_protocol_error("invalid_envelope",
                           "voice mail requires idempotency_key and occurred_at");
    enqueue_event(BackendEventType::error, "INVALID VOICE MAIL ENVELOPE");
    cJSON_Delete(root);
    return;
  }

  if (!protocol_connected_.load() &&
      type != protocol::ControlType::session_ready &&
      type != protocol::ControlType::protocol_error) {
    enqueue_protocol_error("invalid_envelope", "session.ready is required first");
    enqueue_event(BackendEventType::error, "CONTROL BEFORE SESSION READY");
    cJSON_Delete(root);
    return;
  }

  if (type != protocol::ControlType::session_ready && protocol_connected_.load()) {
    const std::array<char, 64> expected_session = session_id_snapshot();
    if (json_string(root, "session_id") != expected_session.data()) {
      enqueue_protocol_error("invalid_envelope", "session_id does not match");
      enqueue_event(BackendEventType::error, "INVALID CONTROL SESSION");
      cJSON_Delete(root);
      return;
    }
  }

  const bool turn_scoped =
      type == protocol::ControlType::turn_abort ||
      type == protocol::ControlType::turn_state ||
      type == protocol::ControlType::transcript_final ||
      type == protocol::ControlType::tts_lifecycle ||
      type == protocol::ControlType::agent_status ||
      type == protocol::ControlType::ui_card ||
      type == protocol::ControlType::ui_state;
  const std::string_view incoming_turn_id = json_string(root, "turn_id");
  if (turn_scoped && incoming_turn_id.empty()) {
    enqueue_protocol_error("invalid_envelope", "turn-scoped control requires turn_id");
    enqueue_event(BackendEventType::error, "MISSING CONTROL TURN");
    cJSON_Delete(root);
    return;
  }
  if (turn_scoped ||
      (type == protocol::ControlType::protocol_error && !incoming_turn_id.empty())) {
    if (!active_turn_matches(incoming_turn_id)) {
      cJSON_Delete(root);
      return;
    }
  }

  if (type == protocol::ControlType::voice_mail_available) {
    VoiceMailMetadata item{};
    uint64_t expires = 0;
    uint32_t duration = 0, size = 0;
    parse_uint32(cJSON_GetObjectItemCaseSensitive(payload, "duration_ms"), duration);
    parse_uint32(cJSON_GetObjectItemCaseSensitive(payload, "size_bytes"), size);
    parse_rfc3339_utc_ms(json_string(payload, "expires_at"), expires);
    item.set_voice_mail_id(json_string(payload, "voice_mail_id"));
    item.set_from_device_id(json_string(payload, "from_device_id"));
    item.set_media_format(json_string(payload, "media_format"));
    item.set_checksum_sha256(json_string(payload, "checksum_sha256"));
    item.duration_ms = duration;
    item.size_bytes = size;
    item.expires_at_unix_ms = expires;
    item.policy = json_string(payload, "policy") == "ephemeral"
                      ? VoiceMailMetadata::Policy::ephemeral
                      : VoiceMailMetadata::Policy::retained;
    bool completes_active_result = false;
    taskENTER_CRITICAL(&voice_mail_lock_);
    completes_active_result = voice_mail_result_pending_ &&
                              active_voice_mail_.voice_mail_id_view() ==
                                  item.voice_mail_id_view() &&
                              std::string_view(active_result_key_.data()) ==
                                  json_string(root, "idempotency_key");
    taskEXIT_CRITICAL(&voice_mail_lock_);
    if (completes_active_result) clear_voice_mail(true);
    if (item.valid()) enqueue_voice_mail_event(BackendEventType::voice_mail_available, item);
  } else if (type == protocol::ControlType::voice_mail_claimed) {
    VoiceMailMetadata item{};
    uint64_t lease_expires_at_ms = 0;
    parse_rfc3339_utc_ms(json_string(payload, "lease_expires_at"),
                         lease_expires_at_ms);
    const std::time_t wall_now = std::time(nullptr);
    const bool lease_valid = wall_now < 1'577'836'800 ||
                             lease_expires_at_ms >
                                 static_cast<uint64_t>(wall_now) * 1'000;
    bool matches = false;
    taskENTER_CRITICAL(&voice_mail_lock_);
    matches = voice_mail_claim_pending_ &&
              active_voice_mail_.voice_mail_id_view() ==
                  json_string(payload, "voice_mail_id") &&
              std::string_view(active_playback_id_.data()) ==
                  json_string(payload, "playback_id") &&
              std::string_view(active_claim_key_.data()) ==
                  json_string(root, "idempotency_key");
    if (matches) {
      item = active_voice_mail_;
      voice_mail_claim_pending_ = false;
    }
    taskEXIT_CRITICAL(&voice_mail_lock_);
    if (matches && !lease_valid) {
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL CLAIM EXPIRED");
      clear_voice_mail(true);
      cJSON_Delete(root);
      return;
    }
    if (matches && !enqueue_media_job(item, json_string(payload, "playback_id"),
                                      json_string(payload, "media_ref"))) {
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL DOWNLOAD BUSY");
      clear_voice_mail(true);
    }
  } else if (type == protocol::ControlType::voice_mail_consumed) {
    VoiceMailMetadata item{};
    bool matches = false;
    taskENTER_CRITICAL(&voice_mail_lock_);
    const std::string_view playback_id = json_string(payload, "playback_id");
    matches = voice_mail_result_pending_ &&
              active_voice_mail_.voice_mail_id_view() ==
                  json_string(payload, "voice_mail_id") &&
              (playback_id.empty() || playback_id == active_playback_id_.data()) &&
              std::string_view(active_result_key_.data()) ==
                  json_string(root, "idempotency_key");
    if (matches) item = active_voice_mail_;
    taskEXIT_CRITICAL(&voice_mail_lock_);
    if (matches) {
      enqueue_voice_mail_event(BackendEventType::voice_mail_consumed, item);
      clear_voice_mail(true);
    }
  } else if (type == protocol::ControlType::voice_mail_expired) {
    VoiceMailMetadata item{};
    item.set_voice_mail_id(json_string(payload, "voice_mail_id"));
    bool active = false;
    taskENTER_CRITICAL(&voice_mail_lock_);
    active = active_voice_mail_.voice_mail_id_view() == item.voice_mail_id_view();
    if (active) item = active_voice_mail_;
    taskEXIT_CRITICAL(&voice_mail_lock_);
    enqueue_voice_mail_event(BackendEventType::voice_mail_expired, item);
    if (active) clear_voice_mail(true);
  } else if (type == protocol::ControlType::session_ready) {
    if (protocol_connected_.load()) {
      enqueue_protocol_error("invalid_envelope", "session.ready was already accepted");
      enqueue_event(BackendEventType::error, "DUPLICATE SESSION READY");
      cJSON_Delete(root);
      return;
    }
    const std::string_view session_id = json_string(root, "session_id");
    const std::string_view transport = json_string(payload, "transport");
    const cJSON* params = cJSON_GetObjectItemCaseSensitive(payload, "audio_params");
    const std::string_view format = params == nullptr ? std::string_view{} :
        json_string(params, "format");
    const cJSON* rate = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "sample_rate");
    const cJSON* channels = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "channels");
    const cJSON* duration = params == nullptr ? nullptr :
        cJSON_GetObjectItemCaseSensitive(params, "frame_duration");
    if (transport != "websocket" || format != "opus" ||
        !has_only_fields(params, {"format", "sample_rate", "channels",
                                  "frame_duration"}) ||
        !json_integer_equals(rate, 24'000) ||
        !json_integer_equals(channels, 1) ||
        !json_integer_equals(duration, 60) || !configure_decoder(24'000)) {
      enqueue_protocol_error("invalid_envelope", "unsupported session.ready transport or audio parameters");
      cJSON_Delete(root);
      enqueue_event(BackendEventType::error, "UNSUPPORTED OPUS HELLO");
      return;
    }
    playback_sample_rate_hz_.store(static_cast<uint32_t>(rate->valueint));
    if (session_id.empty() || !set_session_id(session_id)) {
      cJSON_Delete(root);
      enqueue_protocol_error("invalid_envelope", "session.ready requires a bounded session_id");
      enqueue_event(BackendEventType::error, "INVALID SESSION READY");
      return;
    }
    session_epoch_.fetch_add(1);
    reset_turn_queues();
    protocol_connected_.store(true);
    advertise_capabilities();
    enqueue_event(BackendEventType::connected);
  } else if (type == protocol::ControlType::transcript_final) {
    enqueue_event(BackendEventType::transcript, json_string(payload, "text"));
  } else if (type == protocol::ControlType::tts_lifecycle) {
    const std::string_view state = json_string(payload, "state");
    if (state == "start") {
      if (activate_tts_for_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_started);
    } else if (state == "sentence_start") {
      enqueue_event(BackendEventType::tts_sentence, json_string(payload, "text"));
    } else if (state == "stop") {
      if (deactivate_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_finished);
    }
  } else if (type == protocol::ControlType::alarm_fired) {
    const std::string_view alarm_id = json_string(payload, "alarm_id");
    enqueue_event(BackendEventType::alarm, json_string(payload, "message"));
    if (!alarm_id.empty()) enqueue_command(CommandType::alarm_ack, alarm_id);
  } else if (type == protocol::ControlType::schedule_updated) {
    enqueue_event(BackendEventType::schedule, json_string(payload, "message"));
  } else if (type == protocol::ControlType::ui_card) {
    PresentationCardV1 card{};
    if (parse_presentation_card(payload, card)) enqueue_card_event(card);
  } else if (type == protocol::ControlType::ui_state) {
    PresentationHint hint{};
    if (parse_presentation_hint(payload, hint)) enqueue_hint_event(hint);
  } else if (type == protocol::ControlType::agent_status) {
    AgentPresentationStatus status{};
    if (parse_agent_presentation_status(payload, status))
      enqueue_agent_status_event(status);
  } else if (type == protocol::ControlType::turn_state) {
    if (json_string(payload, "state") == "interrupted" &&
        deactivate_matching_turn(incoming_turn_id)) {
      reset_turn_queues();
    }
  } else if (type == protocol::ControlType::session_ping) {
    enqueue_pong(message_id);
  } else if (type == protocol::ControlType::session_pong) {
  } else if (type == protocol::ControlType::turn_abort) {
    if (deactivate_matching_turn(incoming_turn_id)) reset_turn_queues();
  } else if (type == protocol::ControlType::protocol_error) {
    bool applies = true;
    if (!incoming_turn_id.empty()) {
      applies = deactivate_matching_turn(incoming_turn_id);
    } else {
      taskENTER_CRITICAL(&turn_id_lock_);
      turn_active_.store(false);
      tts_active_.store(false);
      taskEXIT_CRITICAL(&turn_id_lock_);
    }
    if (applies) {
      reset_turn_queues();
      enqueue_event(BackendEventType::error, json_string(payload, "code"));
    }
  } else {
    enqueue_protocol_error("invalid_envelope", "control type is invalid in this direction");
    enqueue_event(BackendEventType::error, "INVALID CONTROL DIRECTION");
  }
  cJSON_Delete(root);
}

void WebSocketVoiceBackend::handle_binary(
    const esp_websocket_event_data_t& data) {
  if (!tts_active_.load()) return;
  const size_t offset = static_cast<size_t>(data.payload_offset);
  const size_t length = static_cast<size_t>(data.data_len);
  const size_t expected = static_cast<size_t>(data.payload_len);
  if (expected == 0 || expected > kMaximumOpusPacketBytes ||
      offset + length > expected) {
    taskENTER_CRITICAL(&media_buffer_lock_);
    binary_payload_ = {};
    taskEXIT_CRITICAL(&media_buffer_lock_);
    enqueue_event(BackendEventType::error, "INVALID TTS FRAME");
    return;
  }
  OpusPacket packet{};
  bool packet_ready = false;
  uint64_t generation = 0;
  taskENTER_CRITICAL(&media_buffer_lock_);
  if (offset == 0) binary_payload_ = {};
  if (offset != binary_payload_.count) {
    binary_payload_ = {};
    taskEXIT_CRITICAL(&media_buffer_lock_);
    enqueue_event(BackendEventType::error, "OUT OF ORDER TTS FRAME");
    return;
  }
  std::memcpy(binary_payload_.bytes.data() + offset, data.data_ptr, length);
  binary_payload_.count = static_cast<uint16_t>(offset + length);
  if (offset + length == expected) {
    packet = binary_payload_;
    binary_payload_ = {};
    packet_ready = true;
    generation = media_generation_.load();
  }
  taskEXIT_CRITICAL(&media_buffer_lock_);
  if (packet_ready && tts_active_.load() &&
      generation == media_generation_.load() &&
      !decode_and_enqueue(packet, generation))
    enqueue_event(BackendEventType::error, "OPUS DECODE FAILED");
}

bool WebSocketVoiceBackend::encode_and_enqueue(
    std::span<const int16_t, kOpusFrameSamples> pcm,
    uint64_t media_generation) {
  if (opus_encoder_ == nullptr) return false;
  OpusPacket packet{};
  esp_audio_enc_in_frame_t input{
      .buffer = reinterpret_cast<uint8_t*>(const_cast<int16_t*>(pcm.data())),
      .len = static_cast<uint32_t>(pcm.size_bytes()),
  };
  esp_audio_enc_out_frame_t output{};
  output.buffer = packet.bytes.data();
  output.len = static_cast<uint32_t>(packet.bytes.size());
  if (esp_opus_enc_process(opus_encoder_, &input, &output) != ESP_AUDIO_ERR_OK ||
      output.encoded_bytes == 0 || output.encoded_bytes > packet.bytes.size()) {
    return false;
  }
  packet.count = static_cast<uint16_t>(output.encoded_bytes);
  return enqueue_audio(packet, media_generation);
}

bool WebSocketVoiceBackend::configure_decoder(uint32_t sample_rate_hz) {
  if (sample_rate_hz != 16'000 && sample_rate_hz != 24'000) return false;
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreTake(opus_decoder_mutex_, portMAX_DELAY);
  }
  if (opus_decoder_ != nullptr) {
    esp_opus_dec_close(opus_decoder_);
    opus_decoder_ = nullptr;
  }
  esp_opus_dec_cfg_t config{
      .sample_rate = sample_rate_hz,
      .channel = ESP_AUDIO_MONO,
      .frame_duration = static_cast<esp_opus_dec_frame_duration_t>(
          ESP_OPUS_ENC_FRAME_DURATION_60_MS),
      .self_delimited = false,
  };
  const bool ok = esp_opus_dec_open(&config, sizeof(config), &opus_decoder_) ==
                     ESP_AUDIO_ERR_OK &&
                 opus_decoder_ != nullptr;
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreGive(opus_decoder_mutex_);
  }
  return ok;
}

bool WebSocketVoiceBackend::decode_and_enqueue(const OpusPacket& packet,
                                               uint64_t media_generation) {
  if (packet.count == 0) return false;
  std::array<int16_t, kMaximumDecodedSamples> decoded{};
  esp_audio_dec_in_raw_t input{
      .buffer = const_cast<uint8_t*>(packet.bytes.data()),
      .len = packet.count,
      .consumed = 0,
      .frame_recover = ESP_AUDIO_DEC_RECOVERY_NONE,
  };
  esp_audio_dec_out_frame_t output{};
  output.buffer = reinterpret_cast<uint8_t*>(decoded.data());
  output.len = static_cast<uint32_t>(decoded.size() * sizeof(int16_t));
  esp_audio_dec_info_t info{};
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreTake(opus_decoder_mutex_, portMAX_DELAY);
  }
  if (opus_decoder_ == nullptr) {
    if (opus_decoder_mutex_ != nullptr) xSemaphoreGive(opus_decoder_mutex_);
    return false;
  }
  const esp_err_t decode_err = esp_opus_dec_decode(opus_decoder_, &input, &output, &info);
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreGive(opus_decoder_mutex_);
  }
  if (decode_err != ESP_AUDIO_ERR_OK ||
      output.decoded_size == 0 || output.decoded_size % sizeof(int16_t) != 0) {
    return false;
  }
  const size_t decoded_samples = output.decoded_size / sizeof(int16_t);
  for (size_t offset = 0; offset < decoded_samples;) {
    AudioFrame frame{};
    frame.count = static_cast<uint16_t>(
        std::min(kAudioFrameSamples, decoded_samples - offset));
    std::copy_n(decoded.begin() + offset, frame.count, frame.samples.begin());
    taskENTER_CRITICAL(&media_buffer_lock_);
    const bool still_current = tts_active_.load() &&
                               media_generation == media_generation_.load();
    const bool queued = still_current &&
                        xQueueSend(playback_queue_, &frame, 0) == pdPASS;
    taskEXIT_CRITICAL(&media_buffer_lock_);
    if (!still_current) return true;
    if (!queued) return false;
    offset += frame.count;
  }
  return true;
}

bool WebSocketVoiceBackend::enqueue_command(CommandType type,
                                            std::string_view turn,
                                            ListenMode mode) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = type;
  outbound.command.mode = mode;
  if (!copy_string(outbound.command.turn_id, turn)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_pong(std::string_view correlation_id) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::session_pong;
  if (!copy_string(outbound.command.correlation_id, correlation_id)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_protocol_error(std::string_view code,
                                                    std::string_view message) {
  Outbound outbound{};
  outbound.type = OutboundType::control;
  outbound.command.type = CommandType::protocol_error;
  if (!copy_string(outbound.command.code, code) ||
      !copy_string(outbound.command.message, message)) return false;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_audio(const OpusPacket& frame,
                                          uint64_t media_generation) {
  if (!turn_active_.load() || media_generation != media_generation_.load()) return false;
  Outbound outbound{};
  outbound.type = OutboundType::audio;
  outbound.audio = frame;
  outbound.media_generation = media_generation;
  return xQueueSend(outbound_queue_, &outbound, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_event(BackendEventType type,
                                          std::string_view text) {
  BackendEvent event{};
  event.type = type;
  event.scope = scope_for_event_type(type);
  event.session_epoch = session_epoch_.load();
  event.generation = media_generation_.load();
  event.set_text(text);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_card_event(const PresentationCardV1& card) {
  BackendEvent event{};
  event.type = BackendEventType::presentation_card;
  event.scope = BackendEventScope::generation;
  event.session_epoch = session_epoch_.load();
  event.generation = media_generation_.load();
  event.set_card(card);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_hint_event(const PresentationHint& hint) {
  BackendEvent event{};
  event.type = BackendEventType::presentation_hint;
  event.scope = BackendEventScope::generation;
  event.session_epoch = session_epoch_.load();
  event.generation = media_generation_.load();
  event.set_hint(hint);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_agent_status_event(
    const AgentPresentationStatus& status) {
  BackendEvent event{};
  event.type = BackendEventType::agent_status;
  event.scope = BackendEventScope::generation;
  event.session_epoch = session_epoch_.load();
  event.generation = media_generation_.load();
  event.set_agent_status(status);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::enqueue_settings_event(const SettingsTwin& settings) {
  BackendEvent event{};
  event.type = BackendEventType::settings;
  event.scope = BackendEventScope::session;
  event.session_epoch = session_epoch_.load();
  event.generation = 0;
  event.set_settings(settings);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}


bool WebSocketVoiceBackend::enqueue_voice_mail_event(
    BackendEventType type, const VoiceMailMetadata& item, std::string_view text) {
  BackendEvent event{};
  event.type = type;
  event.scope = scope_for_event_type(type);
  event.session_epoch = session_epoch_.load();
  event.generation = voice_mail_generation_.load();
  event.set_voice_mail(item);
  event.set_text(text);
  return xQueueSend(event_queue_, &event, 0) == pdPASS;
}

bool WebSocketVoiceBackend::build_http_origin(std::string_view websocket_url) {
  std::string_view scheme;
  if (websocket_url.starts_with("wss://")) {
    scheme = "https://";
    websocket_url.remove_prefix(6);
  } else if (websocket_url.starts_with("ws://")) {
    scheme = "http://";
    websocket_url.remove_prefix(5);
  } else {
    return false;
  }
  const size_t slash = websocket_url.find('/');
  const std::string_view authority = websocket_url.substr(0, slash);
  if (authority.empty() || authority.find('@') != std::string_view::npos ||
      authority.find('#') != std::string_view::npos ||
      scheme.size() + authority.size() >= http_origin_.size()) return false;
  http_origin_.fill('\0');
  std::copy(scheme.begin(), scheme.end(), http_origin_.begin());
  std::copy(authority.begin(), authority.end(), http_origin_.begin() + scheme.size());
  return true;
}

bool WebSocketVoiceBackend::enqueue_media_job(const VoiceMailMetadata& item,
                                              std::string_view playback_id,
                                              std::string_view media_ref) {
  MediaJob job{};
  job.voice_mail = item;
  job.generation = voice_mail_generation_.fetch_add(1) + 1;
  if (!copy_string(job.playback_id, playback_id) ||
      !copy_string(job.media_ref, media_ref)) return false;
  xQueueReset(media_queue_);
  xQueueReset(playback_queue_);
  return xQueueSend(media_queue_, &job, 0) == pdPASS;
}

bool WebSocketVoiceBackend::voice_mail_job_current(const MediaJob& job) const {
  return job.generation == voice_mail_generation_.load();
}

void WebSocketVoiceBackend::media_loop() {
  while (!stopping_.load()) {
    MediaJob job{};
    if (xQueueReceive(media_queue_, &job, pdMS_TO_TICKS(100)) != pdPASS) continue;
    if (stopping_.load()) break;
    const bool valid = download_voice_mail(job, false);
    const bool decoded = valid && voice_mail_job_current(job) &&
                         download_voice_mail(job, true);
    if (!decoded && voice_mail_job_current(job)) {
      xQueueReset(playback_queue_);
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed,
                               job.voice_mail, "VOICE MAIL DATA ERROR");
    }
  }
}

bool WebSocketVoiceBackend::download_voice_mail(const MediaJob& job, bool decode) {
  if (!voice_mail_job_current(job) || !job.voice_mail.valid() ||
      job.voice_mail.size_bytes > kMaximumVoiceMailBytes) return false;
  std::array<char, 385> escaped_playback{};
  size_t escaped_size = 0;
  if (!url_encode(job.playback_id.data(), escaped_playback, escaped_size)) return false;
  std::array<char, 1'024> url{};
  const int url_size = std::snprintf(
      url.data(), url.size(), "%s%s?playback_id=%.*s", http_origin_.data(),
      job.media_ref.data(), static_cast<int>(escaped_size), escaped_playback.data());
  if (url_size <= 0 || static_cast<size_t>(url_size) >= url.size()) return false;

  esp_http_client_config_t config{};
  config.url = url.data();
  config.timeout_ms = 10'000;
  config.buffer_size = 2'048;
  config.buffer_size_tx = 512;
  config.keep_alive_enable = false;
  config.crt_bundle_attach = esp_crt_bundle_attach;
  esp_http_client_handle_t client = esp_http_client_init(&config);
  if (client == nullptr) return false;
  std::array<char, 224> authorization{};
  const int auth_size = std::snprintf(authorization.data(), authorization.size(),
                                      "Bearer %s", token_.data());
  bool ok = auth_size > 7 && static_cast<size_t>(auth_size) < authorization.size() &&
            esp_http_client_set_header(client, "Authorization",
                                       authorization.data()) == ESP_OK &&
            esp_http_client_set_header(client, "Device-Id", device_id_.data()) == ESP_OK &&
            esp_http_client_open(client, 0) == ESP_OK;
  int64_t content_length = -1;
  if (ok) {
    content_length = esp_http_client_fetch_headers(client);
    const bool chunked = esp_http_client_is_chunked_response(client);
    ok = (content_length >= 0 || chunked) &&
         esp_http_client_get_status_code(client) == 200 &&
         (chunked || content_length == 0 ||
          content_length == static_cast<int64_t>(job.voice_mail.size_bytes));
  }

  struct DecodeContext {
    WebSocketVoiceBackend* backend;
    const MediaJob* job;
    uint64_t decoded_samples{};
    bool ready_sent{};
  } decode_context{this, &job};
  const auto packet_handler = [](void* opaque, std::span<const uint8_t> packet) {
    auto& context = *static_cast<DecodeContext*>(opaque);
    return context.backend->decode_voice_mail_packet(
        *context.job, packet, context.decoded_samples, context.ready_sent);
  };
  OggOpusParser parser(decode ? packet_handler : nullptr,
                       decode ? &decode_context : nullptr);
  ok = ok && parser.ready();
  psa_hash_operation_t sha = PSA_HASH_OPERATION_INIT;
  const bool hash_started = ok && psa_crypto_init() == PSA_SUCCESS &&
                            psa_hash_setup(&sha, PSA_ALG_SHA_256) == PSA_SUCCESS;
  ok = ok && hash_started;
  std::array<uint8_t, 2'048> buffer{};
  size_t total = 0;
  while (ok && voice_mail_job_current(job) && !stopping_.load()) {
    const int count = esp_http_client_read(
        client, reinterpret_cast<char*>(buffer.data()), buffer.size());
    if (count < 0) {
      ok = false;
      break;
    }
    if (count == 0) break;
    total += static_cast<size_t>(count);
    if (total > job.voice_mail.size_bytes || total > kMaximumVoiceMailBytes ||
        psa_hash_update(&sha, buffer.data(), static_cast<size_t>(count)) !=
            PSA_SUCCESS ||
        !parser.feed(std::span<const uint8_t>(buffer.data(), count))) {
      ok = false;
    }
  }
  unsigned char digest[32]{};
  size_t digest_size = 0;
  const bool hash_finished = hash_started &&
                             psa_hash_finish(&sha, digest, sizeof(digest),
                                             &digest_size) == PSA_SUCCESS &&
                             digest_size == sizeof(digest);
  ok = ok && voice_mail_job_current(job) &&
       total == job.voice_mail.size_bytes && parser.finish() &&
       hash_finished &&
       sha256_matches(digest, job.voice_mail.checksum_sha256.data());
  if (!hash_finished) (void)psa_hash_abort(&sha);
  esp_http_client_close(client);
  esp_http_client_cleanup(client);

  if (ok && decode) {
    const uint64_t decoded_ms = decode_context.decoded_samples * 1'000 /
                                playback_sample_rate_hz_.load();
    const uint64_t expected = job.voice_mail.duration_ms;
    ok = decode_context.ready_sent && decoded_ms <= expected + 2'000 &&
         decoded_ms + 2'000 >= expected;
    if (ok) ok = enqueue_voice_mail_event(
        BackendEventType::voice_mail_playback_finished, job.voice_mail);
  }
  return ok;
}

bool WebSocketVoiceBackend::decode_voice_mail_packet(
    const MediaJob& job, std::span<const uint8_t> packet,
    uint64_t& decoded_samples, bool& ready_sent) {
  if (!voice_mail_job_current(job) || packet.empty() ||
      packet.size() > kMaximumOpusPacketBytes) return false;
  std::array<int16_t, kMaximumDecodedSamples> decoded{};
  esp_audio_dec_in_raw_t input{
      .buffer = const_cast<uint8_t*>(packet.data()),
      .len = static_cast<uint32_t>(packet.size()),
      .consumed = 0,
      .frame_recover = ESP_AUDIO_DEC_RECOVERY_NONE,
  };
  esp_audio_dec_out_frame_t output{};
  output.buffer = reinterpret_cast<uint8_t*>(decoded.data());
  output.len = static_cast<uint32_t>(decoded.size() * sizeof(int16_t));
  esp_audio_dec_info_t info{};
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreTake(opus_decoder_mutex_, portMAX_DELAY);
  }
  if (opus_decoder_ == nullptr) {
    if (opus_decoder_mutex_ != nullptr) xSemaphoreGive(opus_decoder_mutex_);
    return false;
  }
  const esp_err_t decode_err = esp_opus_dec_decode(opus_decoder_, &input, &output, &info);
  if (opus_decoder_mutex_ != nullptr) {
    xSemaphoreGive(opus_decoder_mutex_);
  }
  if (decode_err != ESP_AUDIO_ERR_OK || output.decoded_size == 0 ||
      output.decoded_size % sizeof(int16_t) != 0) return false;
  const size_t sample_count = output.decoded_size / sizeof(int16_t);
  if (decoded_samples + sample_count >
      static_cast<uint64_t>(playback_sample_rate_hz_.load()) * 600 +
          kMaximumDecodedSamples) return false;
  for (size_t offset = 0; offset < sample_count;) {
    AudioFrame frame{};
    frame.count = static_cast<uint16_t>(
        std::min(kAudioFrameSamples, sample_count - offset));
    std::copy_n(decoded.begin() + offset, frame.count, frame.samples.begin());
    while (voice_mail_job_current(job) &&
           xQueueSend(playback_queue_, &frame, pdMS_TO_TICKS(100)) != pdPASS) {}
    if (!voice_mail_job_current(job)) return false;
    if (!ready_sent) {
      ready_sent = enqueue_voice_mail_event(
          BackendEventType::voice_mail_playback_ready, job.voice_mail);
      if (!ready_sent) return false;
    }
    offset += frame.count;
  }
  decoded_samples += sample_count;
  return true;
}

void WebSocketVoiceBackend::clear_voice_mail(bool reset_playback) {
  voice_mail_generation_.fetch_add(1);
  xQueueReset(media_queue_);
  if (reset_playback) xQueueReset(playback_queue_);
  taskENTER_CRITICAL(&voice_mail_lock_);
  active_voice_mail_ = {};
  active_playback_id_.fill('\0');
  active_claim_key_.fill('\0');
  active_result_key_.fill('\0');
  voice_mail_claim_pending_ = false;
  voice_mail_result_pending_ = false;
  taskEXIT_CRITICAL(&voice_mail_lock_);
}

bool WebSocketVoiceBackend::send_text(std::string_view text) {
  if (!socket_connected_.load()) return false;
  const int written = esp_websocket_client_send_text(
      client_, text.data(), static_cast<int>(text.size()), pdMS_TO_TICKS(1'000));
  return written == static_cast<int>(text.size());
}

bool WebSocketVoiceBackend::set_session_id(std::string_view session_id) {
  taskENTER_CRITICAL(&session_id_lock_);
  const bool copied = copy_string(session_id_, session_id);
  taskEXIT_CRITICAL(&session_id_lock_);
  return copied;
}

void WebSocketVoiceBackend::clear_session_id() {
  taskENTER_CRITICAL(&session_id_lock_);
  session_id_.fill('\0');
  taskEXIT_CRITICAL(&session_id_lock_);
}

std::array<char, 64> WebSocketVoiceBackend::session_id_snapshot() {
  std::array<char, 64> session_id{};
  taskENTER_CRITICAL(&session_id_lock_);
  session_id = session_id_;
  taskEXIT_CRITICAL(&session_id_lock_);
  return session_id;
}

std::array<char, 40> WebSocketVoiceBackend::active_turn_id_snapshot() {
  std::array<char, 40> turn_id{};
  taskENTER_CRITICAL(&turn_id_lock_);
  turn_id = active_turn_id_;
  taskEXIT_CRITICAL(&turn_id_lock_);
  return turn_id;
}

bool WebSocketVoiceBackend::active_turn_matches(std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = (turn_active_.load() || tts_active_.load()) &&
                       turn_id == active_turn_id_.data();
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

bool WebSocketVoiceBackend::activate_tts_for_matching_turn(
    std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = turn_active_.load() &&
                       turn_id == active_turn_id_.data();
  if (matches) tts_active_.store(true);
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

bool WebSocketVoiceBackend::deactivate_matching_turn(std::string_view turn_id) {
  taskENTER_CRITICAL(&turn_id_lock_);
  const bool matches = (turn_active_.load() || tts_active_.load()) &&
                       turn_id == active_turn_id_.data();
  if (matches) {
    turn_active_.store(false);
    tts_active_.store(false);
  }
  taskEXIT_CRITICAL(&turn_id_lock_);
  return matches;
}

void WebSocketVoiceBackend::reset_turn_queues() {
  taskENTER_CRITICAL(&media_buffer_lock_);
  media_generation_.fetch_add(1);
  xQueueReset(playback_queue_);
  binary_payload_ = {};
  upload_payload_ = {};
  upload_payload_size_ = 0;
  taskEXIT_CRITICAL(&media_buffer_lock_);
}

} // namespace companion

#include "websocket_backend.hpp"

#include "companion/wire_protocol.hpp"

#include <boost/asio/connect.hpp>
#include <boost/asio/ip/tcp.hpp>
#include <boost/asio/post.hpp>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>
#include <boost/beast/websocket.hpp>
#include <nlohmann/json.hpp>
#include <opus/opus.h>
#include <opus/opusfile.h>
#include <openssl/rand.h>
#include <openssl/sha.h>

#include <algorithm>
#include <array>
#include <cctype>
#include <chrono>
#include <cstring>
#include <ctime>
#include <iomanip>
#include <initializer_list>
#include <iostream>
#include <limits>
#include <sstream>
#include <stdexcept>
#include <utility>

#if !defined(_WIN32)
#include <sys/socket.h>
#include <sys/time.h>
#endif

namespace companion::software_device {
namespace beast = boost::beast;
namespace http = beast::http;
namespace websocket = boost::beast::websocket;
namespace net = boost::asio;
using tcp = net::ip::tcp;
using json = nlohmann::json;

namespace {
constexpr size_t kUplinkFrameSamples = 16'000 * 60 / 1000;
constexpr size_t kDownlinkFrameSamples = 24'000 * 60 / 1000;
constexpr size_t kMaximumOpusPacketBytes = 1275;
constexpr size_t kMaximumPlaybackSamples = 24'000 * 10;
constexpr size_t kMaximumVoiceMailBytes = 32 * 1024 * 1024;
constexpr size_t kMaximumVoiceMailSamples = 48'000 * 600;
constexpr size_t kMaximumEvents = 128;

struct ParsedURL {
  std::string host;
  std::string port;
  std::string target;
};

std::optional<ParsedURL> parse_ws_url(const std::string& url) {
  constexpr std::string_view prefix = "ws://";
  if (!url.starts_with(prefix)) return std::nullopt;
  const std::string remainder = url.substr(prefix.size());
  const size_t slash = remainder.find('/');
  const std::string authority = remainder.substr(0, slash);
  const std::string target = slash == std::string::npos ? "/" : remainder.substr(slash);
  const size_t colon = authority.rfind(':');
  if (authority.empty()) return std::nullopt;
  if (colon == std::string::npos) return ParsedURL{authority, "80", target};
  if (colon == 0 || colon + 1 >= authority.size()) return std::nullopt;
  return ParsedURL{authority.substr(0, colon), authority.substr(colon + 1), target};
}

std::string json_string(const json& object, const char* key) {
  const auto it = object.find(key);
  return it != object.end() && it->is_string() ? it->get<std::string>() : std::string{};
}

bool has_only_fields(const json& object,
                     std::initializer_list<std::string_view> allowed) {
  if (!object.is_object()) return false;
  for (const auto& [key, value] : object.items()) {
    (void)value;
    if (std::find(allowed.begin(), allowed.end(), std::string_view(key)) ==
        allowed.end()) {
      return false;
    }
  }
  return true;
}

bool exact_uint64(const json& value, uint64_t& output) {
  if (!value.is_number_integer()) return false;
  if (value.is_number_unsigned()) {
    output = value.get<uint64_t>();
    return output <= 9'007'199'254'740'991ULL;
  }
  const int64_t signed_value = value.get<int64_t>();
  if (signed_value < 0) return false;
  output = static_cast<uint64_t>(signed_value);
  return output <= 9'007'199'254'740'991ULL;
}

bool exact_uint32(const json& value, uint32_t& output) {
  uint64_t parsed = 0;
  if (!exact_uint64(value, parsed) || parsed > std::numeric_limits<uint32_t>::max()) {
    return false;
  }
  output = static_cast<uint32_t>(parsed);
  return true;
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

bool parse_settings_arguments(const json& arguments,
                              const DeviceSettings& current,
                              uint64_t current_version,
                              SettingsTwin& output,
                              bool& duplicate) {
  if (!has_only_fields(arguments, {"version", "settings"})) return false;
  const auto version_it = arguments.find("version");
  const auto settings_it = arguments.find("settings");
  uint64_t version = 0;
  if (version_it == arguments.end() || !exact_uint64(*version_it, version) || version == 0 ||
      settings_it == arguments.end() || !settings_it->is_object() ||
      !has_only_fields(*settings_it,
                       {"smart_vad_enabled", "vad_threshold", "vad_silence_ms",
                        "vad_min_speech_ms", "idle_after_ms", "alarm_visible_ms",
                        "ota_poll_interval_s", "wake_model"})) {
    return false;
  }
  if (version < current_version) return false;

  DeviceSettings parsed = current;
  const json& settings = *settings_it;
  if (const auto it = settings.find("smart_vad_enabled"); it != settings.end()) {
    if (!it->is_boolean()) return false;
    parsed.smart_vad_enabled = it->get<bool>();
  }
  auto parse_u32 = [&](const char* key, uint32_t& destination) {
    const auto it = settings.find(key);
    if (it == settings.end()) return true;
    uint32_t value = 0;
    if (!exact_uint32(*it, value)) return false;
    destination = value;
    return true;
  };
  if (!parse_u32("vad_threshold", parsed.vad_threshold) ||
      !parse_u32("vad_silence_ms", parsed.vad_silence_ms) ||
      !parse_u32("vad_min_speech_ms", parsed.vad_min_speech_ms) ||
      !parse_u32("idle_after_ms", parsed.idle_after_ms) ||
      !parse_u32("alarm_visible_ms", parsed.alarm_visible_ms) ||
      !parse_u32("ota_poll_interval_s", parsed.ota_poll_interval_s)) {
    return false;
  }
  if (const auto it = settings.find("wake_model"); it != settings.end()) {
    if (!it->is_string()) return false;
    const std::string model = it->get<std::string>();
    if (model.empty() || model.size() >= parsed.wake_model.size()) return false;
    parsed.set_wake_model(model);
  }
  if (!parsed.validate()) return false;

  duplicate = version == current_version;
  if (duplicate && !same_settings(parsed, current)) return false;
  output = SettingsTwin{.version = version, .settings = parsed};
  return true;
}

bool optional_json_string(const json& object, const char* key, std::string& output) {
  const auto it = object.find(key);
  if (it == object.end()) {
    output.clear();
    return true;
  }
  if (!it->is_string()) return false;
  output = it->get<std::string>();
  return true;
}

bool parse_presentation_card(const json& payload, PresentationCardV1& output) {
  const auto ui_it = payload.find("ui");
  if (ui_it == payload.end() || !ui_it->is_object()) return false;
  const json& ui = *ui_it;
  if (!has_only_fields(ui, {"version", "kind", "title", "primary", "secondary",
                            "progress"})) {
    return false;
  }
  const auto version = ui.find("version");
  const auto kind = ui.find("kind");
  if (version == ui.end() || !version->is_number_integer() || version->get<int64_t>() != 1 ||
      kind == ui.end() || !kind->is_string()) {
    return false;
  }
  std::string title;
  std::string primary;
  std::string secondary;
  if (!optional_json_string(ui, "title", title) ||
      !optional_json_string(ui, "primary", primary) ||
      !optional_json_string(ui, "secondary", secondary)) {
    return false;
  }
  int64_t progress = 0;
  const auto progress_it = ui.find("progress");
  if (progress_it != ui.end()) {
    if (!progress_it->is_number_integer()) return false;
    progress = progress_it->get<int64_t>();
  }
  if (progress < 0 || progress > 100) return false;
  return output.assign(1, kind->get<std::string>(), title, primary, secondary,
                       static_cast<int>(progress));
}

bool parse_presentation_hint(const json& payload, PresentationHint& output) {
  if (!has_only_fields(payload, {"emotion", "tool_name"})) return false;
  const auto emotion = payload.find("emotion");
  if (emotion == payload.end() || !emotion->is_string()) return false;
  std::string tool_name;
  if (!optional_json_string(payload, "tool_name", tool_name)) return false;
  return output.assign(emotion->get<std::string>(), tool_name);
}

bool parse_agent_presentation_status(const json& payload,
                                     AgentPresentationStatus& output) {
  if (!has_only_fields(payload, {"state"})) return false;
  const auto state = payload.find("state");
  return state != payload.end() && state->is_string() &&
         output.assign(state->get<std::string>());
}

std::string rfc3339_now() {
  const auto now = std::chrono::system_clock::now();
  const std::time_t seconds = std::chrono::system_clock::to_time_t(now);
  std::tm utc{};
  gmtime_r(&seconds, &utc);
  std::ostringstream out;
  out << std::put_time(&utc, "%Y-%m-%dT%H:%M:%SZ");
  return out.str();
}

std::string random_opaque_id(std::string_view prefix) {
  std::array<unsigned char, 16> bytes{};
  if (RAND_bytes(bytes.data(), bytes.size()) != 1) return {};
  std::ostringstream out;
  out << prefix << std::hex << std::setfill('0');
  for (const unsigned char byte : bytes) out << std::setw(2) << static_cast<int>(byte);
  return out.str();
}

std::string sha256_hex(std::span<const uint8_t> bytes) {
  std::array<unsigned char, SHA256_DIGEST_LENGTH> digest{};
  SHA256(bytes.data(), bytes.size(), digest.data());
  std::ostringstream out;
  out << std::hex << std::setfill('0');
  for (const unsigned char byte : digest) out << std::setw(2) << static_cast<int>(byte);
  return out.str();
}

std::optional<uint64_t> parse_rfc3339_unix_ms(const std::string& value) {
  if (value.size() < 20 || value.back() != 'Z') return std::nullopt;
  std::tm utc{};
  std::istringstream input(value.substr(0, 19));
  input >> std::get_time(&utc, "%Y-%m-%dT%H:%M:%S");
  if (input.fail()) return std::nullopt;
  uint64_t milliseconds = 0;
  if (value.size() > 20) {
    if (value[19] != '.') return std::nullopt;
    const std::string fraction = value.substr(20, value.size() - 21);
    if (fraction.empty() || fraction.size() > 9 ||
        !std::all_of(fraction.begin(), fraction.end(),
                     [](unsigned char value) { return std::isdigit(value) != 0; })) {
      return std::nullopt;
    }
    std::string millis = fraction.substr(0, std::min<size_t>(3, fraction.size()));
    millis.append(3 - millis.size(), '0');
    milliseconds = static_cast<uint64_t>(std::stoul(millis));
  }
  const std::time_t seconds = timegm(&utc);
  if (seconds < 0) return std::nullopt;
  std::array<char, 20> normalized{};
  if (std::strftime(normalized.data(), normalized.size(), "%Y-%m-%dT%H:%M:%S", &utc) == 0 ||
      value.substr(0, 19) != normalized.data()) {
    return std::nullopt;
  }
  return static_cast<uint64_t>(seconds) * 1000 + milliseconds;
}

bool valid_opaque_id(std::string_view value, size_t maximum) {
  if (value.empty() || value.size() > maximum) return false;
  return std::any_of(value.begin(), value.end(),
                     [](unsigned char character) { return !std::isspace(character); });
}

bool parse_voice_mail_metadata(const json& payload, VoiceMailMetadata& item) {
  if (!payload.is_object() || !payload.contains("duration_ms") ||
      !payload.at("duration_ms").is_number_unsigned() ||
      !payload.contains("size_bytes") || !payload.at("size_bytes").is_number_unsigned()) {
    return false;
  }
  const uint64_t duration = payload.at("duration_ms").get<uint64_t>();
  const uint64_t size = payload.at("size_bytes").get<uint64_t>();
  const std::string voice_mail_id = json_string(payload, "voice_mail_id");
  const std::string from_device_id = json_string(payload, "from_device_id");
  const std::string media_format = json_string(payload, "media_format");
  const std::string checksum = json_string(payload, "checksum_sha256");
  const std::string expires_at = json_string(payload, "expires_at");
  const std::string policy = json_string(payload, "policy");
  const auto expires_at_ms = parse_rfc3339_unix_ms(expires_at);
  if (!valid_opaque_id(voice_mail_id, 128) ||
      !valid_opaque_id(from_device_id, 128) || media_format != "ogg_opus" ||
      checksum.size() != 64 ||
      !std::all_of(checksum.begin(), checksum.end(), [](unsigned char value) {
        return std::isxdigit(value) != 0;
      }) || duration == 0 || duration > 600'000 || size == 0 ||
      size > kMaximumVoiceMailBytes || !expires_at_ms ||
      (policy != "ephemeral" && policy != "retained")) {
    return false;
  }
  item.set_voice_mail_id(voice_mail_id);
  item.set_from_device_id(from_device_id);
  item.set_media_format(media_format);
  item.set_checksum_sha256(checksum);
  item.duration_ms = static_cast<uint32_t>(duration);
  item.size_bytes = static_cast<uint32_t>(size);
  item.expires_at_unix_ms = *expires_at_ms;
  item.policy = policy == "ephemeral" ? VoiceMailMetadata::Policy::ephemeral
                                      : VoiceMailMetadata::Policy::retained;
  return item.valid();
}

bool safe_media_ref(std::string_view ref) {
  return ref.size() <= 256 && ref.starts_with("/v1/voice-mail/") &&
         ref.ends_with("/media") &&
         ref.find("..") == std::string_view::npos &&
         ref.find('?') == std::string_view::npos &&
         ref.find('#') == std::string_view::npos;
}
} // namespace

class WebSocketVoiceBackend::Connection final
    : public std::enable_shared_from_this<WebSocketVoiceBackend::Connection> {
public:
  struct Outgoing {
    bool text{};
    std::shared_ptr<std::string> bytes;
  };

  Connection(WebSocketVoiceBackend& owner, uint64_t generation, ParsedURL url,
             std::string token, std::string device_id)
      : owner_(owner), generation_(generation), url_(std::move(url)),
        token_(std::move(token)), device_id_(std::move(device_id)), resolver_(ioc_),
        websocket_(ioc_) {}

  void run() {
    resolver_.async_resolve(
        url_.host, url_.port,
        [self = shared_from_this()](beast::error_code error,
                                    tcp::resolver::results_type results) {
          if (error) return self->fail("resolve: " + error.message());
          beast::get_lowest_layer(self->websocket_).expires_after(std::chrono::seconds(5));
          beast::get_lowest_layer(self->websocket_).async_connect(
              results, [self](beast::error_code connect_error,
                              const tcp::resolver::results_type::endpoint_type&) {
                if (connect_error) return self->fail("connect: " + connect_error.message());
                beast::get_lowest_layer(self->websocket_).expires_never();
                self->websocket_.set_option(websocket::stream_base::timeout::suggested(
                    beast::role_type::client));
                self->websocket_.set_option(websocket::stream_base::decorator(
                    [self](websocket::request_type& request) {
                      request.set(http::field::authorization, "Bearer " + self->token_);
                      request.set("Protocol-Version", "2");
                      request.set("Device-Id", self->device_id_);
                      request.set("Client-Id", "companion-software-device");
                    }));
                self->websocket_.async_handshake(
                    self->url_.host + ":" + self->url_.port, self->url_.target,
                    [self](beast::error_code handshake_error) {
                      if (handshake_error)
                        return self->fail("handshake: " + handshake_error.message());
                      self->open_.store(true);
                      self->owner_.handle_connection_open(self->generation_);
                      self->read_next();
                    });
              });
        });
    ioc_.run();
  }

  void send_text(std::string message) { enqueue(true, std::move(message)); }
  void send_binary(std::vector<uint8_t> packet) {
    enqueue(false, std::string(reinterpret_cast<const char*>(packet.data()), packet.size()));
  }

  bool open() const { return open_.load(); }

  void stop(bool notify) {
    notify_on_close_.store(notify);
    net::post(ioc_, [self = shared_from_this()] {
      self->open_.store(false);
      beast::error_code ignored;
      auto& socket = beast::get_lowest_layer(self->websocket_).socket();
      socket.cancel(ignored);
      socket.shutdown(tcp::socket::shutdown_both, ignored);
      socket.close(ignored);
      self->ioc_.stop();
      self->notify_closed("closed");
    });
  }

private:
  WebSocketVoiceBackend& owner_;
  uint64_t generation_{};
  ParsedURL url_;
  std::string token_;
  std::string device_id_;
  net::io_context ioc_;
  tcp::resolver resolver_;
  websocket::stream<beast::tcp_stream> websocket_;
  beast::flat_buffer read_buffer_;
  std::deque<Outgoing> writes_;
  std::atomic<bool> open_{false};
  std::atomic<bool> notify_on_close_{true};
  bool write_active_{};
  bool notified_{};

  void enqueue(bool text, std::string bytes) {
    auto data = std::make_shared<std::string>(std::move(bytes));
    net::post(ioc_, [self = shared_from_this(), text, data] {
      if (!self->open_.load()) return;
      self->writes_.push_back(Outgoing{text, data});
      if (!self->write_active_) self->write_next();
    });
  }

  void write_next() {
    if (writes_.empty() || !open_.load()) {
      write_active_ = false;
      return;
    }
    write_active_ = true;
    websocket_.text(writes_.front().text);
    websocket_.async_write(
        net::buffer(*writes_.front().bytes),
        [self = shared_from_this()](beast::error_code error, std::size_t) {
          if (error) return self->fail("write: " + error.message());
          self->writes_.pop_front();
          self->write_next();
        });
  }

  void read_next() {
    websocket_.async_read(
        read_buffer_, [self = shared_from_this()](beast::error_code error, std::size_t) {
          if (error) return self->fail("read: " + error.message());
          if (self->websocket_.got_text()) {
            self->owner_.handle_text(
                self->generation_, beast::buffers_to_string(self->read_buffer_.data()));
          } else {
            const auto buffers = self->read_buffer_.data();
            std::vector<uint8_t> packet(boost::asio::buffer_size(buffers));
            boost::asio::buffer_copy(boost::asio::buffer(packet), buffers);
            self->owner_.handle_binary(self->generation_, packet);
          }
          self->read_buffer_.consume(self->read_buffer_.size());
          self->read_next();
        });
  }

  void fail(const std::string& reason) {
    open_.store(false);
    notify_closed(reason);
  }

  void notify_closed(const std::string& reason) {
    if (notified_) return;
    notified_ = true;
    owner_.handle_connection_closed(generation_, reason, notify_on_close_.load());
  }
};

WebSocketVoiceBackend::WebSocketVoiceBackend(std::string url, std::string token,
                                             std::string device_id)
    : url_(std::move(url)), token_(std::move(token)), device_id_(std::move(device_id)) {
  int error = OPUS_OK;
  encoder_ = opus_encoder_create(16'000, 1, OPUS_APPLICATION_VOIP, &error);
  if (error != OPUS_OK || encoder_ == nullptr) throw std::runtime_error("create Opus encoder");
  decoder_ = opus_decoder_create(24'000, 1, &error);
  if (error != OPUS_OK || decoder_ == nullptr) {
    opus_encoder_destroy(encoder_);
    encoder_ = nullptr;
    throw std::runtime_error("create Opus decoder");
  }
}

WebSocketVoiceBackend::~WebSocketVoiceBackend() {
  stopping_.store(true);
  stop_connection(false);
  if (media_thread_.joinable()) media_thread_.join();
  if (decoder_ != nullptr) opus_decoder_destroy(decoder_);
  if (encoder_ != nullptr) opus_encoder_destroy(encoder_);
}

bool WebSocketVoiceBackend::start(uint64_t) {
  const auto parsed = parse_ws_url(url_);
  if (!parsed) return false;
  stop_connection(false);
  if (media_thread_.joinable()) media_thread_.join();
  {
    std::lock_guard lock(state_mutex_);
    session_id_.clear();
    active_turn_id_.clear();
    last_begin_wire_.clear();
    turn_active_ = false;
    tts_active_ = false;
    pending_settings_ = {};
    pending_settings_correlation_.clear();
    settings_pending_ = false;
    upload_samples_.clear();
    playback_samples_.clear();
    clear_voice_mail_locked();
  }
  protocol_connected_.store(false);
  const uint64_t generation = connection_generation_.fetch_add(1) + 1;
  auto connection = std::make_shared<Connection>(*this, generation, *parsed, token_, device_id_);
  connection_ = connection;
  io_thread_ = std::thread([connection] { connection->run(); });
  return true;
}

void WebSocketVoiceBackend::tick(uint64_t) {
  finish_media_worker();
}

bool WebSocketVoiceBackend::begin_turn(uint64_t, ListenMode mode) {
  if (!protocol_connected_.load()) return false;
  const std::string turn_id =
      "host-turn-" + std::to_string(turn_sequence_.fetch_add(1) + 1);
  {
    std::lock_guard lock(state_mutex_);
    if (turn_active_) return false;
    active_turn_id_ = turn_id;
    turn_active_ = true;
    tts_active_ = false;
    media_generation_.fetch_add(1);
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

bool WebSocketVoiceBackend::send_audio(std::span<const int16_t> pcm) {
  if (pcm.empty()) return false;
  std::vector<std::array<int16_t, kUplinkFrameSamples>> ready;
  {
    std::lock_guard lock(state_mutex_);
    if (!turn_active_) return false;
    upload_samples_.insert(upload_samples_.end(), pcm.begin(), pcm.end());
    while (upload_samples_.size() >= kUplinkFrameSamples) {
      std::array<int16_t, kUplinkFrameSamples> frame{};
      std::copy_n(upload_samples_.begin(), kUplinkFrameSamples, frame.begin());
      upload_samples_.erase(upload_samples_.begin(), upload_samples_.begin() + kUplinkFrameSamples);
      ready.push_back(frame);
    }
  }
  for (const auto& frame : ready) {
    if (!flush_upload_frame(frame)) return false;
  }
  return true;
}

bool WebSocketVoiceBackend::finish_turn(uint64_t) {
  std::string turn_id;
  std::array<int16_t, kUplinkFrameSamples> final{};
  bool has_final = false;
  {
    std::lock_guard lock(state_mutex_);
    if (!turn_active_) return false;
    turn_id = active_turn_id_;
    if (!upload_samples_.empty()) {
      std::copy_n(upload_samples_.begin(),
                  std::min(upload_samples_.size(), final.size()),
                  final.begin());
      upload_samples_.clear();
      has_final = true;
    }
  }
  if (has_final && !flush_upload_frame(final)) return false;
  json payload{{"state", "stop"}};
  return send_text(encode_control(static_cast<int>(protocol::ControlType::turn_listen),
                                  payload.dump(), turn_id));
}

void WebSocketVoiceBackend::cancel_turn() {
  std::string turn_id;
  {
    std::lock_guard lock(state_mutex_);
    turn_id = active_turn_id_;
    turn_active_ = false;
    tts_active_ = false;
    media_generation_.fetch_add(1);
    clear_turn_media_locked();
    ++stats_.cancels;
  }
  if (!turn_id.empty() && protocol_connected_.load()) {
    json payload{{"reason", "button_barge_in"}};
    send_text(encode_control(static_cast<int>(protocol::ControlType::turn_abort),
                             payload.dump(), turn_id));
  }
}

bool WebSocketVoiceBackend::poll_event(BackendEvent& event) {
  std::lock_guard lock(state_mutex_);
  while (!events_.empty()) {
    event = events_.front();
    events_.pop_front();
    if (event.scope == BackendEventScope::generation) {
      if (event.session_epoch != connection_generation_.load() ||
          event.generation != media_generation_.load()) {
        continue;
      }
    } else if (event.scope == BackendEventScope::session) {
      if (event.type != BackendEventType::disconnected &&
          event.session_epoch != connection_generation_.load()) {
        continue;
      }
    }
    return true;
  }
  return false;
}

bool WebSocketVoiceBackend::report_settings_apply(const SettingsTwin& twin,
                                                  bool applied) {
  std::string correlation;
  bool matched = false;
  {
    std::lock_guard lock(state_mutex_);
    if (settings_pending_ && pending_settings_.version == twin.version &&
        same_settings(pending_settings_.settings, twin.settings)) {
      matched = true;
      correlation = pending_settings_correlation_;
      if (applied) {
        current_settings_ = twin.settings;
        settings_version_ = twin.version;
        ++stats_.settings_applies;
      }
      pending_settings_ = {};
      pending_settings_correlation_.clear();
      settings_pending_ = false;
    }
  }
  if (!matched || correlation.empty() || !protocol_connected_.load()) return false;
  json result;
  if (applied) {
    result = {{"ok", true}, {"value", {{"applied", true}, {"version", twin.version}}}};
  } else {
    result = {{"ok", true},
              {"value", {{"applied", false},
                         {"version", twin.version},
                         {"error", "apply_failed"}}}};
  }
  const bool sent = send_text(encode_control(
      static_cast<int>(protocol::ControlType::capability_result), result.dump(), {},
      correlation, true, std::nullopt));
  if (sent) {
    std::lock_guard lock(state_mutex_);
    ++stats_.capability_results;
  }
  return sent;
}

bool WebSocketVoiceBackend::claim_voice_mail(const VoiceMailMetadata& item, uint64_t) {
  finish_media_worker();
  if (!protocol_connected_.load() || !item.valid() || media_worker_running_.load()) {
    return false;
  }
  const std::string candidate_playback_id = random_opaque_id("host-playback-");
  const std::string candidate_claim_key = random_opaque_id("host-voice-mail-claim-");
  if (candidate_playback_id.empty() || candidate_claim_key.empty()) return false;
  std::string wire;
  std::string playback_id;
  std::string idempotency_key;
  {
    std::lock_guard lock(state_mutex_);
    if (voice_mail_claim_active_) {
      if (active_voice_mail_.voice_mail_id_view() != item.voice_mail_id_view()) return false;
      wire = voice_mail_claim_wire_;
    } else {
      active_voice_mail_ = item;
      voice_mail_playback_id_ = candidate_playback_id;
      voice_mail_claim_idempotency_key_ = candidate_claim_key;
      voice_mail_claim_active_ = true;
      voice_mail_result_sent_ = false;
      voice_mail_result_wire_.clear();
      voice_mail_samples_.clear();
      voice_mail_sample_offset_ = 0;
    }
    playback_id = voice_mail_playback_id_;
    idempotency_key = voice_mail_claim_idempotency_key_;
  }
  if (wire.empty()) {
    json payload{{"voice_mail_id", std::string(item.voice_mail_id_view())},
                 {"playback_id", playback_id}};
    wire = encode_control(static_cast<int>(protocol::ControlType::voice_mail_claim),
                          payload.dump(), {}, {}, true, std::nullopt,
                          idempotency_key, rfc3339_now());
    if (wire.empty()) {
      std::lock_guard lock(state_mutex_);
      clear_voice_mail_locked();
      return false;
    }
    std::lock_guard lock(state_mutex_);
    voice_mail_claim_wire_ = wire;
  }
  return send_text(std::move(wire));
}

bool WebSocketVoiceBackend::report_voice_mail_playback(
    const VoiceMailMetadata& item, bool succeeded, std::string_view failure_code,
    uint64_t) {
  if (!protocol_connected_.load() || !item.valid() || failure_code.size() > 64 ||
      (succeeded && !failure_code.empty())) {
    return false;
  }
  std::string wire;
  std::string playback_id;
  {
    std::lock_guard lock(state_mutex_);
    if (!voice_mail_claim_active_ ||
        active_voice_mail_.voice_mail_id_view() != item.voice_mail_id_view() ||
        voice_mail_result_sent_) {
      return false;
    }
    playback_id = voice_mail_playback_id_;
    wire = voice_mail_result_wire_;
  }
  if (wire.empty()) {
    json payload{{"voice_mail_id", std::string(item.voice_mail_id_view())},
                 {"playback_id", playback_id},
                 {"result", succeeded ? "succeeded" : "failed"}};
    if (!succeeded && !failure_code.empty()) payload["failure_code"] = failure_code;
    const std::string idempotency = random_opaque_id("host-voice-mail-result-");
    if (idempotency.empty()) return false;
    wire = encode_control(
        static_cast<int>(protocol::ControlType::voice_mail_playback_result),
        payload.dump(), {}, {}, true, std::nullopt, idempotency, rfc3339_now());
    if (wire.empty()) return false;
    std::lock_guard lock(state_mutex_);
    voice_mail_result_wire_ = wire;
  }
  if (!send_text(std::move(wire))) return false;
  {
    std::lock_guard lock(state_mutex_);
    voice_mail_result_sent_ = true;
    if (!succeeded) clear_voice_mail_locked();
  }
  return true;
}

void WebSocketVoiceBackend::cancel_voice_mail(const VoiceMailMetadata& item,
                                              std::string_view failure_code,
                                              uint64_t now_ms) {
  if (report_voice_mail_playback(item, false, failure_code, now_ms)) return;
  std::lock_guard lock(state_mutex_);
  if (active_voice_mail_.voice_mail_id_view() == item.voice_mail_id_view()) {
    clear_voice_mail_locked();
  }
}

size_t WebSocketVoiceBackend::read_playback(std::span<int16_t> destination) {
  std::lock_guard lock(state_mutex_);
  if (voice_mail_sample_offset_ < voice_mail_samples_.size()) {
    const size_t count = std::min(destination.size(),
                                  voice_mail_samples_.size() - voice_mail_sample_offset_);
    std::copy_n(voice_mail_samples_.begin() + voice_mail_sample_offset_, count,
                destination.begin());
    voice_mail_sample_offset_ += count;
    if (voice_mail_sample_offset_ == voice_mail_samples_.size()) {
      voice_mail_samples_.clear();
      voice_mail_sample_offset_ = 0;
    }
    return count;
  }
  const size_t count = std::min(destination.size(), playback_samples_.size());
  for (size_t i = 0; i < count; ++i) {
    destination[i] = playback_samples_.front();
    playback_samples_.pop_front();
  }
  return count;
}

bool WebSocketVoiceBackend::playback_empty() const {
  std::lock_guard lock(state_mutex_);
  return playback_samples_.empty() &&
         voice_mail_sample_offset_ == voice_mail_samples_.size();
}

uint32_t WebSocketVoiceBackend::playback_sample_rate_hz() const {
  std::lock_guard lock(state_mutex_);
  return playback_sample_rate_;
}

bool WebSocketVoiceBackend::resend_last_begin_for_test() {
  std::string wire;
  {
    std::lock_guard lock(state_mutex_);
    wire = last_begin_wire_;
  }
  return !wire.empty() && send_text(std::move(wire));
}

void WebSocketVoiceBackend::disconnect_for_test() {
  auto connection = connection_;
  if (connection) connection->stop(true);
}

WebSocketVoiceBackend::Stats WebSocketVoiceBackend::stats() const {
  std::lock_guard lock(state_mutex_);
  return stats_;
}

std::string WebSocketVoiceBackend::session_id() const {
  std::lock_guard lock(state_mutex_);
  return session_id_;
}

void WebSocketVoiceBackend::stop_connection(bool notify) {
  auto connection = connection_;
  if (connection) connection->stop(notify);
  if (io_thread_.joinable()) io_thread_.join();
  connection_.reset();
}

bool WebSocketVoiceBackend::send_text(std::string message) {
  auto connection = connection_;
  if (!connection || !connection->open()) return false;
  connection->send_text(std::move(message));
  return true;
}

bool WebSocketVoiceBackend::send_binary(std::vector<uint8_t> packet) {
  auto connection = connection_;
  if (!connection || !connection->open()) return false;
  connection->send_binary(std::move(packet));
  return true;
}

std::string WebSocketVoiceBackend::encode_control(int type_index, std::string payload_json,
                                                  std::string turn_id,
                                                  std::string correlation_id,
                                                  bool include_session,
                                                  std::optional<uint64_t> generation_id,
                                                  std::string idempotency_key,
                                                  std::string occurred_at) {
  const auto type = static_cast<protocol::ControlType>(type_index);
  const std::string message_id =
      "host-message-" + std::to_string(message_sequence_.fetch_add(1) + 1);
  std::string session;
  if (include_session) {
    std::lock_guard lock(state_mutex_);
    session = session_id_;
  }
  std::array<char, 8192> buffer{};
  size_t written = 0;
  protocol::Envelope envelope{
      .type = type,
      .message_id = message_id,
      .payload_json = payload_json,
      .correlation_id = correlation_id,
      .session_id = session,
      .turn_id = turn_id,
      .generation_id = generation_id.value_or(0),
      .has_generation_id = generation_id.has_value(),
      .idempotency_key = idempotency_key,
      .occurred_at = occurred_at,
  };
  if (!protocol::encode(envelope, buffer, written)) return {};
  return {buffer.data(), written};
}

void WebSocketVoiceBackend::enqueue_event(BackendEventType type, std::string_view text) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = type;
  event.scope = scope_for_event_type(type);
  event.session_epoch = connection_generation_.load();
  event.generation = media_generation_.load();
  event.set_text(text);
  events_.push_back(event);
}

void WebSocketVoiceBackend::enqueue_card_event(const PresentationCardV1& card) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = BackendEventType::presentation_card;
  event.scope = BackendEventScope::generation;
  event.session_epoch = connection_generation_.load();
  event.generation = media_generation_.load();
  event.set_card(card);
  events_.push_back(event);
}

void WebSocketVoiceBackend::enqueue_hint_event(const PresentationHint& hint) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = BackendEventType::presentation_hint;
  event.scope = BackendEventScope::generation;
  event.session_epoch = connection_generation_.load();
  event.generation = media_generation_.load();
  event.set_hint(hint);
  events_.push_back(event);
}

void WebSocketVoiceBackend::enqueue_agent_status_event(
    const AgentPresentationStatus& status) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = BackendEventType::agent_status;
  event.scope = BackendEventScope::generation;
  event.session_epoch = connection_generation_.load();
  event.generation = media_generation_.load();
  event.set_agent_status(status);
  events_.push_back(event);
}

void WebSocketVoiceBackend::enqueue_voice_mail_event(BackendEventType type,
                                                     const VoiceMailMetadata& item,
                                                     std::string_view text) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = type;
  event.scope = scope_for_event_type(type);
  event.session_epoch = connection_generation_.load();
  event.generation = media_generation_.load();
  event.set_voice_mail(item);
  event.set_text(text);
  events_.push_back(event);
}

void WebSocketVoiceBackend::handle_connection_open(uint64_t generation) {
  if (generation != connection_generation_.load()) return;
  json payload{{"transport", "websocket"},
               {"audio_params", {{"format", "opus"}, {"sample_rate", 16'000},
                                  {"channels", 1}, {"frame_duration", 60}}}};
  const std::string hello = encode_control(
      static_cast<int>(protocol::ControlType::session_hello), payload.dump(), {}, {}, false);
  if (hello.empty() || !send_text(hello)) enqueue_event(BackendEventType::error, "HELLO SEND FAILED");
}

void WebSocketVoiceBackend::handle_connection_closed(uint64_t generation,
                                                     std::string_view reason,
                                                     bool notify) {
  if (generation != connection_generation_.load()) return;
  protocol_connected_.store(false);
  {
    std::lock_guard lock(state_mutex_);
    turn_active_ = false;
    tts_active_ = false;
    pending_settings_ = {};
    pending_settings_correlation_.clear();
    settings_pending_ = false;
    media_generation_.fetch_add(1);
    clear_turn_media_locked();
    clear_voice_mail_locked();
  }
  if (notify && !stopping_.load()) enqueue_event(BackendEventType::disconnected, reason);
}

void WebSocketVoiceBackend::handle_text(uint64_t generation, const std::string& text) {
  if (generation != connection_generation_.load()) return;
  try {
    const json envelope = json::parse(text);
    if (!envelope.is_object() || envelope.value("version", 0) != 2) {
      enqueue_event(BackendEventType::error, "INVALID VERSION");
      return;
    }
    const std::string type_name = json_string(envelope, "type");
    const std::string incoming_session = json_string(envelope, "session_id");
    const std::string incoming_turn = json_string(envelope, "turn_id");
    const std::string correlation_id = json_string(envelope, "correlation_id");
    const bool has_generation = envelope.contains("generation_id") &&
                                envelope.at("generation_id").is_number_unsigned();
    const uint64_t incoming_generation = has_generation
                                             ? envelope.at("generation_id").get<uint64_t>()
                                             : 0;
    const json payload = envelope.value("payload", json::object());
    protocol::ControlType type{};
    if (!protocol::parse_type(type_name, type)) {
      enqueue_event(BackendEventType::error, "UNKNOWN TYPE");
      return;
    }
    const bool presentation_control = type == protocol::ControlType::ui_card ||
                                      type == protocol::ControlType::ui_state ||
                                      type == protocol::ControlType::agent_status;
    if (presentation_control &&
        presentation_ingress::contains_unsupported_json_nul(text)) {
      enqueue_event(BackendEventType::error, "INVALID PRESENTATION CONTROL");
      return;
    }
    const bool voice_mail_message = type == protocol::ControlType::voice_mail_available ||
                                    type == protocol::ControlType::voice_mail_claimed ||
                                    type == protocol::ControlType::voice_mail_consumed ||
                                    type == protocol::ControlType::voice_mail_expired;
    const std::string interaction_key = json_string(envelope, "idempotency_key");
    if (voice_mail_message &&
        (interaction_key.empty() || interaction_key.size() > 128 ||
         json_string(envelope, "occurred_at").empty())) {
      enqueue_event(BackendEventType::error, "INVALID VOICE MAIL ENVELOPE");
      return;
    }
    if (type == protocol::ControlType::session_ready) {
      const auto audio = payload.at("audio_params");
      if (!has_only_fields(payload, {"transport", "audio_params", "features"}) ||
          incoming_session.empty() || payload.at("transport") != "websocket" ||
          audio.at("format") != "opus" || audio.at("sample_rate") != 24'000 ||
          audio.at("channels") != 1 || audio.at("frame_duration") != 60) {
        enqueue_event(BackendEventType::error, "INVALID READY");
        return;
      }
      {
        std::lock_guard lock(state_mutex_);
        session_id_ = incoming_session;
        playback_sample_rate_ = 24'000;
        ++stats_.connections;
      }
      protocol_connected_.store(true);
      json advertise{{"capabilities", json::array({
          {{"name", "device.volume.set"}, {"version", "1"}, {"kind", "command"}},
          {{"name", "device.settings_v1"}, {"version", "1"}, {"kind", "command"}}
      })}};
      if (send_text(encode_control(static_cast<int>(protocol::ControlType::capability_advertise),
                                   advertise.dump()))) {
        std::lock_guard lock(state_mutex_);
        ++stats_.capability_advertisements;
      } else {
        enqueue_event(BackendEventType::error, "CAPABILITY ADVERTISE FAILED");
      }
      enqueue_event(BackendEventType::connected);
      return;
    }
    std::string expected_session;
    {
      std::lock_guard lock(state_mutex_);
      expected_session = session_id_;
    }
    if (!protocol_connected_.load() || incoming_session != expected_session) {
      enqueue_event(BackendEventType::error, "SESSION MISMATCH");
      return;
    }

    auto current_turn = [&] {
      std::lock_guard lock(state_mutex_);
      return active_turn_id_;
    }();
    const bool turn_scoped = type == protocol::ControlType::turn_state ||
                             type == protocol::ControlType::transcript_final ||
                             type == protocol::ControlType::tts_lifecycle ||
                             type == protocol::ControlType::agent_status ||
                             type == protocol::ControlType::ui_card ||
                             type == protocol::ControlType::ui_state;
    if (turn_scoped && incoming_turn != current_turn) {
      std::lock_guard lock(state_mutex_);
      ++stats_.stale_controls;
      return;
    }

    switch (type) {
    case protocol::ControlType::transcript_final:
      enqueue_event(BackendEventType::transcript, json_string(payload, "text"));
      break;
    case protocol::ControlType::tts_lifecycle: {
      const std::string state = json_string(payload, "state");
      if (state == "start") {
        {
          std::lock_guard lock(state_mutex_);
          tts_active_ = true;
        }
        enqueue_event(BackendEventType::tts_started);
      } else if (state == "sentence_start") {
        enqueue_event(BackendEventType::tts_sentence, json_string(payload, "text"));
      } else if (state == "stop") {
        {
          std::lock_guard lock(state_mutex_);
          tts_active_ = false;
          turn_active_ = false;
        }
        enqueue_event(BackendEventType::tts_finished);
      }
      break;
    }
    case protocol::ControlType::turn_state:
      if (json_string(payload, "state") == "interrupted") {
        std::lock_guard lock(state_mutex_);
        if (incoming_turn == active_turn_id_) {
          tts_active_ = false;
          turn_active_ = false;
          clear_turn_media_locked();
        }
      }
      break;
    case protocol::ControlType::alarm_fired:
      enqueue_event(BackendEventType::alarm, json_string(payload, "message"));
      break;
    case protocol::ControlType::schedule_updated:
      enqueue_event(BackendEventType::schedule, json_string(payload, "message"));
      break;
    case protocol::ControlType::ui_card: {
      PresentationCardV1 card{};
      if (!parse_presentation_card(payload, card)) {
        enqueue_event(BackendEventType::error, "INVALID UI CARD");
        break;
      }
      enqueue_card_event(card);
      break;
    }
    case protocol::ControlType::ui_state: {
      PresentationHint hint{};
      if (!parse_presentation_hint(payload, hint)) {
        enqueue_event(BackendEventType::error, "INVALID UI STATE");
        break;
      }
      enqueue_hint_event(hint);
      break;
    }
    case protocol::ControlType::agent_status: {
      AgentPresentationStatus status{};
      if (!parse_agent_presentation_status(payload, status)) {
        enqueue_event(BackendEventType::error, "INVALID AGENT STATUS");
        break;
      }
      enqueue_agent_status_event(status);
      break;
    }
    case protocol::ControlType::voice_mail_available: {
      VoiceMailMetadata item{};
      if (!parse_voice_mail_metadata(payload, item)) {
        enqueue_event(BackendEventType::error, "INVALID VOICE MAIL");
        break;
      }
      const uint64_t now_ms = static_cast<uint64_t>(
          std::chrono::duration_cast<std::chrono::milliseconds>(
              std::chrono::system_clock::now().time_since_epoch()).count());
      {
        std::lock_guard lock(state_mutex_);
        if (voice_mail_result_sent_ &&
            active_voice_mail_.voice_mail_id_view() == item.voice_mail_id_view()) {
          clear_voice_mail_locked();
        }
      }
      enqueue_voice_mail_event(item.expires_at_unix_ms <= now_ms
                                   ? BackendEventType::voice_mail_expired
                                   : BackendEventType::voice_mail_available,
                               item);
      break;
    }
    case protocol::ControlType::voice_mail_claimed: {
      const std::string voice_mail_id = json_string(payload, "voice_mail_id");
      const std::string playback_id = json_string(payload, "playback_id");
      const std::string media_ref = json_string(payload, "media_ref");
      const auto lease_expires =
          parse_rfc3339_unix_ms(json_string(payload, "lease_expires_at"));
      const uint64_t now_ms = static_cast<uint64_t>(
          std::chrono::duration_cast<std::chrono::milliseconds>(
              std::chrono::system_clock::now().time_since_epoch()).count());
      VoiceMailMetadata item{};
      bool accepted = false;
      bool duplicate = false;
      {
        std::lock_guard lock(state_mutex_);
        const bool matched = voice_mail_claim_active_ &&
                             active_voice_mail_.voice_mail_id_view() == voice_mail_id &&
                             voice_mail_playback_id_ == playback_id;
        duplicate = matched && voice_mail_media_started_;
        accepted = matched && !voice_mail_media_started_ && safe_media_ref(media_ref) &&
                   lease_expires.has_value() && *lease_expires > now_ms;
        if (accepted) {
          voice_mail_media_started_ = true;
          item = active_voice_mail_;
        }
      }
      if (duplicate) break;
      if (!accepted) {
        enqueue_event(BackendEventType::error, "INVALID VOICE MAIL CLAIM");
        break;
      }
      start_voice_mail_fetch(media_ref, generation, item, playback_id);
      break;
    }
    case protocol::ControlType::voice_mail_consumed: {
      const std::string voice_mail_id = json_string(payload, "voice_mail_id");
      const std::string playback_id = json_string(payload, "playback_id");
      if (!valid_opaque_id(voice_mail_id, 128) ||
          (!playback_id.empty() && !valid_opaque_id(playback_id, 128))) {
        enqueue_event(BackendEventType::error, "INVALID VOICE MAIL CONSUMED");
        break;
      }
      VoiceMailMetadata item{};
      bool matched = false;
      {
        std::lock_guard lock(state_mutex_);
        matched = voice_mail_claim_active_ &&
                  active_voice_mail_.voice_mail_id_view() == voice_mail_id &&
                  (playback_id.empty() || voice_mail_playback_id_ == playback_id);
        if (matched) item = active_voice_mail_;
      }
      if (!matched) break;
      enqueue_voice_mail_event(BackendEventType::voice_mail_consumed, item);
      {
        std::lock_guard lock(state_mutex_);
        clear_voice_mail_locked();
      }
      break;
    }
    case protocol::ControlType::voice_mail_expired: {
      const std::string voice_mail_id = json_string(payload, "voice_mail_id");
      if (!valid_opaque_id(voice_mail_id, 128)) {
        enqueue_event(BackendEventType::error, "INVALID VOICE MAIL EXPIRY");
        break;
      }
      VoiceMailMetadata item{};
      item.set_voice_mail_id(voice_mail_id);
      {
        std::lock_guard lock(state_mutex_);
        if (active_voice_mail_.voice_mail_id_view() == voice_mail_id) {
          item = active_voice_mail_;
          clear_voice_mail_locked();
        }
      }
      enqueue_voice_mail_event(BackendEventType::voice_mail_expired, item);
      break;
    }
    case protocol::ControlType::capability_call: {
      {
        std::lock_guard lock(state_mutex_);
        ++stats_.capability_calls;
      }
      json result;
      bool defer_result = false;
      const std::string name = json_string(payload, "name");
      const std::string version = json_string(payload, "version");
      const auto arguments = payload.find("arguments");
      const int deadline_ms = payload.value("deadline_ms", 0);
      if (correlation_id.empty() || deadline_ms < 50 || deadline_ms > 5000) {
        result = {{"ok", false}, {"error", "invalid_argument"}};
      } else if (name == "device.settings_v1" && version == "1") {
        if (has_generation || !incoming_turn.empty() || arguments == payload.end() ||
            !arguments->is_object()) {
          result = {{"ok", false}, {"error", "invalid_argument"}};
        } else {
          DeviceSettings current{};
          uint64_t current_version = 0;
          bool busy = false;
          {
            std::lock_guard lock(state_mutex_);
            current = current_settings_;
            current_version = settings_version_;
            busy = settings_pending_;
          }
          if (busy) {
            result = {{"ok", false}, {"error", "busy"}};
          } else {
            SettingsTwin candidate{};
            bool duplicate = false;
            if (!parse_settings_arguments(*arguments, current, current_version,
                                          candidate, duplicate)) {
              uint64_t attempted_version = 0;
              const auto version_it = arguments->find("version");
              if (version_it != arguments->end()) {
                (void)exact_uint64(*version_it, attempted_version);
              }
              result = {{"ok", true},
                        {"value", {{"applied", false},
                                   {"version", attempted_version},
                                   {"error", "invalid_argument"}}}};
            } else if (duplicate) {
              result = {{"ok", true},
                        {"value", {{"applied", true}, {"version", candidate.version}}}};
            } else {
              bool queued = false;
              {
                std::lock_guard lock(state_mutex_);
                if (!settings_pending_) {
                  if (events_.size() == kMaximumEvents) events_.pop_front();
                  BackendEvent event{};
                  event.type = BackendEventType::settings;
                  event.scope = BackendEventScope::session;
                  event.session_epoch = connection_generation_.load();
                  event.generation = 0;
                  event.set_settings(candidate);
                  events_.push_back(event);
                  pending_settings_ = candidate;
                  pending_settings_correlation_ = correlation_id;
                  settings_pending_ = true;
                  queued = true;
                }
              }
              if (queued) {
                defer_result = true;
              } else {
                result = {{"ok", false}, {"error", "busy"}};
              }
            }
          }
        }
      } else if (name == "device.volume.set" && version == "1") {
        if (!has_generation || arguments == payload.end() || !arguments->is_object() ||
            !arguments->contains("volume") || !arguments->at("volume").is_number_integer()) {
          result = {{"ok", false}, {"error", "invalid_argument"}};
        } else {
          const int volume = arguments->at("volume").get<int>();
          if (volume < 0 || volume > 100) {
            result = {{"ok", false}, {"error", "invalid_argument"}};
          } else {
            {
              std::lock_guard lock(state_mutex_);
              stats_.capability_volume = volume;
            }
            result = {{"ok", true}, {"value", {{"volume", volume}, {"applied", true}}}};
          }
        }
      } else {
        result = {{"ok", false}, {"error", "unsupported"}};
      }
      if (defer_result) break;
      const std::optional<uint64_t> result_generation =
          has_generation ? std::optional<uint64_t>(incoming_generation) : std::nullopt;
      const bool sent = send_text(encode_control(
          static_cast<int>(protocol::ControlType::capability_result), result.dump(),
          incoming_turn, correlation_id, true, result_generation));
      if (sent) {
        std::lock_guard lock(state_mutex_);
        ++stats_.capability_results;
      } else {
        enqueue_event(BackendEventType::error, "CAPABILITY RESULT FAILED");
      }
      break;
    }
    case protocol::ControlType::capability_cancel:
      {
        std::lock_guard lock(state_mutex_);
        ++stats_.capability_cancels;
      }
      break;
    case protocol::ControlType::session_ping: {
      json empty = json::object();
      send_text(encode_control(static_cast<int>(protocol::ControlType::session_pong),
                               empty.dump(), {}, json_string(envelope, "message_id")));
      break;
    }
    case protocol::ControlType::protocol_error:
      {
        VoiceMailMetadata item{};
        bool voice_mail_active = false;
        {
          std::lock_guard lock(state_mutex_);
          voice_mail_active = voice_mail_claim_active_;
          if (voice_mail_active) item = active_voice_mail_;
        }
        if (voice_mail_active) {
          enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                                   json_string(payload, "code"));
        } else {
          enqueue_event(BackendEventType::error, json_string(payload, "code"));
        }
      }
      break;
    default:
      break;
    }
  } catch (const std::exception& error) {
    enqueue_event(BackendEventType::error, std::string("CONTROL PARSE: ") + error.what());
  }
}

void WebSocketVoiceBackend::handle_binary(uint64_t generation,
                                          std::span<const uint8_t> packet) {
  if (generation != connection_generation_.load() || packet.empty() ||
      packet.size() > kMaximumOpusPacketBytes) return;
  {
    std::lock_guard lock(state_mutex_);
    if (!tts_active_) {
      ++stats_.discarded_binary_packets;
      return;
    }
  }
  std::array<int16_t, kDownlinkFrameSamples> decoded{};
  int count = 0;
  {
    std::lock_guard lock(codec_mutex_);
    count = opus_decode(decoder_, packet.data(), static_cast<opus_int32>(packet.size()),
                        decoded.data(), static_cast<int>(decoded.size()), 0);
  }
  if (count <= 0) {
    enqueue_event(BackendEventType::error, "OPUS DECODE FAILED");
    return;
  }
  std::lock_guard lock(state_mutex_);
  if (!tts_active_) {
    ++stats_.discarded_binary_packets;
    return;
  }
  if (playback_samples_.size() + static_cast<size_t>(count) > kMaximumPlaybackSamples) {
    events_.clear();
    BackendEvent event{};
    event.type = BackendEventType::error;
    event.set_text("PLAYBACK QUEUE FULL");
    events_.push_back(event);
    playback_samples_.clear();
    return;
  }
  playback_samples_.insert(playback_samples_.end(), decoded.begin(), decoded.begin() + count);
}

bool WebSocketVoiceBackend::flush_upload_frame(std::span<const int16_t> samples) {
  if (samples.size() != kUplinkFrameSamples) return false;
  std::array<uint8_t, kMaximumOpusPacketBytes> packet{};
  int encoded = 0;
  {
    std::lock_guard lock(codec_mutex_);
    encoded = opus_encode(encoder_, samples.data(), static_cast<int>(samples.size()),
                          packet.data(), static_cast<opus_int32>(packet.size()));
  }
  if (encoded <= 0) return false;
  return send_binary(std::vector<uint8_t>(packet.begin(), packet.begin() + encoded));
}

void WebSocketVoiceBackend::clear_turn_media_locked() {
  upload_samples_.clear();
  playback_samples_.clear();
}

void WebSocketVoiceBackend::start_voice_mail_fetch(std::string media_ref,
                                                   uint64_t generation,
                                                   VoiceMailMetadata item,
                                                   std::string playback_id) {
  if (media_thread_.joinable()) media_thread_.join();
  media_worker_running_.store(true);
  media_thread_ = std::thread(
      [this, media_ref = std::move(media_ref), generation, item,
       playback_id = std::move(playback_id)] {
        struct RunningGuard {
          std::atomic<bool>& running;
          ~RunningGuard() { running.store(false); }
        } guard{media_worker_running_};

        auto fail = [this, &item](std::string_view message) {
          if (stopping_.load()) return;
          bool current = false;
          {
            std::lock_guard lock(state_mutex_);
            current = voice_mail_claim_active_ &&
                      active_voice_mail_.voice_mail_id_view() == item.voice_mail_id_view();
            voice_mail_samples_.clear();
            voice_mail_sample_offset_ = 0;
          }
          if (current) enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item, message);
        };

        try {
          const auto parsed = parse_ws_url(url_);
          if (!parsed) throw std::runtime_error("invalid media origin");
          net::io_context ioc;
          tcp::resolver resolver(ioc);
          beast::tcp_stream stream(ioc);
          stream.expires_after(std::chrono::seconds(5));
          const auto endpoints = resolver.resolve(parsed->host, parsed->port);
          stream.connect(endpoints);
#if !defined(_WIN32)
          const timeval socket_timeout{.tv_sec = 5, .tv_usec = 0};
          const int descriptor = stream.socket().native_handle();
          if (::setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &socket_timeout,
                           sizeof(socket_timeout)) != 0 ||
              ::setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &socket_timeout,
                           sizeof(socket_timeout)) != 0) {
            throw std::runtime_error("media socket timeout setup failed");
          }
#endif

          const std::string media_target =
              media_ref + "?playback_id=" + playback_id;
          http::request<http::empty_body> request{http::verb::get, media_target, 11};
          request.set(http::field::host, parsed->host + ":" + parsed->port);
          request.set(http::field::authorization, "Bearer " + token_);
          request.set(http::field::user_agent, "companion-software-device");
          request.set("Device-Id", device_id_);
          http::write(stream, request);

          beast::flat_buffer buffer;
          http::response_parser<http::vector_body<uint8_t>> parser;
          parser.body_limit(kMaximumVoiceMailBytes);
          http::read(stream, buffer, parser);
          auto response = parser.release();
          beast::error_code ignored;
          stream.socket().shutdown(tcp::socket::shutdown_both, ignored);
          if (response.result() != http::status::ok) {
            throw std::runtime_error("media request rejected");
          }
          const auto& bytes = response.body();
          if (bytes.size() != item.size_bytes) throw std::runtime_error("media size mismatch");
          std::string expected(item.checksum_sha256.data());
          std::transform(expected.begin(), expected.end(), expected.begin(),
                         [](unsigned char value) {
                           return static_cast<char>(std::tolower(value));
                         });
          if (sha256_hex(bytes) != expected) throw std::runtime_error("media checksum mismatch");

          int opus_error = 0;
          OggOpusFile* file = op_open_memory(bytes.data(), bytes.size(), &opus_error);
          if (file == nullptr) throw std::runtime_error("invalid ogg opus media");
          struct OpusFileGuard {
            OggOpusFile* file;
            ~OpusFileGuard() { op_free(file); }
          } file_guard{file};

          std::vector<int16_t> decoded;
          const ogg_int64_t expected_samples = op_pcm_total(file, -1);
          if (expected_samples <= 0 ||
              static_cast<uint64_t>(expected_samples) > kMaximumVoiceMailSamples) {
            throw std::runtime_error("invalid decoded media length");
          }
          decoded.reserve(static_cast<size_t>(expected_samples));
          std::array<opus_int16, 5760 * 2> frame{};
          while (true) {
            int link = 0;
            const int samples = op_read(file, frame.data(), static_cast<int>(frame.size()), &link);
            if (samples == 0) break;
            if (samples < 0) throw std::runtime_error("ogg opus decode failed");
            const int channels = op_channel_count(file, link);
            if (channels != 1 && channels != 2) {
              throw std::runtime_error("unsupported voice mail channels");
            }
            if (decoded.size() + static_cast<size_t>(samples) > kMaximumVoiceMailSamples) {
              throw std::runtime_error("decoded media too large");
            }
            if (channels == 1) {
              decoded.insert(decoded.end(), frame.begin(), frame.begin() + samples);
            } else {
              for (int index = 0; index < samples; ++index) {
                const int32_t mixed = static_cast<int32_t>(frame[index * 2]) +
                                      static_cast<int32_t>(frame[index * 2 + 1]);
                decoded.push_back(static_cast<int16_t>(mixed / 2));
              }
            }
          }
          if (decoded.size() != static_cast<size_t>(expected_samples)) {
            throw std::runtime_error("decoded media length mismatch");
          }
          std::vector<int16_t> output_16khz;
          output_16khz.reserve((decoded.size() + 2) / 3);
          for (size_t offset = 0; offset < decoded.size(); offset += 3) {
            int32_t sum = 0;
            const size_t count = std::min<size_t>(3, decoded.size() - offset);
            for (size_t index = 0; index < count; ++index) {
              sum += decoded[offset + index];
            }
            output_16khz.push_back(static_cast<int16_t>(sum / static_cast<int32_t>(count)));
          }

          const uint64_t now_ms = static_cast<uint64_t>(
              std::chrono::duration_cast<std::chrono::milliseconds>(
                  std::chrono::system_clock::now().time_since_epoch()).count());
          bool publish = false;
          {
            std::lock_guard lock(state_mutex_);
            publish = generation == connection_generation_.load() &&
                      protocol_connected_.load() && voice_mail_claim_active_ &&
                      voice_mail_playback_id_ == playback_id &&
                      active_voice_mail_.voice_mail_id_view() == item.voice_mail_id_view() &&
                      item.expires_at_unix_ms > now_ms;
            if (publish) {
              voice_mail_samples_ = std::move(output_16khz);
              voice_mail_sample_offset_ = 0;
              playback_sample_rate_ = 16'000;
            }
          }
          if (!publish) {
            fail(item.expires_at_unix_ms <= now_ms ? "VOICE MAIL EXPIRED"
                                                   : "VOICE MAIL FETCH CANCELLED");
            return;
          }
          enqueue_voice_mail_event(BackendEventType::voice_mail_playback_ready, item);
          enqueue_voice_mail_event(BackendEventType::voice_mail_playback_finished, item);
        } catch (const std::exception&) {
          fail("VOICE MAIL MEDIA ERROR");
        }
      });
}

void WebSocketVoiceBackend::finish_media_worker() {
  if (media_thread_.joinable() && !media_worker_running_.load()) media_thread_.join();
}

void WebSocketVoiceBackend::clear_voice_mail_locked() {
  active_voice_mail_ = {};
  voice_mail_playback_id_.clear();
  voice_mail_claim_wire_.clear();
  voice_mail_claim_idempotency_key_.clear();
  voice_mail_result_wire_.clear();
  voice_mail_claim_active_ = false;
  voice_mail_media_started_ = false;
  voice_mail_result_sent_ = false;
  voice_mail_samples_.clear();
  voice_mail_sample_offset_ = 0;
}

} // namespace companion::software_device

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

#include <algorithm>
#include <chrono>
#include <cstring>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <utility>

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

RuntimeConfigPatch parse_config(const json& payload) {
  const auto& config = payload.at("config");
  RuntimeConfigPatch out{};
  out.version = payload.at("config_version").get<uint64_t>();
  out.smart_vad_enabled = config.at("smart_vad_enabled").get<bool>();
  out.vad_threshold = config.at("vad_threshold").get<uint32_t>();
  out.vad_silence_ms = config.at("vad_silence_ms").get<uint32_t>();
  out.vad_min_speech_ms = config.at("vad_min_speech_ms").get<uint32_t>();
  out.idle_after_ms = config.at("idle_after_ms").get<uint32_t>();
  out.alarm_visible_ms = config.at("alarm_visible_ms").get<uint32_t>();
  return out;
}

std::string json_string(const json& object, const char* key) {
  const auto it = object.find(key);
  return it != object.end() && it->is_string() ? it->get<std::string>() : std::string{};
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
  if (decoder_ != nullptr) opus_decoder_destroy(decoder_);
  if (encoder_ != nullptr) opus_encoder_destroy(encoder_);
}

bool WebSocketVoiceBackend::start(uint64_t) {
  const auto parsed = parse_ws_url(url_);
  if (!parsed) return false;
  stop_connection(false);
  {
    std::lock_guard lock(state_mutex_);
    session_id_.clear();
    active_turn_id_.clear();
    last_begin_wire_.clear();
    turn_active_ = false;
    tts_active_ = false;
    upload_samples_.clear();
    playback_samples_.clear();
  }
  protocol_connected_.store(false);
  const uint64_t generation = connection_generation_.fetch_add(1) + 1;
  auto connection = std::make_shared<Connection>(*this, generation, *parsed, token_, device_id_);
  connection_ = connection;
  io_thread_ = std::thread([connection] { connection->run(); });
  return true;
}

void WebSocketVoiceBackend::tick(uint64_t) {}

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
  std::array<int16_t, kUplinkFrameSamples> final{};
  bool has_final = false;
  std::string turn_id;
  {
    std::lock_guard lock(state_mutex_);
    if (!turn_active_) return false;
    turn_id = active_turn_id_;
    if (!upload_samples_.empty()) {
      std::copy(upload_samples_.begin(), upload_samples_.end(), final.begin());
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
    if (!turn_active_ && !tts_active_) return;
    turn_id = active_turn_id_;
    turn_active_ = false;
    tts_active_ = false;
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
  if (events_.empty()) return false;
  event = events_.front();
  events_.pop_front();
  return true;
}

bool WebSocketVoiceBackend::report_config(const RuntimeConfigPatch& config, bool applied) {
  if (!protocol_connected_.load()) return false;
  json payload{{"config_version", config.version},
               {"applied", applied},
               {"config", {{"smart_vad_enabled", config.smart_vad_enabled},
                           {"vad_threshold", config.vad_threshold},
                           {"vad_silence_ms", config.vad_silence_ms},
                           {"vad_min_speech_ms", config.vad_min_speech_ms},
                           {"idle_after_ms", config.idle_after_ms},
                           {"alarm_visible_ms", config.alarm_visible_ms}}}};
  const bool sent = send_text(encode_control(
      static_cast<int>(protocol::ControlType::config_report), payload.dump()));
  if (sent) {
    std::lock_guard lock(state_mutex_);
    ++stats_.config_reports;
  }
  return sent;
}

size_t WebSocketVoiceBackend::read_playback(std::span<int16_t> destination) {
  std::lock_guard lock(state_mutex_);
  const size_t count = std::min(destination.size(), playback_samples_.size());
  for (size_t i = 0; i < count; ++i) {
    destination[i] = playback_samples_.front();
    playback_samples_.pop_front();
  }
  return count;
}

bool WebSocketVoiceBackend::playback_empty() const {
  std::lock_guard lock(state_mutex_);
  return playback_samples_.empty();
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
                                                  std::optional<uint64_t> generation_id) {
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
      .idempotency_key = {},
      .occurred_at = {},
  };
  if (!protocol::encode(envelope, buffer, written)) return {};
  return {buffer.data(), written};
}

void WebSocketVoiceBackend::enqueue_event(BackendEventType type, std::string_view text) {
  std::lock_guard lock(state_mutex_);
  if (events_.size() == kMaximumEvents) events_.pop_front();
  BackendEvent event{};
  event.type = type;
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
    clear_turn_media_locked();
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
    if (type == protocol::ControlType::session_ready) {
      const auto audio = payload.at("audio_params");
      if (incoming_session.empty() || payload.at("transport") != "websocket" ||
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
        if (payload.contains("config") && !payload.at("config").is_null()) {
          BackendEvent config{};
          config.type = BackendEventType::config;
          config.config = parse_config(payload);
          if (events_.size() == kMaximumEvents) events_.pop_front();
          events_.push_back(config);
        }
      }
      protocol_connected_.store(true);
      json advertise{{"capabilities", json::array({
          {{"name", "device.volume.set"}, {"version", "1"}, {"kind", "command"}}
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
      const auto it = payload.find("ui");
      const std::string primary = it != payload.end() && it->is_object()
                                      ? json_string(*it, "primary")
                                      : std::string{};
      enqueue_event(BackendEventType::ui_card, primary.empty() ? "ui.card" : primary);
      break;
    }
    case protocol::ControlType::ui_state:
      enqueue_event(BackendEventType::ui_card, json_string(payload, "emotion"));
      break;
    case protocol::ControlType::agent_status:
      enqueue_event(BackendEventType::ui_card, json_string(payload, "state"));
      break;
    case protocol::ControlType::config_update: {
      BackendEvent event{};
      event.type = BackendEventType::config;
      event.config = parse_config(payload);
      std::lock_guard lock(state_mutex_);
      if (events_.size() == kMaximumEvents) events_.pop_front();
      events_.push_back(event);
      break;
    }
    case protocol::ControlType::capability_call: {
      {
        std::lock_guard lock(state_mutex_);
        ++stats_.capability_calls;
      }
      json result;
      const std::string name = json_string(payload, "name");
      const std::string version = json_string(payload, "version");
      const auto arguments = payload.find("arguments");
      const int deadline_ms = payload.value("deadline_ms", 0);
      if (correlation_id.empty() || !has_generation || deadline_ms < 50 || deadline_ms > 5000) {
        result = {{"ok", false}, {"error", "invalid_argument"}};
      } else if (name != "device.volume.set" || version != "1") {
        result = {{"ok", false}, {"error", "unsupported"}};
      } else if (arguments == payload.end() || !arguments->is_object() ||
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
      const bool sent = send_text(encode_control(
          static_cast<int>(protocol::ControlType::capability_result), result.dump(), {},
          correlation_id, true, incoming_generation));
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
      enqueue_event(BackendEventType::error, json_string(payload, "code"));
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

} // namespace companion::software_device

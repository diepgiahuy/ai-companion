#include "websocket_backend.hpp"

#include "companion/app.hpp"
#include "companion/audio_runtime.hpp"

#include <boost/asio/connect.hpp>
#include <boost/asio/ip/tcp.hpp>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>
#include <boost/beast/websocket.hpp>
#include <nlohmann/json.hpp>

#include <algorithm>
#include <chrono>
#include <ctime>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <iomanip>
#include <iostream>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <sstream>
#include <thread>
#include <utility>
#include <vector>

namespace companion::software_device {
namespace beast = boost::beast;
namespace http = beast::http;
namespace net = boost::asio;
using tcp = net::ip::tcp;
using json = nlohmann::json;

namespace {
struct FixtureMicrophone final : Microphone {
  bool active{};
  size_t frames_left{12};
  bool start_capture() override { active = true; return true; }
  size_t read_capture(std::span<int16_t> destination) override {
    if (!active || frames_left == 0) return 0;
    const size_t count = std::min(destination.size(), kAudioFrameSamples);
    std::fill_n(destination.begin(), count, static_cast<int16_t>(1'200));
    --frames_left;
    return count;
  }
  void stop_capture() override { active = false; }
  void arm(size_t frames = 12) { frames_left = frames; }
};

struct RecordingSpeaker final : Speaker {
  bool active{};
  uint32_t rate{};
  uint64_t samples{};
  uint64_t starts{};
  uint64_t stops{};
  uint64_t hash{1469598103934665603ULL};
  size_t maximum_write{static_cast<size_t>(-1)};
  bool drained{true};
  bool start_playback(uint32_t sample_rate_hz) override {
    if (sample_rate_hz != 24'000 && sample_rate_hz != 16'000) return false;
    active = true;
    rate = sample_rate_hz;
    ++starts;
    return true;
  }
  size_t write_playback(std::span<const int16_t> pcm) override {
    if (!active) return 0;
    const size_t accepted = std::min(pcm.size(), maximum_write);
    for (const int16_t sample : pcm.first(accepted)) {
      const uint16_t value = static_cast<uint16_t>(sample);
      hash ^= value & 0xffU;
      hash *= 1099511628211ULL;
      hash ^= value >> 8U;
      hash *= 1099511628211ULL;
    }
    samples += accepted;
    return accepted;
  }
  bool playback_drained() const override { return drained; }
  void stop_playback() override {
    if (active) ++stops;
    active = false;
  }
};

struct RecordingDisplay final : Display {
  struct Record { UiState state; std::string text; };
  std::vector<Record> records;
  void show(UiState state, std::string_view text) override {
    records.push_back({state, std::string(text)});
  }
  bool saw(UiState state) const {
    return std::any_of(records.begin(), records.end(), [state](const Record& record) {
      return record.state == state;
    });
  }
  bool contains(std::string_view text) const {
    return std::any_of(records.begin(), records.end(), [text](const Record& record) {
      return record.text.find(text) != std::string::npos;
    });
  }
};

struct ScriptedButton final : Button {
  bool pending{};
  bool consume_press(uint64_t) override { return std::exchange(pending, false); }
  void press() { pending = true; }
};

struct DeviceFixture {
  FixtureMicrophone microphone;
  RecordingSpeaker speaker;
  AudioRuntime audio;
  RecordingDisplay display;
  ScriptedButton button;
  WebSocketVoiceBackend backend;
  CompanionApp app;
  uint64_t now_ms{};

  DeviceFixture(const std::string& url, const std::string& token, const std::string& device)
      : audio(microphone, speaker),
        backend(url, token, device),
        app(audio, display, button, backend,
            AppConfig{.maximum_recording_ms = 8'000,
                      .idle_after_ms = 60'000,
                      .alarm_visible_ms = 10'000,
                      .alarm_tone_ms = 0,
                      .smart_vad_enabled = false}) {}

  void tick() {
    now_ms += 20;
    app.tick(now_ms);
    std::this_thread::sleep_for(std::chrono::milliseconds(3));
  }

  bool until(const std::function<bool()>& predicate, size_t iterations = 1'500) {
    for (size_t i = 0; i < iterations; ++i) {
      tick();
      if (predicate()) return true;
    }
    return false;
  }

  void require_ready() {
    if (!app.start(now_ms)) throw std::runtime_error("app start failed");
    if (!until([&] { return app.state() == UiState::ready; }))
      throw std::runtime_error("device did not reach ready");
  }

  void begin_audio_turn() {
    microphone.arm();
    button.press();
    tick();
    if (app.state() != UiState::listening) throw std::runtime_error("turn did not start");
    for (int i = 0; i < 6; ++i) tick();
  }

  void finish_audio_turn() {
    button.press();
    tick();
    if (app.state() != UiState::processing)
      throw std::runtime_error("turn did not enter processing");
  }
};

struct ScenarioResult {
  std::string id;
  bool passed{};
  std::string error;
  uint64_t elapsed_ms{};
  json counters = json::object();
};

ScenarioResult run_scenario(std::string id, const std::function<void(ScenarioResult&)>& body) {
  ScenarioResult result{};
  result.id = std::move(id);
  const auto started = std::chrono::steady_clock::now();
  try {
    body(result);
    result.passed = true;
  } catch (const std::exception& error) {
    result.error = error.what();
  }
  result.elapsed_ms = static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::milliseconds>(
      std::chrono::steady_clock::now() - started).count());
  return result;
}

void require(bool value, std::string_view message) {
  if (!value) throw std::runtime_error(std::string(message));
}

void patch_device_config(const std::string& host, const std::string& port,
                         const std::string& admin_token, const std::string& device_id) {
  net::io_context io;
  tcp::resolver resolver(io);
  beast::tcp_stream stream(io);
  const auto endpoints = resolver.resolve(host, port);
  stream.connect(endpoints);
  json body{{"smart_vad_enabled", false}, {"vad_threshold", 600},
            {"vad_silence_ms", 700}, {"vad_min_speech_ms", 200},
            {"idle_after_ms", 60'000}, {"alarm_visible_ms", 10'000}};
  http::request<http::string_body> request{
      http::verb::patch, "/v1/admin/devices/" + device_id + "/config", 11};
  request.set(http::field::host, host);
  request.set(http::field::authorization, "Bearer " + admin_token);
  request.set(http::field::content_type, "application/json");
  request.body() = body.dump();
  request.prepare_payload();
  http::write(stream, request);
  beast::flat_buffer buffer;
  http::response<http::string_body> response;
  http::read(stream, buffer, response);
  require(response.result() == http::status::ok, "config PATCH did not return 200");
  beast::error_code ignored;
  stream.socket().shutdown(tcp::socket::shutdown_both, ignored);
}

http::response<http::string_body> device_request(
    const std::string& host, const std::string& port, http::verb method,
    const std::string& target, const std::string& device_id,
    const std::string& token, std::string body = {},
    std::string_view content_type = "application/json") {
  net::io_context io;
  tcp::resolver resolver(io);
  beast::tcp_stream stream(io);
  stream.connect(resolver.resolve(host, port));
  http::request<http::string_body> request{method, target, 11};
  request.set(http::field::host, host);
  request.set(http::field::authorization, "Bearer " + token);
  request.set("Device-Id", device_id);
  if (!body.empty()) {
    request.set(http::field::content_type, std::string(content_type));
    request.body() = std::move(body);
    request.prepare_payload();
  }
  http::write(stream, request);
  beast::flat_buffer buffer;
  http::response<http::string_body> response;
  http::read(stream, buffer, response);
  beast::error_code ignored;
  stream.socket().shutdown(tcp::socket::shutdown_both, ignored);
  return response;
}

std::string future_rfc3339() {
  const std::time_t value = std::time(nullptr) + 3'600;
  std::tm utc{};
#if defined(_WIN32)
  gmtime_s(&utc, &value);
#else
  gmtime_r(&value, &utc);
#endif
  std::ostringstream output;
  output << std::put_time(&utc, "%Y-%m-%dT%H:%M:%SZ");
  return output.str();
}

std::string read_binary_file(const std::string& path) {
  std::ifstream input(path, std::ios::binary);
  if (!input) throw std::runtime_error("voice-mail fixture is unavailable");
  return {std::istreambuf_iterator<char>(input), std::istreambuf_iterator<char>()};
}

std::string resolve_voice_mail_relationship(const std::string& host,
                                            const std::string& port,
                                            const std::string& sender_device,
                                            const std::string& sender_token,
                                            const std::string& recipient_device) {
  const auto response = device_request(host, port, http::verb::get,
                                       "/v1/voice-mail/recipients",
                                       sender_device, sender_token);
  require(response.result() == http::status::ok,
          "voice-mail recipient selector did not return 200");
  const auto payload = json::parse(response.body());
  for (const auto& recipient : payload.at("recipients")) {
    if (recipient.value("peer_device_id", std::string{}) == recipient_device) {
      const std::string relationship_id =
          recipient.value("relationship_id", std::string{});
      require(!relationship_id.empty(), "voice-mail recipient relationship id missing");
      return relationship_id;
    }
  }
  throw std::runtime_error("authorized voice-mail recipient relationship not found");
}

std::string provision_voice_mail(const std::string& host, const std::string& port,
                                 const std::string& sender_device,
                                 const std::string& sender_token,
                                 const std::string& recipient_device,
                                 const std::string& media_path,
                                 const std::string& checksum,
                                 std::string_view suffix) {
  const std::string media = read_binary_file(media_path);
  const std::string key = "tier1-voice-mail-" + std::string(suffix);
  const std::string relationship_id = resolve_voice_mail_relationship(
      host, port, sender_device, sender_token, recipient_device);
  const json create{{"relationship_id", relationship_id},
                    {"duration_ms", 240},
                    {"size_bytes", media.size()},
                    {"checksum_sha256", checksum},
                    {"policy", "ephemeral"},
                    {"expires_at", future_rfc3339()},
                    {"idempotency_key", key + "-create"}};
  auto response = device_request(host, port, http::verb::post, "/v1/voice-mail",
                                 sender_device, sender_token, create.dump());
  require(response.result() == http::status::created,
          "voice-mail create did not return 201");
  const std::string id = json::parse(response.body()).at("id").get<std::string>();
  response = device_request(host, port, http::verb::put,
                            "/v1/voice-mail/" + id + "/media", sender_device,
                            sender_token, media, "audio/ogg");
  require(response.result() == http::status::no_content,
          "voice-mail upload did not return 204");
  response = device_request(
      host, port, http::verb::post, "/v1/voice-mail/" + id + "/complete",
      sender_device, sender_token,
      json{{"idempotency_key", key + "-complete"}}.dump());
  require(response.result() == http::status::ok,
          "voice-mail complete did not return 200");
  return id;
}

size_t unread_voice_mail_count(const std::string& host, const std::string& port,
                               const std::string& device_id,
                               const std::string& token) {
  const auto response = device_request(host, port, http::verb::get,
                                       "/v1/voice-mail?limit=8", device_id, token);
  require(response.result() == http::status::ok,
          "voice-mail list did not return 200");
  return json::parse(response.body()).at("items").size();
}

bool probe_v1_rejection(const std::string& host, const std::string& port,
                        const std::string& token, const std::string& device_id) {
  net::io_context io;
  tcp::resolver resolver(io);
  boost::beast::websocket::stream<beast::tcp_stream> ws(io);
  beast::get_lowest_layer(ws).connect(resolver.resolve(host, port));
  ws.set_option(boost::beast::websocket::stream_base::decorator(
      [&](boost::beast::websocket::request_type& request) {
        request.set(http::field::authorization, "Bearer " + token);
        request.set("Device-Id", device_id);
        request.set("Client-Id", device_id);
      }));
  ws.handshake(host + ":" + port, "/v2/device");
  ws.write(net::buffer(std::string(
      R"({"version":1,"type":"hello","transport":"websocket","audio_params":{}})")));
  beast::flat_buffer response;
  ws.read(response);
  const std::string text = beast::buffers_to_string(response.data());
  return text.find("unsupported_protocol_version") != std::string::npos;
}

std::pair<std::string, std::string> split_host_port(const std::string& url) {
  const std::string prefix = "ws://";
  if (!url.starts_with(prefix)) throw std::runtime_error("only ws:// is supported by Tier-1");
  const std::string rest = url.substr(prefix.size());
  const size_t slash = rest.find('/');
  const std::string authority = rest.substr(0, slash);
  const size_t colon = authority.rfind(':');
  if (colon == std::string::npos) return {authority, "80"};
  return {authority.substr(0, colon), authority.substr(colon + 1)};
}

json stats_json(const WebSocketVoiceBackend::Stats& stats) {
  return {{"connections", stats.connections}, {"turns_started", stats.turns_started},
          {"cancels", stats.cancels}, {"stale_controls", stats.stale_controls},
          {"discarded_binary_packets", stats.discarded_binary_packets},
          {"config_reports", stats.config_reports}};
}
} // namespace

int run(int argc, char** argv) {
  std::string url = "ws://127.0.0.1:18000/v2/device";
  std::string token = "tier1-device-token";
  std::string device_id = "software-device-tier1";
  std::string admin_token = "tier1-admin-token";
  std::string expected_text = "Tier-1 tool parity ok";
  std::string evidence_path = "software-device-evidence.json";
  std::string scenario_set = "core";
  std::string voice_mail_sender_device;
  std::string voice_mail_sender_token;
  std::string voice_mail_media;
  std::string voice_mail_checksum;
  for (int i = 1; i < argc; ++i) {
    const std::string arg = argv[i];
    auto value = [&](const char* name) -> std::string {
      if (i + 1 >= argc) throw std::runtime_error(std::string("missing value for ") + name);
      return argv[++i];
    };
    if (arg == "--url") url = value("--url");
    else if (arg == "--device-id") device_id = value("--device-id");
    else if (arg == "--token") token = value("--token");
    else if (arg == "--admin-token") admin_token = value("--admin-token");
    else if (arg == "--expected-text") expected_text = value("--expected-text");
    else if (arg == "--evidence") evidence_path = value("--evidence");
    else if (arg == "--scenario-set") scenario_set = value("--scenario-set");
    else if (arg == "--voice-mail-sender-device")
      voice_mail_sender_device = value("--voice-mail-sender-device");
    else if (arg == "--voice-mail-sender-token")
      voice_mail_sender_token = value("--voice-mail-sender-token");
    else if (arg == "--voice-mail-media") voice_mail_media = value("--voice-mail-media");
    else if (arg == "--voice-mail-checksum")
      voice_mail_checksum = value("--voice-mail-checksum");
    else throw std::runtime_error("unknown argument: " + arg);
  }
  const auto [host, port] = split_host_port(url);
  std::vector<ScenarioResult> results;

  if (scenario_set == "core") {
  results.push_back(run_scenario("hello_turn_tts", [&](ScenarioResult& result) {
    DeviceFixture fixture(url, token, device_id);
    fixture.require_ready();
    fixture.begin_audio_turn();
    fixture.finish_audio_turn();
    require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
            "turn did not return ready");
    require(fixture.display.contains("tier1 transcript"), "transcript was not rendered");
    require(fixture.display.saw(UiState::speaking), "speaking state was not observed");
    require(fixture.speaker.samples > 0, "no decoded TTS PCM reached speaker sink");
    result.counters = stats_json(fixture.backend.stats());
    result.counters["speaker_samples"] = fixture.speaker.samples;
    result.counters["speaker_hash"] = fixture.speaker.hash;
  }));

  results.push_back(run_scenario("duplicate_message_id", [&](ScenarioResult& result) {
    DeviceFixture fixture(url, token, device_id);
    fixture.require_ready();
    fixture.begin_audio_turn();
    require(fixture.backend.resend_last_begin_for_test(), "could not resend begin envelope");
    fixture.tick();
    require(fixture.app.state() == UiState::listening,
            "duplicate begin changed client state; replay suppression likely regressed");
    fixture.finish_audio_turn();
    require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
            "duplicate-id turn did not complete");
    result.counters = stats_json(fixture.backend.stats());
  }));

  results.push_back(run_scenario("barge_in_generation", [&](ScenarioResult& result) {
    DeviceFixture fixture(url, token, device_id);
    fixture.require_ready();
    fixture.begin_audio_turn();
    fixture.finish_audio_turn();
    require(fixture.until([&] { return fixture.app.state() == UiState::speaking; }),
            "first turn never reached speaking");
    const uint64_t before = fixture.speaker.samples;
    fixture.microphone.arm();
    fixture.button.press();
    fixture.tick();
    require(fixture.app.state() == UiState::listening, "barge-in did not start a new turn");
    for (int i = 0; i < 6; ++i) fixture.tick();
    require(fixture.speaker.samples == before, "stale audio leaked while new turn was listening");
    fixture.button.press();
    fixture.tick();
    require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
            "second turn did not complete after barge-in");
    const auto stats = fixture.backend.stats();
    require(stats.turns_started == 2, "barge-in did not create exactly two turns");
    require(stats.cancels >= 1, "barge-in did not cancel the old turn");
    result.counters = stats_json(stats);
  }));

  results.push_back(run_scenario("reconnect_new_session", [&](ScenarioResult& result) {
    DeviceFixture fixture(url, token, device_id);
    fixture.require_ready();
    const std::string first = fixture.backend.session_id();
    require(!first.empty(), "first session id missing");
    fixture.backend.disconnect_for_test();
    require(fixture.until([&] { return fixture.app.state() == UiState::error; }),
            "disconnect did not reach application error state");
    fixture.button.press();
    fixture.tick();
    require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
            "reconnect did not return ready");
    const std::string second = fixture.backend.session_id();
    require(!second.empty() && second != first, "reconnect reused old session id");
    fixture.begin_audio_turn();
    fixture.finish_audio_turn();
    require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
            "post-reconnect turn failed");
    result.counters = stats_json(fixture.backend.stats());
  }));

  results.push_back(run_scenario("config_update_report", [&](ScenarioResult& result) {
    const std::string device = device_id;
    DeviceFixture fixture(url, token, device);
    fixture.require_ready();
    const uint64_t before = fixture.app.runtime_config_version();
    patch_device_config(host, port, admin_token, device);
    require(fixture.until([&] { return fixture.app.runtime_config_version() > before; }),
            "live config update was not applied");
    const auto stats = fixture.backend.stats();
    require(stats.config_reports > 0, "applied config was not reported to backend");
    result.counters = stats_json(stats);
    result.counters["config_version"] = fixture.app.runtime_config_version();
  }));

  results.push_back(run_scenario("protocol_v1_rejected", [&](ScenarioResult&) {
    require(probe_v1_rejection(host, port, token, device_id),
            "v1 probe did not receive unsupported_protocol_version");
  }));
  } else if (scenario_set == "voice-mail") {
    require(!voice_mail_sender_device.empty() && !voice_mail_sender_token.empty() &&
                !voice_mail_media.empty() && voice_mail_checksum.size() == 64,
            "voice-mail scenario credentials/fixture are required");
    results.push_back(run_scenario("voice_mail_lifecycle", [&](ScenarioResult& result) {
      {
        DeviceFixture notified(url, token, device_id);
        notified.require_ready();
        provision_voice_mail(host, port, voice_mail_sender_device,
                             voice_mail_sender_token, device_id, voice_mail_media,
                             voice_mail_checksum, "success");
        require(notified.until([&] {
                  return notified.app.state() == UiState::voice_mail_waiting;
                }), "voice-mail notification did not enter waiting state");
        require(notified.speaker.starts == 0,
                "voice mail auto-played before an explicit gesture");
      }

      DeviceFixture fixture(url, token, device_id);
      require(fixture.app.start(fixture.now_ms), "cold-start app failed to start");
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_waiting;
              }), "unread voice mail was not recovered after a cold restart");
      require(fixture.speaker.starts == 0,
              "recovered voice mail auto-played before an explicit gesture");

      fixture.backend.disconnect_for_test();
      require(fixture.until([&] { return fixture.app.state() == UiState::error; }),
              "duplicate recovery setup did not observe disconnect");
      fixture.button.press();
      fixture.tick();
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_waiting;
              }), "voice-mail item was not preserved and deduplicated after reconnect");
      require(fixture.speaker.starts == 0,
              "duplicate notification triggered automatic playback");

      fixture.speaker.drained = false;
      fixture.button.press();
      fixture.tick();
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_playing &&
                       fixture.speaker.samples > 0;
              }), "voice-mail media did not reach the logical speaker");
      for (int i = 0; i < 20; ++i) fixture.tick();
      require(fixture.app.state() == UiState::voice_mail_playing,
              "voice mail completed before the speaker drained");
      fixture.speaker.drained = true;
      require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
              "voice-mail success did not return ready after drain");
      require(unread_voice_mail_count(host, port, device_id, token) == 0,
              "successfully consumed ephemeral voice mail remained unread");

      provision_voice_mail(host, port, voice_mail_sender_device,
                           voice_mail_sender_token, device_id, voice_mail_media,
                           voice_mail_checksum, "cancel");
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_waiting;
              }), "cancel fixture did not reach waiting");
      fixture.button.press();
      fixture.tick();
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_playing;
              }), "cancel fixture did not start playback");
      fixture.button.press();
      fixture.tick();
      require(fixture.app.state() == UiState::voice_mail_waiting,
              "explicit cancel did not return waiting");
      require(fixture.until([&] {
                return unread_voice_mail_count(host, port, device_id, token) == 1;
              }, 200), "cancelled voice mail was consumed instead of released");

      fixture.button.press();
      fixture.tick();
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_playing;
              }), "released voice mail could not be reclaimed");
      require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
              "reclaimed voice mail did not complete");

      provision_voice_mail(host, port, voice_mail_sender_device,
                           voice_mail_sender_token, device_id, voice_mail_media,
                           voice_mail_checksum, "timeout");
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_waiting;
              }), "timeout fixture did not reach waiting");
      fixture.speaker.maximum_write = 0;
      fixture.button.press();
      fixture.tick();
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_playing;
              }), "timeout fixture did not start playback");
      require(fixture.until([&] {
                return fixture.app.state() == UiState::voice_mail_waiting;
              }), "stalled output did not time out to waiting");
      fixture.speaker.maximum_write = static_cast<size_t>(-1);
      require(fixture.until([&] {
                return unread_voice_mail_count(host, port, device_id, token) == 1;
              }, 200), "timed-out voice mail was consumed instead of released");
      result.counters = stats_json(fixture.backend.stats());
      result.counters["speaker_samples"] = fixture.speaker.samples;
      result.counters["cold_start_recoveries"] = 1;
      result.counters["unread_after_cancel"] = 1;
      result.counters["unread_after_timeout"] = 1;
    }));
  } else if (scenario_set == "tool") {
    results.push_back(run_scenario("agent_tool_authoritative_mutation", [&](ScenarioResult& result) {
      DeviceFixture fixture(url, token, device_id);
      fixture.require_ready();
      fixture.begin_audio_turn();
      fixture.finish_audio_turn();
      require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
              "tool turn did not return ready");
      require(fixture.display.contains(expected_text),
              "deterministic model/tool response was not rendered");
      require(fixture.speaker.samples > 0, "tool turn produced no decoded TTS PCM");
      result.counters = stats_json(fixture.backend.stats());
      result.counters["speaker_samples"] = fixture.speaker.samples;
    }));
  } else {
    throw std::runtime_error("unsupported scenario set: " + scenario_set);
  }

  bool all_passed = true;
  json scenarios = json::array();
  for (const auto& result : results) {
    all_passed = all_passed && result.passed;
    scenarios.push_back({{"id", result.id}, {"result", result.passed ? "passed" : "failed"},
                         {"error", result.error}, {"elapsed_ms", result.elapsed_ms},
                         {"counters", result.counters}});
    std::cout << (result.passed ? "PASS " : "FAIL ") << result.id;
    if (!result.error.empty()) std::cout << ": " << result.error;
    std::cout << '\n';
  }

  const char* commit = std::getenv("COMPANION_EVIDENCE_COMMIT");
  const char* fingerprint = std::getenv("COMPANION_EVIDENCE_CONFIG_SHA256");
  const json providers = json{{"asr", "mock"}, {"agent", "adk_fake_responses"},
                              {"tts", "mock"}};
  json evidence{{"schema_version", 1},
                {"evidence_class", "tier1_orchestration"},
                {"result", all_passed ? "passed" : "failed"},
                {"commit", commit == nullptr ? "unknown" : commit},
                {"backend_config_sha256", fingerprint == nullptr ? "unknown" : fingerprint},
                {"device_fsm", "production_companion_app"},
                {"protocol", "v2"},
                {"scenario_set", scenario_set},
                {"providers", providers},
                {"promotion", "orchestration_only"},
                {"scenarios", scenarios}};
  std::ofstream output(evidence_path);
  output << evidence.dump(2) << '\n';
  if (!output) throw std::runtime_error("could not write evidence file");
  return all_passed ? 0 : 1;
}

} // namespace companion::software_device

int main(int argc, char** argv) {
  try {
    return companion::software_device::run(argc, argv);
  } catch (const std::exception& error) {
    std::cerr << "software-device fatal: " << error.what() << '\n';
    return 2;
  }
}
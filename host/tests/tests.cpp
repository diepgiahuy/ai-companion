#include "companion/app.hpp"
#include "companion/audio_runtime.hpp"
#include "companion/input_router.hpp"
#include "companion/wire_protocol.hpp"
#include "companion/mock_backend.hpp"

#include <algorithm>
#include <array>
#include <cassert>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <span>
#include <string>
#include <utility>
#include <vector>

using namespace companion;

namespace {

struct FakeMicrophone final : Microphone {
  bool active{};
  bool start_result{true};
  int frames_available{4};
  bool start_capture() override {
    active = start_result;
    return start_result;
  }
  size_t read_capture(std::span<int16_t> output) override {
    if (!active || frames_available-- <= 0) return 0;
    const size_t count = std::min(output.size(), kAudioFrameSamples);
    std::fill_n(output.begin(), count, 123);
    return count;
  }
  void stop_capture() override { active = false; }
};

struct FakeSpeaker final : Speaker {
  bool active{};
  bool start_result{true};
  size_t maximum_write{160};
  size_t samples_written{};
  int start_count{};
  int stop_count{};

  bool start_playback(uint32_t rate) override {
    ++start_count;
    active = start_result && rate == kAudioSampleRateHz;
    return active;
  }
  size_t write_playback(std::span<const int16_t> pcm) override {
    if (!active) return 0;
    const size_t count = std::min(pcm.size(), maximum_write);
    samples_written += count;
    return count;
  }
  bool drained{true};
  bool playback_drained() const override { return drained; }
  void stop_playback() override {
    if (active) ++stop_count;
    active = false;
  }
};

struct FakeDisplay final : Display {
  std::vector<std::pair<UiState, std::string>> events;
  void show(UiState state, std::string_view text) override {
    events.emplace_back(state, text);
  }
};

struct ScheduledInput final {
  explicit ScheduledInput(std::vector<uint64_t> scheduled = {})
      : presses(std::move(scheduled)) {}
  std::vector<uint64_t> presses;
  size_t next{};
  InputRouter router;

  void sample(uint64_t now_ms) {
    if (next < presses.size() && now_ms >= presses[next]) {
      ++next;
      router.queue_primary_action(InputIntent::primary_action);
    }
  }
};

struct TestApp final {
  ScheduledInput input;
  CompanionApp app;

  TestApp(AudioEngine& audio, Display& display, ScheduledInput scheduled_input,
          VoiceBackend& backend, AppConfig config = {})
      : input(std::move(scheduled_input)),
        app(audio, display, input.router, backend, config) {}

  bool start(uint64_t now_ms) { return app.start(now_ms); }
  void tick(uint64_t now_ms) {
    input.sample(now_ms);
    app.tick(now_ms);
  }
  UiState state() const { return app.state(); }
  const AppConfig& config() const { return app.config(); }
  uint64_t streamed_samples() const { return app.streamed_samples(); }
  uint64_t runtime_config_version() const { return app.runtime_config_version(); }
  uint64_t settings_version() const { return app.settings_version(); }
};

VoiceMailMetadata voice_mail(std::string_view id = "voice-1") {
  VoiceMailMetadata item{};
  item.set_voice_mail_id(id);
  item.set_from_device_id("sender-device");
  item.set_media_format("ogg_opus");
  item.set_checksum_sha256(
      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef");
  item.duration_ms = 200;
  item.size_bytes = 1'024;
  item.expires_at_unix_ms = 1'800'000'000'000ULL;
  item.policy = VoiceMailMetadata::Policy::ephemeral;
  return item;
}

struct VadMicrophone final : Microphone {
  bool active{};
  int frame{};
  bool start_capture() override { active = true; frame = 0; return true; }
  size_t read_capture(std::span<int16_t> output) override {
    if (!active) return 0;
    const size_t count = std::min(output.size(), kAudioFrameSamples);
    const int16_t value = frame++ < 2 ? 1'000 : 0;
    std::fill_n(output.begin(), count, value);
    return count;
  }
  void stop_capture() override { active = false; }
};

struct SilentMicrophone final : Microphone {
  bool start_capture() override { return true; }
  size_t read_capture(std::span<int16_t>) override { return 0; }
  void stop_capture() override {}
};

std::string read_fixture(const char* relative_path) {
  std::ifstream fixture(std::string(COMPANION_SOURCE_DIR) + "/" + relative_path,
                        std::ios::binary);
  assert(fixture.good());
  return {std::istreambuf_iterator<char>(fixture), std::istreambuf_iterator<char>()};
}

void canonical_v2_envelope_matches_golden_fixture() {
  std::array<char, 256> output{};
  size_t written = 0;
  const protocol::Envelope envelope{
      .type = protocol::ControlType::session_hello,
      .message_id = "firmware-1",
      .payload_json =
          "{\"transport\":\"websocket\",\"audio_params\":{"
          "\"format\":\"opus\",\"sample_rate\":16000,"
          "\"channels\":1,\"frame_duration\":60}}",
      .correlation_id = {},
      .session_id = {},
      .turn_id = {},
      .generation_id = 0,
      .has_generation_id = false,
      .idempotency_key = {},
      .occurred_at = {},
  };
  assert(protocol::encode(envelope, output, written));
  std::string expected = read_fixture("testdata/protocol/v2/session_hello.json");
  while (!expected.empty() && (expected.back() == '\n' || expected.back() == '\r')) {
    expected.pop_back();
  }
  assert(std::string_view(output.data(), written) == expected);

  const protocol::Envelope escaped{
      .type = protocol::ControlType::session_pong,
      .message_id = "firmware-2\nquoted\"",
      .payload_json = "{}",
      .correlation_id = {},
      .session_id = "session\\one",
      .turn_id = {},
      .generation_id = 0,
      .has_generation_id = false,
      .idempotency_key = {},
      .occurred_at = {},
  };
  assert(protocol::encode(escaped, output, written));
  assert(std::string_view(output.data(), written) ==
         "{\"version\":2,\"type\":\"session.pong\",\"message_id\":"
         "\"firmware-2\\nquoted\\\"\",\"session_id\":\"session\\\\one\","
         "\"payload\":{}}");

  const protocol::Envelope ordered{
      .type = protocol::ControlType::turn_listen,
      .message_id = "firmware-3",
      .payload_json = "{\"state\":\"start\"}",
      .correlation_id = "server-1",
      .session_id = "session-1",
      .turn_id = "turn-1",
      .generation_id = 7,
      .has_generation_id = true,
      .idempotency_key = "idem-1",
      .occurred_at = "2026-08-13T10:00:00Z",
  };
  assert(protocol::encode(ordered, output, written));
  assert(std::string_view(output.data(), written) ==
         "{\"version\":2,\"type\":\"turn.listen\",\"message_id\":\"firmware-3\","
         "\"correlation_id\":\"server-1\",\"session_id\":\"session-1\","
         "\"turn_id\":\"turn-1\",\"generation_id\":7,"
         "\"idempotency_key\":\"idem-1\","
         "\"occurred_at\":\"2026-08-13T10:00:00Z\","
         "\"payload\":{\"state\":\"start\"}}");

  std::array<char, 16> too_small{};
  assert(!protocol::encode(envelope, too_small, written));
  protocol::Envelope invalid_payload = envelope;
  invalid_payload.payload_json = "{bad}";
  assert(!protocol::encode(invalid_payload, output, written));
  invalid_payload.payload_json = "{\"state\":}";
  assert(!protocol::encode(invalid_payload, output, written));
  invalid_payload.payload_json = "[]";
  assert(!protocol::encode(invalid_payload, output, written));
  invalid_payload.payload_json =
      " { \"nested\": [true, false, null, -12.5e+2, {\"escaped\":\"\\u263a\"}] } ";
  assert(protocol::encode(invalid_payload, output, written));
  protocol::ControlType parsed{};
  assert(protocol::parse_type("capability.call", parsed));
  assert(parsed == protocol::ControlType::capability_call);
  assert(protocol::parse_type("capability.result", parsed));
  assert(parsed == protocol::ControlType::capability_result);
  assert(protocol::parse_type("voice_mail.available", parsed));
  assert(parsed == protocol::ControlType::voice_mail_available);
  assert(protocol::parse_type("pairing.confirmation", parsed));
  assert(parsed == protocol::ControlType::pairing_confirmation);
  assert(!protocol::parse_type("config.update", parsed));
  assert(!protocol::parse_type("config.report", parsed));
  assert(!protocol::parse_type("config", parsed));
}

void connect(TestApp& app) {
  assert(app.start(0));
  assert(app.state() == UiState::connecting);
  app.tick(0);
  assert(app.state() == UiState::ready);
}

void conversation_happy_path() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{100, 200}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);

  connect(app);
  app.tick(100);
  assert(app.state() == UiState::listening);
  app.tick(120);
  app.tick(140);
  assert(app.streamed_samples() == 640);
  app.tick(200);
  assert(app.state() == UiState::processing);
  app.tick(449);
  assert(app.state() == UiState::processing);
  for (uint64_t now = 450; now < 500 && app.state() != UiState::ready; ++now) {
    app.tick(now);
  }
  assert(app.state() == UiState::ready);
  assert(speaker.start_count == 1);
  assert(speaker.samples_written == 3'200);
  assert(backend.received_samples() == 640);
}

void timeout_finishes_recording() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend, AppConfig{100});
  connect(app);
  app.tick(10);
  app.tick(30);
  app.tick(110);
  assert(app.state() == UiState::processing);
}

void silence_is_rejected() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10, 20}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);
  app.tick(10);
  app.tick(20);
  assert(app.state() == UiState::error);
  assert(display.events.back().second == "NO AUDIO");
}

void barge_in_cancels_reply_and_starts_capture() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10, 20, 271}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);
  app.tick(10);
  app.tick(15);
  app.tick(20);
  app.tick(270);
  assert(app.state() == UiState::speaking);
  app.tick(271);
  assert(app.state() == UiState::listening);
  assert(speaker.stop_count == 1);
  assert(microphone.active);
  assert(backend.playback_empty());
}

void smart_vad_finishes_after_speech_silence() {
  VadMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10}};
  MockVoiceBackend backend;
  AppConfig config{};
  config.smart_vad_enabled = true;
  config.vad_mean_abs_threshold = 100;
  config.vad_min_speech_ms = 0;
  config.vad_silence_ms = 100;
  TestApp app(audio, display, input, backend, config);
  connect(app);
  app.tick(10);
  app.tick(20);
  app.tick(40);
  app.tick(160);
  assert(app.state() == UiState::processing);
}

void idle_and_alarm_states_are_non_destructive() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input;
  MockVoiceBackend backend;
  AppConfig config{};
  config.idle_after_ms = 100;
  config.alarm_visible_ms = 100;
  TestApp app(audio, display, input, backend, config);
  connect(app);
  app.tick(100);
  assert(app.state() == UiState::idle);
  assert(backend.inject_event(BackendEventType::schedule, "15:00 TEAM"));
  app.tick(101);
  assert(app.state() == UiState::idle);
  assert(backend.inject_event(BackendEventType::alarm, "TIMER DONE"));
  app.tick(102);
  assert(app.state() == UiState::alarm);
  assert(speaker.start_count == 1);
  assert(speaker.samples_written > 0);
  app.tick(202);
  assert(app.state() == UiState::ready);
  assert(!speaker.active);
}

void runtime_config_is_versioned_and_last_known_good() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input;
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);
  SettingsTwin good{};
  good.version = 3;
  good.settings.smart_vad_enabled = false;
  good.settings.vad_threshold = 700;
  good.settings.vad_silence_ms = 650;
  good.settings.vad_min_speech_ms = 200;
  good.settings.idle_after_ms = 9'000;
  good.settings.alarm_visible_ms = 12'000;
  good.settings.alarm_tone_ms = 1'000;
  good.settings.alarm_tone_hz = 900;
  good.settings.alarm_tone_amplitude = 4'000;
  good.settings.ota_poll_interval_s = 7'200;
  good.settings.volume = 85;
  good.settings.wake_threshold = 0.72F;
  good.settings.set_wake_model("hey_bin");

  assert(good.valid());
  const bool good_queued = backend.inject_settings(good); (void)good_queued;
  assert(good_queued);
  app.tick(1);
  assert(app.runtime_config_version() == 3);
  assert(app.settings_version() == 3);
  assert(app.config().volume == 85);
  assert(app.config().wake_threshold >= 0.719F && app.config().wake_threshold <= 0.721F);
  assert(app.config().wake_model.data() == std::string_view("hey_bin"));
  assert(app.config().ota_poll_interval_s == 7'200);

  SettingsTwin stale = good;
  stale.version = 2;
  stale.settings.volume = 50;
  const bool stale_queued = backend.inject_settings(stale); (void)stale_queued;
  assert(stale_queued);
  app.tick(2);
  assert(app.settings_version() == 3);
  assert(app.config().volume == 85);

  SettingsTwin invalid = good;
  invalid.version = 4;
  invalid.settings.volume = 120; // > 100
  assert(!invalid.valid());
  const bool invalid_queued = backend.inject_settings(invalid); (void)invalid_queued;
  assert(invalid_queued);
  app.tick(3);
  assert(app.settings_version() == 3);
  assert(app.config().volume == 85);

  SettingsTwin invalid_wake = good;
  invalid_wake.version = 5;
  invalid_wake.settings.wake_threshold = 0.20F; // < 0.40
  assert(!invalid_wake.valid());
  const bool invalid_wake_queued = backend.inject_settings(invalid_wake); (void)invalid_wake_queued;
  assert(invalid_wake_queued);
  app.tick(4);
  assert(app.settings_version() == 3);
  assert(app.config().volume == 85);
}

void voice_mail_waits_deduplicates_and_completes_only_after_drain() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  speaker.drained = false;
  FakeDisplay display;
  ScheduledInput input{{10}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);

  const auto item = voice_mail();
  assert(backend.inject_voice_mail(item));
  app.tick(1);
  assert(app.state() == UiState::voice_mail_waiting);
  assert(speaker.start_count == 0);
  assert(backend.inject_voice_mail(item));
  app.tick(2);
  assert(app.state() == UiState::voice_mail_waiting);

  app.tick(10);
  assert(app.state() == UiState::voice_mail_claiming);
  assert(backend.voice_mail_claims() == 1);
  for (uint64_t now = 11; now < 40; ++now) app.tick(now);
  assert(app.state() == UiState::voice_mail_playing);
  assert(backend.voice_mail_successes() == 0);
  assert(speaker.samples_written == 3'200);

  speaker.drained = true;
  app.tick(40);
  assert(backend.voice_mail_successes() == 1);
  assert(app.state() == UiState::voice_mail_claiming);
  app.tick(41);
  assert(app.state() == UiState::ready);
}

void voice_mail_invalid_cancel_and_expiry_are_safe() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10, 12}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);

  auto invalid = voice_mail("invalid");
  invalid.set_checksum_sha256("not-a-checksum");
  assert(backend.inject_voice_mail(invalid));
  app.tick(1);
  assert(app.state() == UiState::ready);

  const auto item = voice_mail("voice-cancel");
  assert(backend.inject_voice_mail(item));
  app.tick(2);
  app.tick(10);
  app.tick(11);
  assert(app.state() == UiState::voice_mail_playing);
  app.tick(12);
  assert(app.state() == UiState::voice_mail_waiting);
  assert(backend.voice_mail_failures() == 1);
  assert(backend.voice_mail_successes() == 0);

  assert(backend.inject_voice_mail(item, BackendEventType::voice_mail_expired));
  app.tick(13);
  assert(app.state() == UiState::ready);
  assert(display.events.back().second == "NO VOICE MAIL");
}

void voice_mail_output_stall_times_out_without_consuming() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  speaker.maximum_write = 0;
  FakeDisplay display;
  ScheduledInput input{{10}};
  MockVoiceBackend backend;
  AppConfig config{};
  config.voice_mail_operation_timeout_ms = 5;
  TestApp app(audio, display, input, backend, config);
  connect(app);

  const auto item = voice_mail("voice-timeout");
  assert(backend.inject_voice_mail(item));
  app.tick(1);
  app.tick(10);
  app.tick(11);
  assert(app.state() == UiState::voice_mail_playing);
  app.tick(16);
  assert(app.state() == UiState::voice_mail_waiting);
  assert(display.events.back().second == "VOICE MAIL TIMEOUT");
  assert(backend.voice_mail_failures() == 1);
  assert(backend.voice_mail_successes() == 0);
}

void retained_voice_mail_returns_to_waiting_after_drain() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);

  auto item = voice_mail("voice-retained");
  item.policy = VoiceMailMetadata::Policy::retained;
  assert(backend.inject_voice_mail(item));
  app.tick(1);
  app.tick(10);
  for (uint64_t now = 11; now < 50 &&
                                app.state() != UiState::voice_mail_waiting;
       ++now) {
    app.tick(now);
  }
  assert(backend.voice_mail_successes() == 1);
  assert(app.state() == UiState::voice_mail_waiting);
}

void stale_generation_events_after_barge_in_or_cancel_are_invalidated() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10, 20, 271}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);

  const uint64_t initial_epoch = backend.session_epoch();
  app.tick(10);
  app.tick(15);
  app.tick(20);
  const uint64_t turn1_gen = backend.media_generation();
  app.tick(270);
  assert(app.state() == UiState::speaking);

  app.tick(271);
  assert(app.state() == UiState::listening);
  assert(backend.media_generation() > turn1_gen);

  PresentationCardV1 stale_card{};
  assert(stale_card.assign(1, "card", "STALE TITLE", "STALE PRIMARY", "STALE SECONDARY", 0));
  assert(backend.inject_card(stale_card, BackendEventScope::generation, initial_epoch, turn1_gen));
  assert(backend.inject_scoped_event(BackendEventType::tts_sentence, "OBSOLETE SENTENCE",
                                     BackendEventScope::generation, initial_epoch, turn1_gen));
  assert(backend.inject_scoped_event(BackendEventType::transcript, "OBSOLETE TRANSCRIPT",
                                     BackendEventScope::generation, initial_epoch, turn1_gen));

  app.tick(272);
  assert(app.state() == UiState::listening);
  for (const auto& ev : display.events) {
    assert(ev.second != "STALE PRIMARY");
    assert(ev.second != "OBSOLETE SENTENCE");
    assert(ev.second != "OBSOLETE TRANSCRIPT");
  }
}

void disconnect_and_reconnect_invalidates_stale_session_and_generation_events() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input{{10, 20, 500}};
  MockVoiceBackend backend;
  TestApp app(audio, display, input, backend);
  connect(app);

  const uint64_t session1_epoch = backend.session_epoch();
  app.tick(10);
  app.tick(15);
  app.tick(20);
  app.tick(270);
  assert(app.state() == UiState::speaking);

  backend.disconnect();
  const uint64_t disconnected_epoch = backend.session_epoch();
  assert(disconnected_epoch > session1_epoch);

  PresentationCardV1 stale_card{};
  assert(stale_card.assign(1, "card", "OLD SESSION CARD", "OLD SESSION PRIMARY", "", 0));
  assert(backend.inject_card(stale_card, BackendEventScope::generation, session1_epoch, 1));
  assert(backend.inject_scoped_event(BackendEventType::tts_started, {},
                                     BackendEventScope::generation, session1_epoch, 1));

  app.tick(300);
  assert(app.state() == UiState::error);
  assert(display.events.back().second == "DISCONNECTED");
  assert(!speaker.active);

  app.tick(500);
  assert(app.state() == UiState::connecting);
  assert(backend.session_epoch() > disconnected_epoch);

  app.tick(501);
  assert(app.state() == UiState::ready);

  assert(!speaker.active);
  for (const auto& ev : display.events) {
    assert(ev.second != "OLD SESSION PRIMARY");
    assert(ev.second != "OLD SESSION CARD");
  }
}

void settings_twin_validation_and_dynamic_apply() {
  DeviceSettings settings{};
  assert(settings.validate());

  // Test boundaries
  settings.volume = 0;
  assert(settings.validate());
  settings.volume = 100;
  assert(settings.validate());
  settings.volume = 101;
  assert(!settings.validate());
  settings.volume = 70;

  settings.wake_threshold = 0.40F;
  assert(settings.validate());
  settings.wake_threshold = 0.9999F;
  assert(settings.validate());
  settings.wake_threshold = 0.39F;
  assert(!settings.validate());
  settings.wake_threshold = 1.0F;
  assert(!settings.validate());
  settings.wake_threshold = 0.60F;

  settings.alarm_tone_ms = 60001;
  assert(!settings.validate());
  settings.alarm_tone_ms = 900;

  settings.alarm_tone_hz = 49;
  assert(!settings.validate());
  settings.alarm_tone_hz = 5001;
  assert(!settings.validate());
  settings.alarm_tone_hz = 880;

  settings.alarm_tone_amplitude = -1;
  assert(!settings.validate());
  settings.alarm_tone_amplitude = 3500;
  assert(settings.validate());

  settings.wake_model.fill('\0');
  assert(!settings.validate());
  settings.set_wake_model("hey_bin");
  assert(settings.validate());

  SettingsTwin twin{
      .version = 1,
      .settings = settings,
  };
  assert(twin.valid());

  twin.version = 0;
  assert(!twin.valid());
  twin.version = 10;
  assert(twin.valid());

  // Test copy and assignment
  SettingsTwin patch{};
  patch.version = 5;
  patch.settings.smart_vad_enabled = true;
  patch.settings.vad_threshold = 500;
  patch.settings.vad_silence_ms = 900;
  patch.settings.vad_min_speech_ms = 300;
  patch.settings.idle_after_ms = 8000;
  patch.settings.alarm_visible_ms = 15000;
  patch.settings.ota_poll_interval_s = 14400;

  SettingsTwin converted(patch);
  assert(converted.version == 5);
  assert(converted.settings.vad_threshold == 500);
  assert(converted.settings.vad_silence_ms == 900);
  assert(converted.settings.vad_min_speech_ms == 300);
  assert(converted.settings.idle_after_ms == 8000);
  assert(converted.settings.alarm_visible_ms == 15000);
  assert(converted.settings.ota_poll_interval_s == 14400);
  assert(converted.settings.volume == 70);
  assert(converted.valid());

  // Test CompanionApp direct apply
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  AudioRuntime audio(microphone, speaker);
  FakeDisplay display;
  ScheduledInput input;
  MockVoiceBackend backend;
  CompanionApp app(audio, display, input.router, backend);
  assert(app.start(0));

  twin.version = 1;
  twin.settings.volume = 92;
  twin.settings.wake_threshold = 0.85F;
  twin.settings.set_wake_model("hey_bin");
  assert(app.apply_settings(twin));
  assert(app.settings_version() == 1);
  assert(app.config().volume == 92);
  assert(app.config().wake_threshold >= 0.849F && app.config().wake_threshold <= 0.851F);
  assert(app.config().wake_model.data() == std::string_view("hey_bin"));

  // Reject monotonic <= version
  twin.version = 1;
  twin.settings.volume = 40;
  assert(!app.apply_settings(twin));
  assert(app.config().volume == 92);

  // Apply higher version
  twin.version = 2;
  assert(app.apply_settings(twin));
  assert(app.settings_version() == 2);
  assert(app.config().volume == 40);
}

} // namespace

int main() {
  canonical_v2_envelope_matches_golden_fixture();
  conversation_happy_path();
  timeout_finishes_recording();
  silence_is_rejected();
  barge_in_cancels_reply_and_starts_capture();
  smart_vad_finishes_after_speech_silence();
  idle_and_alarm_states_are_non_destructive();
  runtime_config_is_versioned_and_last_known_good();
  settings_twin_validation_and_dynamic_apply();
  voice_mail_waits_deduplicates_and_completes_only_after_drain();
  voice_mail_invalid_cancel_and_expiry_are_safe();
  voice_mail_output_stall_times_out_without_consuming();
  retained_voice_mail_returns_to_waiting_after_drain();
  stale_generation_events_after_barge_in_or_cancel_are_invalidated();
  disconnect_and_reconnect_invalidates_stale_session_and_generation_events();
  std::cout << "PASS: protocol-v2 + streaming + timeout + silence + barge-in + smart VAD + idle/alarm + settings-twin + voice-mail FSM + A6 recovery/stale-event convergence\n";
}

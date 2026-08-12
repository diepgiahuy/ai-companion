#include "companion/app.hpp"
#include "companion/mock_backend.hpp"

#include <algorithm>
#include <cassert>
#include <cstdint>
#include <iostream>
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
  bool playback_drained() const override { return true; }
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

struct FakeButton final : Button {
  explicit FakeButton(std::vector<uint64_t> scheduled = {})
      : presses(std::move(scheduled)) {}
  std::vector<uint64_t> presses;
  size_t next{};
  bool consume_press(uint64_t now_ms) override {
    if (next < presses.size() && now_ms >= presses[next]) {
      ++next;
      return true;
    }
    return false;
  }
};


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

void connect(CompanionApp& app) {
  assert(app.start(0));
  assert(app.state() == UiState::connecting);
  app.tick(0);
  assert(app.state() == UiState::ready);
}

void conversation_happy_path() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  FakeDisplay display;
  FakeButton button{{100, 200}};
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend);

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
  FakeDisplay display;
  FakeButton button{{10}};
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend,
                   AppConfig{100});
  connect(app);
  app.tick(10);
  app.tick(30);
  app.tick(110);
  assert(app.state() == UiState::processing);
}

void silence_is_rejected() {
  SilentMicrophone microphone;
  FakeSpeaker speaker;
  FakeDisplay display;
  FakeButton button{{10, 20}};
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend);
  connect(app);
  app.tick(10);
  app.tick(20);
  assert(app.state() == UiState::error);
  assert(display.events.back().second == "NO AUDIO");
}

void barge_in_cancels_reply_and_starts_capture() {
  FakeMicrophone microphone;
  FakeSpeaker speaker;
  FakeDisplay display;
  FakeButton button{{10, 20, 271}};
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend);
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
  FakeDisplay display;
  FakeButton button{{10}};
  MockVoiceBackend backend;
  AppConfig config{};
  config.smart_vad_enabled = true;
  config.vad_mean_abs_threshold = 100;
  config.vad_min_speech_ms = 0;
  config.vad_silence_ms = 100;
  CompanionApp app(microphone, speaker, display, button, backend, config);
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
  FakeDisplay display;
  FakeButton button;
  MockVoiceBackend backend;
  AppConfig config{};
  config.idle_after_ms = 100;
  config.alarm_visible_ms = 100;
  CompanionApp app(microphone, speaker, display, button, backend, config);
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
  FakeDisplay display;
  FakeButton button;
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend);
  connect(app);
  RuntimeConfigPatch good{};
  good.version = 3;
  good.smart_vad_enabled = false;
  good.vad_threshold = 700;
  good.vad_silence_ms = 650;
  good.vad_min_speech_ms = 200;
  good.idle_after_ms = 9'000;
  good.alarm_visible_ms = 12'000;
  const bool good_queued = backend.inject_config(good); (void)good_queued;
  assert(good_queued);
  app.tick(1);
  assert(app.runtime_config_version() == 3);
  assert(backend.reported_config_version() == 3);
  assert(backend.reported_config_applied());

  RuntimeConfigPatch stale = good;
  stale.version = 2;
  stale.vad_threshold = 999;
  const bool stale_queued = backend.inject_config(stale); (void)stale_queued;
  assert(stale_queued);
  app.tick(2);
  assert(app.runtime_config_version() == 3);

  RuntimeConfigPatch invalid = good;
  invalid.version = 4;
  invalid.vad_threshold = 100'000;
  const bool invalid_queued = backend.inject_config(invalid); (void)invalid_queued;
  assert(invalid_queued);
  app.tick(3);
  assert(app.runtime_config_version() == 3);
  assert(backend.reported_config_version() == 4);
  assert(!backend.reported_config_applied());
}

} // namespace

int main() {
  conversation_happy_path();
  timeout_finishes_recording();
  silence_is_rejected();
  barge_in_cancels_reply_and_starts_capture();
  smart_vad_finishes_after_speech_silence();
  idle_and_alarm_states_are_non_destructive();
  runtime_config_is_versioned_and_last_known_good();
  std::cout << "PASS: streaming + timeout + silence + barge-in + smart VAD + idle/alarm + runtime-config\n";
}

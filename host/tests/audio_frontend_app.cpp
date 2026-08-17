#include "companion/app.hpp"
#include "companion/audio_runtime.hpp"

#include <algorithm>
#include <array>
#include <cassert>
#include <cstdint>
#include <span>
#include <string>
#include <utility>
#include <vector>

using namespace companion;

namespace {

struct MonitorMicrophone final : Microphone {
  bool active{};
  size_t starts{};
  size_t stops{};
  int16_t sample{1'000};
  bool start_capture() override { active = true; ++starts; return true; }
  size_t read_capture(std::span<int16_t> destination) override {
    if (!active) return 0;
    const size_t count = std::min(destination.size(), kAudioFrameSamples);
    std::fill_n(destination.begin(), count, sample);
    return count;
  }
  void stop_capture() override { if (active) ++stops; active = false; }
};

struct RecordingSpeaker final : Speaker {
  bool active{};
  uint32_t rate{};
  size_t written{};
  bool start_playback(uint32_t sample_rate_hz) override { active = true; rate = sample_rate_hz; return true; }
  size_t write_playback(std::span<const int16_t> pcm) override { if (!active) return 0; written += pcm.size(); return pcm.size(); }
  bool playback_drained() const override { return true; }
  void stop_playback() override { active = false; }
};

struct RecordingDisplay final : Display {
  std::vector<std::pair<UiState, std::string>> events;
  void show(UiState state, std::string_view text) override { events.emplace_back(state, std::string(text)); }
};

struct NoButton final : Button { bool consume_press(uint64_t) override { return false; } };

struct ScriptedFrontend final : AudioFrontend {
  bool started{};
  bool reference_active{};
  uint64_t reference_epoch{};
  uint32_t last_reference_rate{};
  size_t resets{};
  size_t reference_begins{};
  size_t reference_ends{};
  std::vector<AudioFrontendEvent> events;
  size_t next_event{};
  std::vector<int16_t> references;
  PlaybackReferenceStats stats{};

  bool start() override { started = true; return true; }
  void reset() override { ++resets; reference_active = false; reference_epoch = 0; }
  bool begin_playback_reference(uint64_t epoch) override {
    if (epoch == 0) return false;
    reference_active = true;
    reference_epoch = epoch;
    ++reference_begins;
    stats.epoch = epoch;
    stats.active = true;
    return true;
  }
  void end_playback_reference(uint64_t epoch) override {
    if (!reference_active || epoch != reference_epoch) return;
    reference_active = false;
    reference_epoch = 0;
    ++reference_ends;
    stats.epoch = 0;
    stats.active = false;
  }
  bool push_playback_reference(std::span<const int16_t> pcm_16k,
                               uint32_t sample_rate_hz) override {
    if (!reference_active || sample_rate_hz != 16'000) return false;
    last_reference_rate = sample_rate_hz;
    references.insert(references.end(), pcm_16k.begin(), pcm_16k.end());
    stats.pushed_samples += pcm_16k.size();
    return true;
  }
  PlaybackReferenceStats playback_reference_stats() const override { return stats; }
  AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                      std::span<int16_t> cleaned_16k) override {
    const size_t count = std::min(microphone_16k.size(), cleaned_16k.size());
    for (size_t i = 0; i < count; ++i) cleaned_16k[i] = static_cast<int16_t>(microphone_16k[i] / 2);
    AudioFrontendEvent event = AudioFrontendEvent::none;
    if (next_event < events.size()) event = events[next_event++];
    return {.samples = count, .event = event};
  }
};

struct ScriptedBackend final : VoiceBackend {
  size_t begin_count{};
  size_t finish_count{};
  size_t cancel_count{};
  uint64_t received_samples{};
  int16_t last_first_sample{};
  uint32_t playback_rate{24'000};
  std::vector<int16_t> playback;
  size_t playback_offset{};
  std::vector<BackendEvent> events;
  bool start(uint64_t) override { BackendEvent e{}; e.type = BackendEventType::connected; events.push_back(e); return true; }
  void tick(uint64_t) override {}
  bool begin_turn(uint64_t, ListenMode) override { ++begin_count; return true; }
  bool send_audio(std::span<const int16_t> pcm) override { received_samples += pcm.size(); if (!pcm.empty()) last_first_sample = pcm.front(); return true; }
  bool finish_turn(uint64_t) override { ++finish_count; return true; }
  void cancel_turn() override { ++cancel_count; }
  bool poll_event(BackendEvent& event) override { if (events.empty()) return false; event = events.front(); events.erase(events.begin()); return true; }
  bool report_config(const RuntimeConfigPatch&, bool) override { return true; }
  bool claim_voice_mail(const VoiceMailMetadata&, uint64_t) override { return false; }
  bool report_voice_mail_playback(const VoiceMailMetadata&, bool,
                                  std::string_view, uint64_t) override { return false; }
  void cancel_voice_mail(const VoiceMailMetadata&, std::string_view, uint64_t) override {}
  size_t read_playback(std::span<int16_t> destination) override {
    const size_t remaining = playback.size() - playback_offset;
    const size_t count = std::min(remaining, destination.size());
    std::copy_n(playback.begin() + static_cast<std::ptrdiff_t>(playback_offset), count, destination.begin());
    playback_offset += count;
    return count;
  }
  bool playback_empty() const override { return playback_offset == playback.size(); }
  uint32_t playback_sample_rate_hz() const override { return playback_rate; }
  void push(BackendEventType type) { BackendEvent e{}; e.type = type; events.push_back(e); }
};

void connect_without_wake(CompanionApp& app, ScriptedFrontend& frontend) {
  frontend.events.push_back(AudioFrontendEvent::none);
  assert(app.start(0)); app.tick(0); assert(app.state() == UiState::ready);
}

void wake_and_finish_one_turn(CompanionApp& app, ScriptedFrontend& frontend, ScriptedBackend& backend) {
  frontend.events.push_back(AudioFrontendEvent::wake_detected); app.tick(20);
  assert(app.state() == UiState::listening); assert(backend.begin_count == 1);
  frontend.events.push_back(AudioFrontendEvent::speech_started); app.tick(40);
  assert(backend.received_samples == kAudioFrameSamples); assert(backend.last_first_sample == 500);
  frontend.events.push_back(AudioFrontendEvent::speech_ended); app.tick(60);
  assert(app.state() == UiState::processing); assert(backend.finish_count == 1);
}

void wake_and_vad_use_canonical_turn_path() {
  MonitorMicrophone microphone; RecordingSpeaker speaker; RecordingDisplay display; NoButton button;
  ScriptedBackend backend; ScriptedFrontend frontend; AudioRuntime audio(microphone, speaker, frontend);
  AppConfig config{}; config.vad_min_speech_ms = 0;
  CompanionApp app(audio, audio, display, button, backend, audio, config);
  connect_without_wake(app, frontend);
  assert(frontend.started && microphone.active && microphone.starts == 1);
  wake_and_finish_one_turn(app, frontend, backend);
  assert(microphone.active && microphone.starts == 1);
}

void playback_reference_and_speech_barge_in_share_generation_path() {
  MonitorMicrophone microphone; RecordingSpeaker speaker; RecordingDisplay display; NoButton button;
  ScriptedBackend backend; ScriptedFrontend frontend; AudioRuntime audio(microphone, speaker, frontend);
  AppConfig config{}; config.vad_min_speech_ms = 0;
  CompanionApp app(audio, audio, display, button, backend, audio, config);
  connect_without_wake(app, frontend); wake_and_finish_one_turn(app, frontend, backend);
  backend.playback = {100, 200, 300, 400, 500, 600}; backend.playback_offset = 0;
  backend.push(BackendEventType::tts_started); frontend.events.push_back(AudioFrontendEvent::speech_started); app.tick(100);
  assert(speaker.rate == 24'000); assert(speaker.written == 6);
  // CompanionApp forwards accepted 24-kHz speaker PCM unchanged. AudioRuntime
  // alone converts it into the 16-kHz domain seen by the vendor frontend.
  assert(frontend.last_reference_rate == 16'000);
  assert((frontend.references == std::vector<int16_t>{100, 250, 400, 550}));
  assert(frontend.reference_begins == 1);
  assert(frontend.reference_ends == 1);
  assert(!frontend.reference_active);
  const auto stats = audio.stats();
  assert(stats.playback_starts == 1);
  assert(stats.playback_stops == 1);
  assert(stats.accepted_playback_samples == 6);
  assert(stats.reference_epochs == 1);
  assert(stats.reference_converter_dropped_groups == 0);
  assert(!stats.reference_active);
  assert(backend.cancel_count == 1); assert(backend.begin_count == 2); assert(app.state() == UiState::listening);
}

void alarm_suspends_hands_free_monitor_and_ready_restarts_it() {
  MonitorMicrophone microphone; RecordingSpeaker speaker; RecordingDisplay display; NoButton button;
  ScriptedBackend backend; ScriptedFrontend frontend; AudioRuntime audio(microphone, speaker, frontend);
  AppConfig config{}; config.alarm_tone_ms = 0; config.alarm_visible_ms = 100;
  CompanionApp app(audio, audio, display, button, backend, audio, config);
  connect_without_wake(app, frontend); backend.push(BackendEventType::alarm); app.tick(10);
  assert(app.state() == UiState::alarm); assert(!microphone.active); assert(microphone.stops == 1);
  assert(audio.stats().reference_epochs == 0);
  app.tick(110); assert(app.state() == UiState::ready); assert(microphone.active); assert(microphone.starts == 2);
}

} // namespace

int main() {
  wake_and_vad_use_canonical_turn_path();
  playback_reference_and_speech_barge_in_share_generation_path();
  alarm_suspends_hands_free_monitor_and_ready_restarts_it();
  return 0;
}

#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def replace(path, old, new, count=1):
    p = ROOT / path
    text = p.read_text(encoding="utf-8")
    actual = text.count(old)
    if actual != count:
        raise SystemExit(f"{path} drift: expected {count}, found {actual}: {old[:120]!r}")
    p.write_text(text.replace(old, new), encoding="utf-8")

replace("components/companion_app/include/companion/app.hpp", "#pragma once\n\n#include <algorithm>", "#pragma once\n\n#include \"companion/audio_frontend.hpp\"\n\n#include <algorithm>")
replace("components/companion_app/include/companion/app.hpp",
'''  CompanionApp(Microphone& microphone, Speaker& speaker, Display& display,
               Button& button, VoiceBackend& backend, AppConfig config = {});
''',
'''  CompanionApp(Microphone& microphone, Speaker& speaker, Display& display,
               Button& button, VoiceBackend& backend, AppConfig config = {},
               AudioFrontend* audio_frontend = nullptr);
''')
replace("components/companion_app/include/companion/app.hpp",
'''  VoiceBackend& backend_;
  AppConfig config_;
  UiState state_{UiState::booting};
''',
'''  VoiceBackend& backend_;
  AppConfig config_;
  AudioFrontend* audio_frontend_{};
  PlaybackReference24To16 playback_reference_converter_{};
  UiState state_{UiState::booting};
''')
replace("components/companion_app/include/companion/app.hpp",
'''  bool alarm_pending_{};
  bool alarm_tone_active_{};
  std::array<char, 96> upcoming_{};
''',
'''  bool alarm_pending_{};
  bool alarm_tone_active_{};
  bool microphone_capturing_{};
  std::array<char, 96> upcoming_{};
''')
replace("components/companion_app/include/companion/app.hpp",
'''  std::array<int16_t, kAudioFrameSamples> capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_frame_{};
''',
'''  std::array<int16_t, kAudioFrameSamples> capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> cleaned_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_reference_frame_{};
''')
replace("components/companion_app/include/companion/app.hpp",
'''  void process_backend_event(const BackendEvent& event, uint64_t now_ms);
  void pump_capture(uint64_t now_ms);
  void pump_playback(uint64_t now_ms);
''',
'''  void process_backend_event(const BackendEvent& event, uint64_t now_ms);
  void pump_capture(uint64_t now_ms);
  void pump_audio_frontend(uint64_t now_ms);
  void pump_playback(uint64_t now_ms);
  void feed_playback_reference(std::span<const int16_t> actually_played);
''')
replace("components/companion_app/include/companion/app.hpp",
'''  void begin_listening(uint64_t now_ms);
  void finish_listening(uint64_t now_ms);
  void abort_and_listen(uint64_t now_ms);
  void enter_ready(uint64_t now_ms, std::string_view message = "PRESS TO TALK");
''',
'''  void begin_listening(uint64_t now_ms);
  void finish_listening(uint64_t now_ms);
  void abort_and_listen(uint64_t now_ms);
  bool ensure_local_capture();
  void stop_local_capture();
  void enter_ready(uint64_t now_ms, std::string_view message = "PRESS TO TALK");
''')

replace("components/companion_app/src/app.cpp",
'''CompanionApp::CompanionApp(Microphone& microphone, Speaker& speaker,
                           Display& display, Button& button,
                           VoiceBackend& backend, AppConfig config)
    : microphone_(microphone), speaker_(speaker), display_(display),
      button_(button), backend_(backend), config_(config) {}
''',
'''CompanionApp::CompanionApp(Microphone& microphone, Speaker& speaker,
                           Display& display, Button& button,
                           VoiceBackend& backend, AppConfig config,
                           AudioFrontend* audio_frontend)
    : microphone_(microphone), speaker_(speaker), display_(display),
      button_(button), backend_(backend), config_(config),
      audio_frontend_(audio_frontend) {}
''')
replace("components/companion_app/src/app.cpp",
'''  if (!backend_.start(now_ms)) {
    fail("BACKEND START FAILED");
    return false;
  }
''',
'''  if (audio_frontend_ != nullptr && !audio_frontend_->start()) {
    fail("AUDIO FRONTEND FAILED");
    return false;
  }
  if (!backend_.start(now_ms)) {
    fail("BACKEND START FAILED");
    return false;
  }
''')
replace("components/companion_app/src/app.cpp",
'''  switch (state_) {
  case UiState::listening:
    pump_capture(now_ms);
    break;
  case UiState::speaking:
    pump_playback(now_ms);
    break;
''',
'''  switch (state_) {
  case UiState::ready:
  case UiState::idle:
  case UiState::processing:
    if (audio_frontend_ != nullptr) {
      pump_audio_frontend(now_ms);
    }
    break;
  case UiState::listening:
    if (audio_frontend_ != nullptr) {
      pump_audio_frontend(now_ms);
    } else {
      pump_capture(now_ms);
    }
    break;
  case UiState::speaking:
    pump_playback(now_ms);
    if (audio_frontend_ != nullptr) {
      pump_audio_frontend(now_ms);
    }
    break;
''')
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::pump_capture(uint64_t now_ms) {
  const size_t count = microphone_.read_capture(capture_frame_);
''',
'''void CompanionApp::pump_capture(uint64_t now_ms) {
  const size_t count = microphone_.read_capture(capture_frame_);
''')
# Insert frontend pump before pump_playback.
marker = '''void CompanionApp::pump_playback(uint64_t now_ms) {
'''
frontend_impl = r'''void CompanionApp::pump_audio_frontend(uint64_t now_ms) {
  if (audio_frontend_ == nullptr || !ensure_local_capture()) {
    return;
  }
  const size_t count = microphone_.read_capture(capture_frame_);
  if (count == 0) {
    return;
  }
  const auto result = audio_frontend_->process_capture(
      std::span<const int16_t>(capture_frame_.data(), count), cleaned_frame_);
  if (result.samples > cleaned_frame_.size()) {
    fail("AUDIO FRONTEND OVERFLOW");
    return;
  }

  if (result.event == AudioFrontendEvent::wake_detected &&
      (state_ == UiState::ready || state_ == UiState::idle)) {
    begin_listening(now_ms);
  } else if (result.event == AudioFrontendEvent::speech_started &&
             (state_ == UiState::speaking || state_ == UiState::processing)) {
    abort_and_listen(now_ms);
  }

  if (state_ == UiState::listening && result.samples > 0) {
    const auto cleaned = std::span<const int16_t>(cleaned_frame_.data(), result.samples);
    if (!backend_.send_audio(cleaned)) {
      fail("AUDIO STREAM FAILED");
      return;
    }
    streamed_samples_ += result.samples;
  }

  if (state_ == UiState::listening &&
      result.event == AudioFrontendEvent::speech_ended) {
    finish_listening(now_ms);
    return;
  }
  if (state_ == UiState::listening &&
      now_ms - recording_started_ms_ >= config_.maximum_recording_ms) {
    finish_listening(now_ms);
  }
}

'''
p = ROOT / "components/companion_app/src/app.cpp"
text = p.read_text(encoding="utf-8")
if text.count(marker) != 1:
    raise SystemExit("app.cpp pump_playback marker drifted")
text = text.replace(marker, frontend_impl + marker)
p.write_text(text, encoding="utf-8")

replace("components/companion_app/src/app.cpp",
'''    const size_t written = speaker_.write_playback(pending);
    if (written == 0) {
      break;
    }
    playback_offset_ += written;
''',
'''    const size_t written = speaker_.write_playback(pending);
    if (written == 0) {
      break;
    }
    feed_playback_reference(pending.first(written));
    playback_offset_ += written;
''')
# Insert playback-reference method before alarm tone.
marker = '''void CompanionApp::pump_alarm_tone() {
'''
ref_impl = r'''void CompanionApp::feed_playback_reference(
    std::span<const int16_t> actually_played) {
  if (audio_frontend_ == nullptr || actually_played.empty()) {
    return;
  }
  const uint32_t rate = backend_.playback_sample_rate_hz();
  if (rate == kAudioSampleRateHz) {
    audio_frontend_->push_playback_reference(actually_played);
    return;
  }
  if (rate == 24'000) {
    const size_t converted = playback_reference_converter_.convert(
        actually_played, playback_reference_frame_);
    if (converted > 0) {
      audio_frontend_->push_playback_reference(
          std::span<const int16_t>(playback_reference_frame_.data(), converted));
    }
  }
}

'''
p = ROOT / "components/companion_app/src/app.cpp"
text = p.read_text(encoding="utf-8")
if text.count(marker) != 1:
    raise SystemExit("app.cpp alarm marker drifted")
text = text.replace(marker, ref_impl + marker)
p.write_text(text, encoding="utf-8")

# Legacy begin/finish remain unchanged; frontend mode reuses continuous local
# capture instead of starting/stopping it for every turn.
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::begin_listening(uint64_t now_ms) {
  if (!microphone_.start_capture()) {
    fail("MIC START FAILED");
    return;
  }
  const ListenMode mode = config_.smart_vad_enabled ? ListenMode::auto_vad : ListenMode::manual;
  if (!backend_.begin_turn(now_ms, mode)) {
    microphone_.stop_capture();
    fail("TURN START FAILED");
    return;
  }
''',
'''void CompanionApp::begin_listening(uint64_t now_ms) {
  if (audio_frontend_ != nullptr) {
    if (!ensure_local_capture()) {
      fail("MIC START FAILED");
      return;
    }
  } else if (!microphone_.start_capture()) {
    fail("MIC START FAILED");
    return;
  }
  const ListenMode mode = config_.smart_vad_enabled ? ListenMode::auto_vad : ListenMode::manual;
  if (!backend_.begin_turn(now_ms, mode)) {
    if (audio_frontend_ == nullptr) {
      microphone_.stop_capture();
    }
    fail("TURN START FAILED");
    return;
  }
''')
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::finish_listening(uint64_t now_ms) {
  microphone_.stop_capture();
  if (!backend_.finish_turn(now_ms)) {
''',
'''void CompanionApp::finish_listening(uint64_t now_ms) {
  if (audio_frontend_ == nullptr) {
    microphone_.stop_capture();
  }
  if (!backend_.finish_turn(now_ms)) {
''')
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::abort_and_listen(uint64_t now_ms) {
  backend_.cancel_turn();
  microphone_.stop_capture();
  speaker_.stop_playback();
  playback_count_ = 0;
  playback_offset_ = 0;
  begin_listening(now_ms);
}
''',
'''void CompanionApp::abort_and_listen(uint64_t now_ms) {
  backend_.cancel_turn();
  if (audio_frontend_ == nullptr) {
    microphone_.stop_capture();
  }
  speaker_.stop_playback();
  playback_count_ = 0;
  playback_offset_ = 0;
  begin_listening(now_ms);
}

bool CompanionApp::ensure_local_capture() {
  if (microphone_capturing_) {
    return true;
  }
  if (!microphone_.start_capture()) {
    return false;
  }
  microphone_capturing_ = true;
  return true;
}

void CompanionApp::stop_local_capture() {
  if (!microphone_capturing_) {
    return;
  }
  microphone_.stop_capture();
  microphone_capturing_ = false;
}
''')
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::enter_ready(uint64_t now_ms, std::string_view message) {
  state_ = UiState::ready;
''',
'''void CompanionApp::enter_ready(uint64_t now_ms, std::string_view message) {
  state_ = UiState::ready;
  if (audio_frontend_ != nullptr && !ensure_local_capture()) {
    fail("MIC START FAILED");
    return;
  }
''')
replace("components/companion_app/src/app.cpp",
'''void CompanionApp::fail(std::string_view reason) {
  state_ = UiState::error;
  microphone_.stop_capture();
''',
'''void CompanionApp::fail(std::string_view reason) {
  state_ = UiState::error;
  if (audio_frontend_ != nullptr) {
    stop_local_capture();
    audio_frontend_->reset();
  } else {
    microphone_.stop_capture();
  }
''')

# Add focused host behavior test and register it.
(ROOT / "host/tests/audio_frontend_app.cpp").write_text(r'''#include "companion/app.hpp"

#include <array>
#include <cassert>
#include <cstdint>
#include <deque>
#include <span>
#include <string_view>
#include <vector>

using namespace companion;

namespace {
struct Mic final : Microphone {
  bool active{}; int starts{}; int stops{}; std::deque<std::array<int16_t, 6>> frames;
  bool start_capture() override { active = true; ++starts; return true; }
  size_t read_capture(std::span<int16_t> out) override {
    if (!active || frames.empty()) return 0;
    auto frame = frames.front(); frames.pop_front();
    const size_t n = std::min(out.size(), frame.size());
    for (size_t i=0;i<n;++i) out[i]=frame[i]; return n;
  }
  void stop_capture() override { if (active) ++stops; active=false; }
};
struct Spk final : Speaker {
  uint32_t rate{}; std::vector<int16_t> written;
  bool start_playback(uint32_t r) override { rate=r; return true; }
  size_t write_playback(std::span<const int16_t> pcm) override { written.insert(written.end(), pcm.begin(), pcm.end()); return pcm.size(); }
  bool playback_drained() const override { return true; }
  void stop_playback() override {}
};
struct Disp final : Display { UiState last{}; void show(UiState s, std::string_view) override { last=s; } };
struct Btn final : Button { bool consume_press(uint64_t) override { return false; } };
struct Backend final : VoiceBackend {
  std::deque<BackendEvent> events; int begins{}; int finishes{}; int cancels{}; size_t sent{}; uint32_t rate{16000}; std::deque<int16_t> playback;
  bool start(uint64_t) override { BackendEvent e; e.type=BackendEventType::connected; events.push_back(e); return true; }
  void tick(uint64_t) override {}
  bool begin_turn(uint64_t, ListenMode) override { ++begins; return true; }
  bool send_audio(std::span<const int16_t> pcm) override { sent += pcm.size(); return true; }
  bool finish_turn(uint64_t) override { ++finishes; return true; }
  void cancel_turn() override { ++cancels; }
  bool poll_event(BackendEvent& e) override { if(events.empty()) return false; e=events.front(); events.pop_front(); return true; }
  bool report_config(const RuntimeConfigPatch&, bool) override { return true; }
  size_t read_playback(std::span<int16_t> out) override { size_t n=0; while(n<out.size() && !playback.empty()){out[n++]=playback.front(); playback.pop_front();} return n; }
  bool playback_empty() const override { return playback.empty(); }
  uint32_t playback_sample_rate_hz() const override { return rate; }
};
struct Frontend final : AudioFrontend {
  std::deque<AudioFrontendEvent> scripted; std::vector<int16_t> reference; int resets{};
  bool start() override { return true; }
  void reset() override { ++resets; }
  bool push_playback_reference(std::span<const int16_t> pcm) override { reference.insert(reference.end(),pcm.begin(),pcm.end()); return true; }
  AudioFrontendResult process_capture(std::span<const int16_t> mic, std::span<int16_t> cleaned) override {
    const size_t n=std::min(mic.size(),cleaned.size()); for(size_t i=0;i<n;++i) cleaned[i]=mic[i];
    AudioFrontendEvent event=AudioFrontendEvent::none; if(!scripted.empty()){event=scripted.front(); scripted.pop_front();}
    return {n,event};
  }
};
std::array<int16_t,6> frame(int16_t x){ return {x,x,x,x,x,x}; }
}

int main(){
  Mic mic; Spk spk; Disp disp; Btn btn; Backend backend; Frontend frontend;
  CompanionApp app(mic,spk,disp,btn,backend,{},&frontend);
  assert(app.start(0));
  app.tick(1); // connected -> ready; continuous local capture starts.
  assert(app.state()==UiState::ready); assert(mic.starts==1); assert(mic.active);

  mic.frames.push_back(frame(100)); frontend.scripted.push_back(AudioFrontendEvent::wake_detected);
  app.tick(20); assert(app.state()==UiState::listening); assert(backend.begins==1); assert(backend.sent==6);
  mic.frames.push_back(frame(110)); frontend.scripted.push_back(AudioFrontendEvent::speech_ended);
  app.tick(40); assert(app.state()==UiState::processing); assert(backend.finishes==1); assert(mic.active);

  BackendEvent tts; tts.type=BackendEventType::tts_started; backend.rate=24000; backend.playback={0,10,20,30,40,50}; backend.events.push_back(tts);
  app.tick(60); assert(app.state()==UiState::speaking); assert(spk.written.size()==6); assert(frontend.reference.size()==4);
  assert(frontend.reference[0]==0 && frontend.reference[1]==15 && frontend.reference[2]==30 && frontend.reference[3]==45);

  mic.frames.push_back(frame(120)); frontend.scripted.push_back(AudioFrontendEvent::speech_started);
  app.tick(80); assert(app.state()==UiState::listening); assert(backend.cancels==1); assert(backend.begins==2); assert(mic.starts==1); assert(mic.active);
  return 0;
}
''', encoding="utf-8")

cmake = ROOT / "host/CMakeLists.txt"
c = cmake.read_text(encoding="utf-8")
anchor = '''add_executable(audio_frontend_tests tests/audio_frontend.cpp)
target_link_libraries(audio_frontend_tests PRIVATE companion_app)
target_compile_options(audio_frontend_tests PRIVATE -Wall -Wextra -Wpedantic -Werror -UNDEBUG)
add_test(NAME audio_frontend_tests COMMAND audio_frontend_tests)
'''
extra = '''\nadd_executable(audio_frontend_app_tests tests/audio_frontend_app.cpp)\ntarget_link_libraries(audio_frontend_app_tests PRIVATE companion_app)\ntarget_compile_options(audio_frontend_app_tests PRIVATE -Wall -Wextra -Wpedantic -Werror -UNDEBUG)\nadd_test(NAME audio_frontend_app_tests COMMAND audio_frontend_app_tests)\n'''
if c.count(anchor) != 1:
    raise SystemExit("host CMake audio frontend anchor drifted")
cmake.write_text(c.replace(anchor, anchor + extra), encoding="utf-8")
print("issue17 app frontend integration patch applied")

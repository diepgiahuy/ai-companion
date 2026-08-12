#include "companion/app.hpp"

#include <cstdlib>

namespace companion {

namespace {
template <size_t N>
void copy_text(std::array<char, N>& destination, std::string_view source) {
  destination.fill('\0');
  const size_t count = std::min(source.size(), destination.size() - 1);
  std::copy_n(source.begin(), count, destination.begin());
}

template <size_t N>
std::string_view text_view(const std::array<char, N>& value) {
  const auto end = std::find(value.begin(), value.end(), '\0');
  return {value.data(), static_cast<size_t>(end - value.begin())};
}
} // namespace

CompanionApp::CompanionApp(Microphone& microphone, Speaker& speaker,
                           Display& display, Button& button,
                           VoiceBackend& backend, AppConfig config)
    : microphone_(microphone), speaker_(speaker), display_(display),
      button_(button), backend_(backend), config_(config) {}

bool CompanionApp::start(uint64_t now_ms) {
  state_ = UiState::connecting;
  display_.show(state_, "CONNECTING");
  if (!backend_.start(now_ms)) {
    fail("NETWORK ERROR");
    return false;
  }
  return true;
}

void CompanionApp::tick(uint64_t now_ms) {
  backend_.tick(now_ms);
  process_backend_events(now_ms);

  const bool pressed = button_.consume_press(now_ms);
  if (pressed) {
    if (state_ == UiState::ready || state_ == UiState::idle || state_ == UiState::alarm) {
      begin_listening(now_ms);
      return;
    }
    if (state_ == UiState::listening) {
      finish_listening(now_ms);
      return;
    }
    if (state_ == UiState::processing || state_ == UiState::speaking) {
      abort_and_listen(now_ms);
      return;
    }
    if (state_ == UiState::error) {
      state_ = UiState::connecting;
      display_.show(state_, "RECONNECTING");
      if (!backend_.start(now_ms)) fail("NETWORK ERROR");
      return;
    }
  }

  if (state_ == UiState::ready && now_ms - ready_since_ms_ >= config_.idle_after_ms) {
    state_ = UiState::idle;
    render_idle(now_ms);
  } else if (state_ == UiState::idle && now_ms - last_idle_render_ms_ >= 1'000) {
    render_idle(now_ms);
  } else if (state_ == UiState::alarm &&
             now_ms - alarm_started_ms_ >= config_.alarm_visible_ms) {
    speaker_.stop_playback();
    alarm_tone_active_ = false;
    enter_ready(now_ms);
  }

  if (state_ == UiState::listening) {
    if (now_ms - recording_started_ms_ >= config_.maximum_recording_ms) {
      finish_listening(now_ms);
      return;
    }
    pump_capture(now_ms);
  } else if (state_ == UiState::speaking) {
    pump_playback(now_ms);
  } else if (state_ == UiState::alarm) {
    pump_alarm_tone();
  }
}

void CompanionApp::process_backend_events(uint64_t now_ms) {
  BackendEvent event{};
  for (size_t handled = 0; handled < 8 && backend_.poll_event(event); ++handled) {
    process_backend_event(event, now_ms);
  }
}

void CompanionApp::process_backend_event(const BackendEvent& event,
                                          uint64_t now_ms) {
  switch (event.type) {
  case BackendEventType::connected:
    if (state_ == UiState::connecting || state_ == UiState::error) enter_ready(now_ms);
    break;
  case BackendEventType::disconnected:
    fail("DISCONNECTED");
    break;
  case BackendEventType::transcript:
    if (state_ == UiState::processing) display_.show(state_, event.text_view());
    break;
  case BackendEventType::tts_started:
    if (state_ != UiState::processing) break;
    playback_count_ = 0;
    playback_offset_ = 0;
    tts_finished_ = false;
    if (!speaker_.start_playback(backend_.playback_sample_rate_hz())) {
      fail("SPEAKER ERROR");
      break;
    }
    state_ = UiState::speaking;
    display_.show(state_, "SPEAKING");
    break;
  case BackendEventType::tts_sentence:
    if (state_ == UiState::speaking) display_.show(state_, event.text_view());
    break;
  case BackendEventType::tts_finished:
    tts_finished_ = true;
    break;
  case BackendEventType::alarm:
    if (state_ == UiState::ready || state_ == UiState::idle || state_ == UiState::alarm) {
      enter_alarm(now_ms, event.text_view());
    } else {
      set_pending_alarm(event.text_view());
      alarm_pending_ = true;
    }
    break;
  case BackendEventType::schedule:
    set_upcoming(event.text_view());
    if (state_ == UiState::idle) render_idle(now_ms);
    break;
  case BackendEventType::ui_card:
    display_.show(state_, event.text_view());
    break;
  case BackendEventType::config: {
    const auto& c = event.config;
    if (c.version <= runtime_config_version_) break; // shadow/twin versions are monotonic.
    const bool valid = c.vad_threshold >= 1 && c.vad_threshold <= 65'535 &&
                       c.vad_silence_ms >= 100 && c.vad_silence_ms <= 5'000 &&
                       c.vad_min_speech_ms >= 50 && c.vad_min_speech_ms <= 5'000 &&
                       c.idle_after_ms >= 1'000 && c.idle_after_ms <= 3'600'000 &&
                       c.alarm_visible_ms >= 1'000 && c.alarm_visible_ms <= 3'600'000;
    if (!valid) { backend_.report_config(c, false); break; }
    config_.smart_vad_enabled = c.smart_vad_enabled;
    config_.vad_mean_abs_threshold = static_cast<uint16_t>(c.vad_threshold);
    config_.vad_silence_ms = c.vad_silence_ms;
    config_.vad_min_speech_ms = c.vad_min_speech_ms;
    config_.idle_after_ms = c.idle_after_ms;
    config_.alarm_visible_ms = c.alarm_visible_ms;
    runtime_config_version_ = c.version;
    backend_.report_config(c, true);
    break;
  }
  case BackendEventType::error:
    fail(event.text_view().empty() ? "BACKEND ERROR" : event.text_view());
    break;
  }
}

void CompanionApp::pump_capture(uint64_t now_ms) {
  const size_t samples = microphone_.read_capture(capture_frame_);
  if (samples == 0) return;
  const auto frame = std::span<const int16_t>(capture_frame_.data(), samples);
  if (!backend_.send_audio(frame)) {
    microphone_.stop_capture();
    backend_.cancel_turn();
    fail("UPLOAD FULL");
    return;
  }
  streamed_samples_ += samples;

  if (!config_.smart_vad_enabled) return;
  if (frame_has_voice(frame)) {
    if (!speech_detected_) {
      speech_detected_ = true;
      first_voice_ms_ = now_ms;
    }
    last_voice_ms_ = now_ms;
    return;
  }
  if (speech_detected_ && now_ms - first_voice_ms_ >= config_.vad_min_speech_ms &&
      now_ms - last_voice_ms_ >= config_.vad_silence_ms) {
    finish_listening(now_ms);
  }
}

void CompanionApp::pump_playback(uint64_t now_ms) {
  if (playback_offset_ == playback_count_) {
    playback_count_ = backend_.read_playback(playback_frame_);
    playback_offset_ = 0;
  }
  if (playback_offset_ < playback_count_) {
    const auto pending = std::span<const int16_t>(playback_frame_.data() + playback_offset_,
                                                  playback_count_ - playback_offset_);
    playback_offset_ += speaker_.write_playback(pending);
  }
  const bool local_frame_empty = playback_offset_ == playback_count_;
  if (tts_finished_ && local_frame_empty && backend_.playback_empty() &&
      speaker_.playback_drained()) {
    speaker_.stop_playback();
    if (alarm_pending_) {
      alarm_pending_ = false;
      enter_alarm(now_ms, text_view(pending_alarm_));
    } else {
      enter_ready(now_ms);
    }
  }
}


void CompanionApp::pump_alarm_tone() {
  if (!alarm_tone_active_) return;

  const uint64_t total_samples =
      static_cast<uint64_t>(kAudioSampleRateHz) * config_.alarm_tone_ms / 1'000;
  if (playback_offset_ == playback_count_ && alarm_tone_generated_samples_ < total_samples) {
    const size_t count = static_cast<size_t>(std::min<uint64_t>(
        playback_frame_.size(), total_samples - alarm_tone_generated_samples_));
    const uint32_t period = std::max<uint32_t>(2, kAudioSampleRateHz /
                                                     std::max<uint16_t>(1, config_.alarm_tone_hz));
    for (size_t i = 0; i < count; ++i) {
      const uint64_t sample_index = alarm_tone_generated_samples_ + i;
      const uint32_t within_period = static_cast<uint32_t>(sample_index % period);
      const bool beep_window = ((sample_index * 1'000 / kAudioSampleRateHz) % 300) < 180;
      playback_frame_[i] = beep_window
                               ? (within_period < period / 2 ? config_.alarm_tone_amplitude
                                                            : -config_.alarm_tone_amplitude)
                               : 0;
    }
    playback_count_ = count;
    playback_offset_ = 0;
    alarm_tone_generated_samples_ += count;
  }

  if (playback_offset_ < playback_count_) {
    const auto pending = std::span<const int16_t>(playback_frame_.data() + playback_offset_,
                                                  playback_count_ - playback_offset_);
    playback_offset_ += speaker_.write_playback(pending);
  }

  if (alarm_tone_generated_samples_ >= total_samples &&
      playback_offset_ == playback_count_ && speaker_.playback_drained()) {
    speaker_.stop_playback();
    alarm_tone_active_ = false;
  }
}

void CompanionApp::begin_listening(uint64_t now_ms) {
  speaker_.stop_playback();
  alarm_tone_active_ = false;
  streamed_samples_ = 0;
  tts_finished_ = false;
  playback_count_ = playback_offset_ = 0;
  speech_detected_ = false;
  first_voice_ms_ = last_voice_ms_ = now_ms;
  const ListenMode mode = config_.smart_vad_enabled ? ListenMode::auto_vad
                                                     : ListenMode::manual;
  if (!backend_.begin_turn(now_ms, mode)) {
    fail("BACKEND ERROR");
    return;
  }
  if (!microphone_.start_capture()) {
    backend_.cancel_turn();
    fail("MIC ERROR");
    return;
  }
  recording_started_ms_ = now_ms;
  state_ = UiState::listening;
  display_.show(state_, config_.smart_vad_enabled ? "LISTENING VAD" : "LISTENING");
}

void CompanionApp::finish_listening(uint64_t now_ms) {
  if (state_ != UiState::listening) return;
  microphone_.stop_capture();
  if (streamed_samples_ == 0 || !backend_.finish_turn(now_ms)) {
    backend_.cancel_turn();
    fail(streamed_samples_ == 0 ? "NO AUDIO" : "BACKEND ERROR");
    return;
  }
  state_ = UiState::processing;
  display_.show(state_, "PROCESSING");
}

void CompanionApp::abort_and_listen(uint64_t now_ms) {
  speaker_.stop_playback();
  backend_.cancel_turn();
  begin_listening(now_ms);
}

void CompanionApp::enter_ready(uint64_t now_ms, std::string_view message) {
  if (alarm_pending_) {
    alarm_pending_ = false;
    enter_alarm(now_ms, text_view(pending_alarm_));
    return;
  }
  state_ = UiState::ready;
  ready_since_ms_ = now_ms;
  display_.show(state_, message);
}

void CompanionApp::enter_alarm(uint64_t now_ms, std::string_view message) {
  speaker_.stop_playback();
  playback_count_ = playback_offset_ = 0;
  alarm_tone_generated_samples_ = 0;
  alarm_tone_active_ = config_.alarm_tone_ms > 0 &&
                       speaker_.start_playback(kAudioSampleRateHz);
  state_ = UiState::alarm;
  alarm_started_ms_ = now_ms;
  display_.show(state_, message.empty() ? "ALARM" : message);
}

void CompanionApp::render_idle(uint64_t now_ms) {
  last_idle_render_ms_ = now_ms;
  display_.show(UiState::idle, text_view(upcoming_));
}

void CompanionApp::set_upcoming(std::string_view text) {
  copy_text(upcoming_, text);
}

void CompanionApp::set_pending_alarm(std::string_view text) {
  copy_text(pending_alarm_, text);
}

bool CompanionApp::frame_has_voice(std::span<const int16_t> pcm) const {
  if (pcm.empty()) return false;
  uint64_t sum = 0;
  for (const int16_t sample : pcm) {
    const int32_t value = sample;
    sum += static_cast<uint64_t>(value < 0 ? -value : value);
  }
  return sum / pcm.size() >= config_.vad_mean_abs_threshold;
}

void CompanionApp::fail(std::string_view reason) {
  microphone_.stop_capture();
  speaker_.stop_playback();
  backend_.cancel_turn();
  state_ = UiState::error;
  display_.show(state_, reason);
}

} // namespace companion

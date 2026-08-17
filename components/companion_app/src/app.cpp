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

void VoiceMailMetadata::set_voice_mail_id(std::string_view value) {
  copy_text(voice_mail_id, value);
}

void VoiceMailMetadata::set_from_device_id(std::string_view value) {
  copy_text(from_device_id, value);
}

void VoiceMailMetadata::set_media_format(std::string_view value) {
  copy_text(media_format, value);
}

void VoiceMailMetadata::set_checksum_sha256(std::string_view value) {
  copy_text(checksum_sha256, value);
}

std::string_view VoiceMailMetadata::voice_mail_id_view() const {
  return text_view(voice_mail_id);
}

bool VoiceMailMetadata::valid() const {
  const auto id = voice_mail_id_view();
  const auto sender = text_view(from_device_id);
  const auto format = text_view(media_format);
  const auto checksum = text_view(checksum_sha256);
  if (id.empty() || id.size() > 128 || sender.empty() || sender.size() > 128 ||
      format != "ogg_opus" || duration_ms == 0 || duration_ms > 600'000 ||
      size_bytes == 0 || size_bytes > 33'554'432 || checksum.size() != 64 ||
      expires_at_unix_ms == 0 ||
      (policy != Policy::ephemeral && policy != Policy::retained)) {
    return false;
  }
  for (const char value : checksum) {
    const bool hex = (value >= '0' && value <= '9') ||
                     (value >= 'a' && value <= 'f') ||
                     (value >= 'A' && value <= 'F');
    if (!hex) return false;
  }
  return true;
}

CompanionApp::CompanionApp(AudioEngine& audio, Display& display, Button& button,
                           VoiceBackend& backend, AppConfig config)
    : audio_(audio), display_(display), button_(button), backend_(backend),
      config_(config) {}

bool CompanionApp::start(uint64_t now_ms) {
  state_ = UiState::connecting;
  display_.show(state_, "CONNECTING");
  if (!audio_.start()) {
    fail("AUDIO ERROR");
    return false;
  }
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
    if (state_ == UiState::voice_mail_waiting) {
      begin_voice_mail(now_ms);
      return;
    }
    if (state_ == UiState::voice_mail_claiming ||
        state_ == UiState::voice_mail_playing) {
      fail_voice_mail(now_ms, "user_cancelled", "VOICE MAIL CANCELLED");
      return;
    }
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
    audio_.stop_playback();
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
    if (audio_.frontend_enabled() && state_ == UiState::speaking) {
      pump_frontend_monitor(now_ms);
    }
  } else if (state_ == UiState::voice_mail_playing) {
    pump_voice_mail(now_ms);
  } else if (state_ == UiState::alarm) {
    pump_alarm_tone();
  } else if (audio_.frontend_enabled() &&
             (state_ == UiState::ready || state_ == UiState::idle ||
              state_ == UiState::processing)) {
    pump_frontend_monitor(now_ms);
  }

  if (state_ == UiState::voice_mail_claiming &&
      now_ms - voice_mail_operation_started_ms_ >=
          config_.voice_mail_operation_timeout_ms) {
    fail_voice_mail(now_ms, "operation_timeout", "VOICE MAIL TIMEOUT");
  } else if (state_ == UiState::voice_mail_playing &&
             now_ms - voice_mail_last_progress_ms_ >=
                 config_.voice_mail_operation_timeout_ms) {
    fail_voice_mail(now_ms, "output_timeout", "VOICE MAIL TIMEOUT");
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
    if (state_ == UiState::voice_mail_claiming ||
        state_ == UiState::voice_mail_playing) {
      backend_.cancel_voice_mail(voice_mail_queue_[0], "disconnected", now_ms);
      voice_mail_stream_finished_ = false;
      voice_mail_result_pending_ = false;
    }
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
    if (!ensure_monitor_capture()) {
      fail("MIC ERROR");
      break;
    }
    if (!audio_.start_playback(backend_.playback_sample_rate_hz())) {
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
  case BackendEventType::presentation_card:
    (void)display_.show_card(state_, event.payload.card);
    break;
  case BackendEventType::presentation_hint:
    (void)display_.show_hint(state_, event.payload.hint);
    break;
  case BackendEventType::agent_status:
    (void)display_.show_agent_status(state_, event.payload.agent_status);
    break;
  case BackendEventType::config: {
    const auto& c = event.payload.config;
    if (c.version <= runtime_config_version_) break;
    const bool valid = c.vad_threshold >= 1 && c.vad_threshold <= 65'535 &&
                       c.vad_silence_ms >= 100 && c.vad_silence_ms <= 5'000 &&
                       c.vad_min_speech_ms >= 50 && c.vad_min_speech_ms <= 5'000 &&
                       c.idle_after_ms >= 1'000 && c.idle_after_ms <= 3'600'000 &&
                       c.alarm_visible_ms >= 1'000 && c.alarm_visible_ms <= 3'600'000 &&
                       c.ota_poll_interval_s >= 3'600 && c.ota_poll_interval_s <= 604'800;
    if (!valid) { backend_.report_config(c, false); break; }
    config_.smart_vad_enabled = c.smart_vad_enabled;
    config_.vad_mean_abs_threshold = static_cast<uint16_t>(c.vad_threshold);
    config_.vad_silence_ms = c.vad_silence_ms;
    config_.vad_min_speech_ms = c.vad_min_speech_ms;
    config_.idle_after_ms = c.idle_after_ms;
    config_.alarm_visible_ms = c.alarm_visible_ms;
    config_.ota_poll_interval_s = c.ota_poll_interval_s;
    runtime_config_version_ = c.version;
    backend_.report_config(c, true);
    break;
  }
  case BackendEventType::voice_mail_available:
    if (!event.payload.voice_mail.valid()) break;
    if (voice_mail_result_pending_ && current_voice_mail_matches(event.payload.voice_mail)) {
      voice_mail_result_pending_ = false;
      voice_mail_stream_finished_ = false;
      enter_voice_mail_waiting();
    } else if (enqueue_voice_mail(event.payload.voice_mail) &&
               (state_ == UiState::ready || state_ == UiState::idle)) {
      enter_voice_mail_waiting();
    }
    break;
  case BackendEventType::voice_mail_playback_ready:
    if (state_ != UiState::voice_mail_claiming || voice_mail_result_pending_ ||
        !current_voice_mail_matches(event.payload.voice_mail)) {
      break;
    }
    playback_count_ = playback_offset_ = 0;
    voice_mail_stream_finished_ = false;
    if (!audio_.start_playback(backend_.playback_sample_rate_hz())) {
      fail_voice_mail(now_ms, "speaker_start", "VOICE MAIL OUTPUT ERROR");
      break;
    }
    state_ = UiState::voice_mail_playing;
    voice_mail_last_progress_ms_ = now_ms;
    display_.show(state_, "PLAYING VOICE MAIL");
    break;
  case BackendEventType::voice_mail_playback_finished:
    if (current_voice_mail_matches(event.payload.voice_mail)) {
      voice_mail_stream_finished_ = true;
    }
    break;
  case BackendEventType::voice_mail_consumed:
    if (!current_voice_mail_matches(event.payload.voice_mail)) break;
    remove_voice_mail(event.payload.voice_mail.voice_mail_id_view());
    voice_mail_result_pending_ = false;
    voice_mail_stream_finished_ = false;
    enter_ready(now_ms);
    break;
  case BackendEventType::voice_mail_expired: {
    const bool current = current_voice_mail_matches(event.payload.voice_mail);
    if (!remove_voice_mail(event.payload.voice_mail.voice_mail_id_view())) break;
    if (current) {
      audio_.stop_playback();
      playback_count_ = playback_offset_ = 0;
      voice_mail_result_pending_ = false;
      voice_mail_stream_finished_ = false;
    }
    if (voice_mail_count_ > 0) {
      enter_voice_mail_waiting();
    } else if (current || state_ == UiState::voice_mail_waiting) {
      enter_ready(now_ms, "NO VOICE MAIL");
    }
    break;
  }
  case BackendEventType::voice_mail_failed:
    if (current_voice_mail_matches(event.payload.voice_mail)) {
      fail_voice_mail(now_ms, "backend_failure",
                      event.text_view().empty() ? "VOICE MAIL RETRY" : event.text_view());
    }
    break;
  case BackendEventType::error:
    if (state_ == UiState::voice_mail_claiming ||
        state_ == UiState::voice_mail_playing) {
      fail_voice_mail(now_ms, "backend_error",
                      event.text_view().empty() ? "VOICE MAIL RETRY" : event.text_view());
    } else {
      fail(event.text_view().empty() ? "BACKEND ERROR" : event.text_view());
    }
    break;
  }
}

void CompanionApp::pump_voice_mail(uint64_t now_ms) {
  if (playback_offset_ == playback_count_) {
    playback_count_ = backend_.read_playback(playback_frame_);
    playback_offset_ = 0;
    if (playback_count_ > playback_frame_.size()) {
      fail_voice_mail(now_ms, "invalid_frame", "VOICE MAIL DATA ERROR");
      return;
    }
  }
  if (playback_offset_ < playback_count_) {
    const auto pending = std::span<const int16_t>(
        playback_frame_.data() + playback_offset_, playback_count_ - playback_offset_);
    const size_t written = audio_.write_playback(pending);
    if (written > pending.size()) {
      fail_voice_mail(now_ms, "speaker_write", "VOICE MAIL OUTPUT ERROR");
      return;
    }
    playback_offset_ += written;
    if (written > 0) voice_mail_last_progress_ms_ = now_ms;
  }
  const bool local_empty = playback_offset_ == playback_count_;
  if (!voice_mail_result_pending_ && voice_mail_stream_finished_ && local_empty &&
      backend_.playback_empty() && audio_.playback_drained()) {
    audio_.stop_playback();
    voice_mail_result_pending_ = true;
    voice_mail_operation_started_ms_ = now_ms;
    state_ = UiState::voice_mail_claiming;
    display_.show(state_, "FINISHING VOICE MAIL");
    if (!backend_.report_voice_mail_playback(voice_mail_queue_[0], true, {}, now_ms)) {
      voice_mail_result_pending_ = false;
      fail_voice_mail(now_ms, "result_queue_full", "VOICE MAIL RETRY");
    }
  }
}

void CompanionApp::pump_capture(uint64_t now_ms) {
  const size_t samples = audio_.read_capture(capture_frame_);
  if (samples == 0) return;
  if (samples > capture_frame_.size()) {
    fail("MIC FRAME ERROR");
    return;
  }

  std::span<const int16_t> frame(capture_frame_.data(), samples);
  AudioFrontendEvent frontend_event = AudioFrontendEvent::none;
  if (audio_.frontend_enabled()) {
    const auto result = audio_.process_capture(
        frame, std::span<int16_t>(cleaned_capture_frame_.data(), cleaned_capture_frame_.size()));
    if (result.samples > cleaned_capture_frame_.size()) {
      fail("AUDIO FRONTEND ERROR");
      return;
    }
    frame = std::span<const int16_t>(cleaned_capture_frame_.data(), result.samples);
    frontend_event = result.event;
  }

  if (!frame.empty()) {
    if (!backend_.send_audio(frame)) {
      stop_capture_if_owned_by_turn();
      backend_.cancel_turn();
      fail("UPLOAD FULL");
      return;
    }
    streamed_samples_ += frame.size();
  }

  if (!config_.smart_vad_enabled) return;
  if (audio_.frontend_enabled()) {
    if (frontend_event == AudioFrontendEvent::speech_started) {
      if (!speech_detected_) {
        speech_detected_ = true;
        first_voice_ms_ = now_ms;
      }
      last_voice_ms_ = now_ms;
    } else if (frontend_event == AudioFrontendEvent::speech_ended && speech_detected_ &&
               now_ms - first_voice_ms_ >= config_.vad_min_speech_ms) {
      finish_listening(now_ms);
    }
    return;
  }

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

void CompanionApp::pump_frontend_monitor(uint64_t now_ms) {
  if (!audio_.frontend_enabled() || !ensure_monitor_capture()) return;
  const size_t samples = audio_.read_capture(capture_frame_);
  if (samples == 0) return;
  if (samples > capture_frame_.size()) {
    fail("MIC FRAME ERROR");
    return;
  }
  const auto result = audio_.process_capture(
      std::span<const int16_t>(capture_frame_.data(), samples),
      std::span<int16_t>(cleaned_capture_frame_.data(), cleaned_capture_frame_.size()));
  if (result.samples > cleaned_capture_frame_.size()) {
    fail("AUDIO FRONTEND ERROR");
    return;
  }
  handle_frontend_event(result.event, now_ms);
}

void CompanionApp::pump_playback(uint64_t now_ms) {
  if (playback_offset_ == playback_count_) {
    playback_count_ = backend_.read_playback(playback_frame_);
    playback_offset_ = 0;
    if (playback_count_ > playback_frame_.size()) {
      fail("PLAYBACK FRAME ERROR");
      return;
    }
  }
  if (playback_offset_ < playback_count_) {
    const auto pending = std::span<const int16_t>(playback_frame_.data() + playback_offset_,
                                                  playback_count_ - playback_offset_);
    const size_t written = audio_.write_playback(pending);
    if (written > pending.size()) {
      fail("SPEAKER ERROR");
      return;
    }
    if (written > 0 &&
        !audio_.push_playback_reference(pending.first(written),
                                        backend_.playback_sample_rate_hz())) {
      fail("AEC REFERENCE ERROR");
      return;
    }
    playback_offset_ += written;
  }
  const bool local_frame_empty = playback_offset_ == playback_count_;
  if (tts_finished_ && local_frame_empty && backend_.playback_empty() &&
      audio_.playback_drained()) {
    audio_.stop_playback();
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
    const size_t written = audio_.write_playback(pending);
    if (written > pending.size()) {
      fail("SPEAKER ERROR");
      return;
    }
    playback_offset_ += written;
  }

  if (alarm_tone_generated_samples_ >= total_samples &&
      playback_offset_ == playback_count_ && audio_.playback_drained()) {
    audio_.stop_playback();
    alarm_tone_active_ = false;
  }
}

void CompanionApp::begin_listening(uint64_t now_ms) {
  audio_.stop_playback();
  alarm_tone_active_ = false;
  streamed_samples_ = 0;
  tts_finished_ = false;
  playback_count_ = playback_offset_ = 0;
  speech_detected_ = false;
  first_voice_ms_ = last_voice_ms_ = now_ms;
  if (audio_.frontend_enabled()) audio_.reset();
  const ListenMode mode = config_.smart_vad_enabled ? ListenMode::auto_vad
                                                     : ListenMode::manual;
  if (!backend_.begin_turn(now_ms, mode)) {
    fail("BACKEND ERROR");
    return;
  }
  if (audio_.frontend_enabled()) {
    if (!ensure_monitor_capture()) {
      backend_.cancel_turn();
      fail("MIC ERROR");
      return;
    }
  } else {
    if (!audio_.start_capture()) {
      backend_.cancel_turn();
      fail("MIC ERROR");
      return;
    }
    capture_active_ = true;
  }
  recording_started_ms_ = now_ms;
  state_ = UiState::listening;
  display_.show(state_, config_.smart_vad_enabled ? "LISTENING VAD" : "LISTENING");
}

void CompanionApp::finish_listening(uint64_t now_ms) {
  if (state_ != UiState::listening) return;
  stop_capture_if_owned_by_turn();
  if (streamed_samples_ == 0 || !backend_.finish_turn(now_ms)) {
    backend_.cancel_turn();
    fail(streamed_samples_ == 0 ? "NO AUDIO" : "BACKEND ERROR");
    return;
  }
  state_ = UiState::processing;
  display_.show(state_, "PROCESSING");
}

void CompanionApp::abort_and_listen(uint64_t now_ms) {
  audio_.stop_playback();
  backend_.cancel_turn();
  begin_listening(now_ms);
}

void CompanionApp::enter_ready(uint64_t now_ms, std::string_view message) {
  if (alarm_pending_) {
    alarm_pending_ = false;
    enter_alarm(now_ms, text_view(pending_alarm_));
    return;
  }
  if (voice_mail_count_ > 0) {
    enter_voice_mail_waiting();
    return;
  }
  if (!ensure_monitor_capture()) {
    fail("MIC ERROR");
    return;
  }
  state_ = UiState::ready;
  ready_since_ms_ = now_ms;
  display_.show(state_, message);
}

void CompanionApp::enter_voice_mail_waiting() {
  state_ = UiState::voice_mail_waiting;
  display_.show(state_, "VOICE MAIL WAITING");
}

void CompanionApp::begin_voice_mail(uint64_t now_ms) {
  if (voice_mail_count_ == 0) {
    enter_ready(now_ms, "NO VOICE MAIL");
    return;
  }
  voice_mail_stream_finished_ = false;
  voice_mail_result_pending_ = false;
  voice_mail_operation_started_ms_ = now_ms;
  voice_mail_last_progress_ms_ = now_ms;
  playback_count_ = playback_offset_ = 0;
  if (!backend_.claim_voice_mail(voice_mail_queue_[0], now_ms)) {
    display_.show(UiState::voice_mail_waiting, "VOICE MAIL RETRY");
    return;
  }
  state_ = UiState::voice_mail_claiming;
  display_.show(state_, "LOADING VOICE MAIL");
}

void CompanionApp::fail_voice_mail(uint64_t now_ms, std::string_view code,
                                   std::string_view message) {
  audio_.stop_playback();
  playback_count_ = playback_offset_ = 0;
  voice_mail_stream_finished_ = false;
  voice_mail_result_pending_ = false;
  if (voice_mail_count_ > 0) {
    backend_.cancel_voice_mail(voice_mail_queue_[0], code, now_ms);
    state_ = UiState::voice_mail_waiting;
    display_.show(state_, message);
    return;
  }
  enter_ready(now_ms, message);
}

bool CompanionApp::enqueue_voice_mail(const VoiceMailMetadata& item) {
  const auto id = item.voice_mail_id_view();
  for (size_t index = 0; index < voice_mail_count_; ++index) {
    if (voice_mail_queue_[index].voice_mail_id_view() == id) return false;
  }
  if (voice_mail_count_ == voice_mail_queue_.size()) return false;
  voice_mail_queue_[voice_mail_count_++] = item;
  return true;
}

bool CompanionApp::remove_voice_mail(std::string_view voice_mail_id) {
  size_t found = voice_mail_count_;
  for (size_t index = 0; index < voice_mail_count_; ++index) {
    if (voice_mail_queue_[index].voice_mail_id_view() == voice_mail_id) {
      found = index;
      break;
    }
  }
  if (found == voice_mail_count_) return false;
  for (size_t index = found + 1; index < voice_mail_count_; ++index) {
    voice_mail_queue_[index - 1] = voice_mail_queue_[index];
  }
  voice_mail_queue_[--voice_mail_count_] = {};
  return true;
}

bool CompanionApp::current_voice_mail_matches(const VoiceMailMetadata& item) const {
  return voice_mail_count_ > 0 && !item.voice_mail_id_view().empty() &&
         voice_mail_queue_[0].voice_mail_id_view() == item.voice_mail_id_view();
}

void CompanionApp::enter_alarm(uint64_t now_ms, std::string_view message) {
  audio_.stop_playback();
  if (audio_.frontend_enabled() && capture_active_) {
    audio_.stop_capture();
    capture_active_ = false;
    audio_.reset();
  }
  playback_count_ = playback_offset_ = 0;
  alarm_tone_generated_samples_ = 0;
  alarm_tone_active_ = config_.alarm_tone_ms > 0 &&
                       audio_.start_playback(kAudioSampleRateHz);
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

bool CompanionApp::ensure_monitor_capture() {
  if (!audio_.frontend_enabled() || capture_active_) return true;
  if (!audio_.start_capture()) return false;
  capture_active_ = true;
  return true;
}

void CompanionApp::handle_frontend_event(AudioFrontendEvent event, uint64_t now_ms) {
  if (event == AudioFrontendEvent::wake_detected &&
      (state_ == UiState::ready || state_ == UiState::idle)) {
    begin_listening(now_ms);
    return;
  }
  if (event == AudioFrontendEvent::speech_started && state_ == UiState::speaking) {
    abort_and_listen(now_ms);
  }
}

void CompanionApp::stop_capture_if_owned_by_turn() {
  if (!audio_.frontend_enabled() && capture_active_) {
    audio_.stop_capture();
    capture_active_ = false;
  }
}

void CompanionApp::fail(std::string_view reason) {
  if (capture_active_) {
    audio_.stop_capture();
    capture_active_ = false;
  }
  audio_.reset();
  audio_.stop_playback();
  backend_.cancel_turn();
  state_ = UiState::error;
  display_.show(state_, reason);
}

} // namespace companion
#include "companion/mock_backend.hpp"

#include <algorithm>

namespace companion {

namespace {
constexpr std::array<int16_t, 32> kToneWave{
    0, 3196, 6270, 9102, 11585, 13622, 15136, 16069,
    16384, 16069, 15136, 13622, 11585, 9102, 6270, 3196,
    0, -3196, -6270, -9102, -11585, -13622, -15136, -16069,
    -16384, -16069, -15136, -13622, -11585, -9102, -6270, -3196};
}

MockVoiceBackend::MockVoiceBackend() {
  for (size_t i = 0; i < reply_pcm_.size(); ++i) {
    const int16_t envelope = i < 160 ? static_cast<int16_t>(i) :
                             i + 160 >= reply_pcm_.size() ?
                                 static_cast<int16_t>(reply_pcm_.size() - i) : 160;
    reply_pcm_[i] = static_cast<int16_t>((kToneWave[i % kToneWave.size()] * envelope) / 320);
  }
}

bool MockVoiceBackend::start(uint64_t) {
  if (!connected_) {
    connected_ = true;
    return push_event(BackendEventType::connected);
  }
  return true;
}

void MockVoiceBackend::tick(uint64_t now_ms) {
  if (!response_pending_ || now_ms < reply_at_ms_) return;
  response_pending_ = false;
  playback_offset_ = 0;
  push_event(BackendEventType::transcript, "MOCK TRANSCRIPT");
  push_event(BackendEventType::tts_started);
  push_event(BackendEventType::tts_sentence, "SAVED MOCK");
  push_event(BackendEventType::tts_finished);
}

bool MockVoiceBackend::begin_turn(uint64_t, ListenMode) {
  if (!connected_ || active_ || response_pending_) return false;
  active_ = true;
  received_samples_ = 0;
  playback_offset_ = reply_pcm_.size();
  return true;
}

bool MockVoiceBackend::send_audio(std::span<const int16_t> pcm) {
  if (!active_) return false;
  received_samples_ += pcm.size();
  return true;
}

bool MockVoiceBackend::finish_turn(uint64_t now_ms) {
  if (!active_ || received_samples_ == 0) return false;
  active_ = false;
  response_pending_ = true;
  reply_at_ms_ = now_ms + response_delay_ms_;
  return true;
}

void MockVoiceBackend::cancel_turn() {
  active_ = false;
  response_pending_ = false;
  playback_offset_ = reply_pcm_.size();
  clear_events();
}

bool MockVoiceBackend::report_config(const RuntimeConfigPatch& config, bool applied) {
  reported_config_version_ = config.version;
  reported_config_applied_ = applied;
  return true;
}

bool MockVoiceBackend::inject_config(const RuntimeConfigPatch& config) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = BackendEventType::config;
  event.config = config;
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::poll_event(BackendEvent& event) {
  if (event_count_ == 0) return false;
  event = events_[event_head_];
  event_head_ = (event_head_ + 1) % event_capacity_;
  --event_count_;
  return true;
}

size_t MockVoiceBackend::read_playback(std::span<int16_t> destination) {
  if (playback_offset_ >= reply_pcm_.size() || destination.empty()) return 0;
  const size_t count = std::min(destination.size(), reply_pcm_.size() - playback_offset_);
  std::copy_n(reply_pcm_.begin() + playback_offset_, count, destination.begin());
  playback_offset_ += count;
  return count;
}

bool MockVoiceBackend::playback_empty() const {
  return playback_offset_ >= reply_pcm_.size();
}

bool MockVoiceBackend::push_event(BackendEventType type, std::string_view text) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = type;
  event.set_text(text);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

void MockVoiceBackend::clear_events() {
  event_head_ = event_tail_ = event_count_ = 0;
}

} // namespace companion

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
    ++session_epoch_;
    ++media_generation_;
    return push_event(BackendEventType::connected, {}, BackendEventScope::session,
                      session_epoch_, 0);
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
  ++media_generation_;
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
  ++media_generation_;
}

void MockVoiceBackend::disconnect() {
  if (connected_) {
    connected_ = false;
    active_ = false;
    response_pending_ = false;
    playback_offset_ = reply_pcm_.size();
    ++session_epoch_;
    ++media_generation_;
    push_event(BackendEventType::disconnected, {}, BackendEventScope::session,
               session_epoch_, 0);
  }
}

bool MockVoiceBackend::claim_voice_mail(const VoiceMailMetadata& item, uint64_t) {
  if (!connected_ || !item.valid()) return false;
  ++voice_mail_claims_;
  playback_offset_ = 0;
  return inject_voice_mail(item, BackendEventType::voice_mail_playback_ready) &&
         inject_voice_mail(item, BackendEventType::voice_mail_playback_finished);
}

bool MockVoiceBackend::report_voice_mail_playback(const VoiceMailMetadata& item,
                                                  bool succeeded,
                                                  std::string_view,
                                                  uint64_t) {
  if (!item.valid()) return false;
  if (succeeded) {
    ++voice_mail_successes_;
    if (item.policy == VoiceMailMetadata::Policy::retained) {
      return inject_voice_mail(item, BackendEventType::voice_mail_available);
    }
    return inject_voice_mail(item, BackendEventType::voice_mail_consumed);
  }
  ++voice_mail_failures_;
  return true;
}

void MockVoiceBackend::cancel_voice_mail(const VoiceMailMetadata& item,
                                         std::string_view, uint64_t) {
  if (item.valid()) ++voice_mail_failures_;
  playback_offset_ = reply_pcm_.size();
  ++media_generation_;
}

bool MockVoiceBackend::inject_card(const PresentationCardV1& card,
                                   BackendEventScope scope,
                                   uint64_t session_epoch,
                                   uint64_t generation) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = BackendEventType::presentation_card;
  event.scope = scope;
  event.session_epoch = session_epoch == 0 ? session_epoch_ : session_epoch;
  event.generation = generation == 0 ? media_generation_ : generation;
  event.set_card(card);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::inject_hint(const PresentationHint& hint,
                                   BackendEventScope scope,
                                   uint64_t session_epoch,
                                   uint64_t generation) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = BackendEventType::presentation_hint;
  event.scope = scope;
  event.session_epoch = session_epoch == 0 ? session_epoch_ : session_epoch;
  event.generation = generation == 0 ? media_generation_ : generation;
  event.set_hint(hint);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::inject_agent_status(const AgentPresentationStatus& status,
                                           BackendEventScope scope,
                                           uint64_t session_epoch,
                                           uint64_t generation) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = BackendEventType::agent_status;
  event.scope = scope;
  event.session_epoch = session_epoch == 0 ? session_epoch_ : session_epoch;
  event.generation = generation == 0 ? media_generation_ : generation;
  event.set_agent_status(status);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::inject_scoped_event(BackendEventType type,
                                           std::string_view text,
                                           BackendEventScope scope,
                                           uint64_t session_epoch,
                                           uint64_t generation) {
  return push_event(type, text, scope, session_epoch, generation);
}

bool MockVoiceBackend::inject_settings(const SettingsTwin& settings) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = BackendEventType::settings;
  event.scope = BackendEventScope::session;
  event.session_epoch = session_epoch_;
  event.generation = 0;
  event.set_settings(settings);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::inject_voice_mail(const VoiceMailMetadata& item,
                                         BackendEventType type) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = type;
  event.scope = scope_for_event_type(type);
  event.session_epoch = session_epoch_;
  event.generation = media_generation_;
  event.set_voice_mail(item);
  events_[event_tail_] = event;
  event_tail_ = (event_tail_ + 1) % event_capacity_;
  ++event_count_;
  return true;
}

bool MockVoiceBackend::poll_event(BackendEvent& event) {
  while (event_count_ > 0) {
    event = events_[event_head_];
    event_head_ = (event_head_ + 1) % event_capacity_;
    --event_count_;
    if (event.scope == BackendEventScope::generation) {
      if (event.session_epoch != session_epoch_ ||
          event.generation != media_generation_) {
        continue;
      }
    } else if (event.scope == BackendEventScope::session) {
      if (event.type != BackendEventType::disconnected &&
          event.session_epoch != session_epoch_) {
        continue;
      }
    }
    return true;
  }
  return false;
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

bool MockVoiceBackend::push_event(BackendEventType type, std::string_view text,
                                  BackendEventScope scope,
                                  uint64_t session_epoch, uint64_t generation) {
  if (event_count_ == event_capacity_) return false;
  BackendEvent event{};
  event.type = type;
  event.scope = (scope == BackendEventScope::global &&
                 type != BackendEventType::alarm &&
                 type != BackendEventType::schedule &&
                 type != BackendEventType::voice_mail_available &&
                 type != BackendEventType::voice_mail_consumed &&
                 type != BackendEventType::voice_mail_expired)
                    ? scope_for_event_type(type)
                    : scope;
  event.session_epoch = session_epoch == 0 ? session_epoch_ : session_epoch;
  event.generation = generation == 0 ? media_generation_ : generation;
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

#pragma once

#include "companion/app.hpp"

#include <array>

namespace companion {

class MockVoiceBackend final : public VoiceBackend {
public:
  MockVoiceBackend();
  bool start(uint64_t now_ms) override;
  void tick(uint64_t now_ms) override;
  bool begin_turn(uint64_t now_ms, ListenMode mode) override;
  bool send_audio(std::span<const int16_t> pcm) override;
  bool finish_turn(uint64_t now_ms) override;
  void cancel_turn() override;
  bool poll_event(BackendEvent& event) override;
  bool claim_voice_mail(const VoiceMailMetadata& item, uint64_t now_ms) override;
  bool report_voice_mail_playback(const VoiceMailMetadata& item, bool succeeded,
                                  std::string_view failure_code,
                                  uint64_t now_ms) override;
  void cancel_voice_mail(const VoiceMailMetadata& item,
                         std::string_view failure_code,
                         uint64_t now_ms) override;
  size_t read_playback(std::span<int16_t> destination) override;
  bool playback_empty() const override;
  uint32_t playback_sample_rate_hz() const override { return kAudioSampleRateHz; }

  uint64_t session_epoch() const override { return session_epoch_; }
  uint64_t media_generation() const override { return media_generation_; }
  void set_session_epoch(uint64_t epoch) { session_epoch_ = epoch; }
  void set_media_generation(uint64_t gen) { media_generation_ = gen; }
  void disconnect();

  uint64_t received_samples() const { return received_samples_; }
  bool inject_event(BackendEventType type, std::string_view text = {}) { return push_event(type, text); }
  bool inject_card(const PresentationCardV1& card,
                   BackendEventScope scope = BackendEventScope::generation,
                   uint64_t session_epoch = 0, uint64_t generation = 0);
  bool inject_hint(const PresentationHint& hint,
                   BackendEventScope scope = BackendEventScope::generation,
                   uint64_t session_epoch = 0, uint64_t generation = 0);
  bool inject_agent_status(const AgentPresentationStatus& status,
                           BackendEventScope scope = BackendEventScope::generation,
                           uint64_t session_epoch = 0, uint64_t generation = 0);
  bool inject_scoped_event(BackendEventType type, std::string_view text = {},
                           BackendEventScope scope = BackendEventScope::generation,
                           uint64_t session_epoch = 0, uint64_t generation = 0);
  bool inject_settings(const SettingsTwin& settings);
  bool inject_voice_mail(const VoiceMailMetadata& item,
                         BackendEventType type = BackendEventType::voice_mail_available);
  uint32_t voice_mail_claims() const { return voice_mail_claims_; }
  uint32_t voice_mail_successes() const { return voice_mail_successes_; }
  uint32_t voice_mail_failures() const { return voice_mail_failures_; }

private:
  static constexpr uint32_t response_delay_ms_{250};
  static constexpr size_t reply_samples_{3'200}; // 200 ms confirmation tone.
  static constexpr size_t event_capacity_{8};
  bool connected_{};
  bool active_{};
  bool response_pending_{};
  uint64_t received_samples_{};
  uint64_t reply_at_ms_{};
  size_t playback_offset_{};
  uint64_t session_epoch_{1};
  uint64_t media_generation_{1};
  uint32_t voice_mail_claims_{};
  uint32_t voice_mail_successes_{};
  uint32_t voice_mail_failures_{};
  std::array<int16_t, reply_samples_> reply_pcm_{};
  std::array<BackendEvent, event_capacity_> events_{};
  size_t event_head_{};
  size_t event_tail_{};
  size_t event_count_{};

  bool push_event(BackendEventType type, std::string_view text = {},
                  BackendEventScope scope = BackendEventScope::global,
                  uint64_t session_epoch = 0, uint64_t generation = 0);
  void clear_events();
};

} // namespace companion

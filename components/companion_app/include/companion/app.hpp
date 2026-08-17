#pragma once

#include "companion/audio_frontend.hpp"
#include "companion/input_router.hpp"
#include "companion/presentation_ingress.hpp"
#include "companion/settings.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <new>
#include <span>
#include <string_view>
#include <type_traits>

namespace companion {

inline constexpr uint32_t kAudioSampleRateHz = 16'000;
inline constexpr size_t kAudioFrameSamples = 320; // 20 ms at 16 kHz.

enum class UiState : uint8_t {
  booting,
  connecting,
  ready,
  idle,
  listening,
  processing,
  speaking,
  voice_mail_waiting,
  voice_mail_claiming,
  voice_mail_playing,
  alarm,
  error,
};

enum class ListenMode : uint8_t {
  manual,
  auto_vad,
};

enum class BackendEventType : uint8_t {
  connected,
  disconnected,
  transcript,
  tts_started,
  tts_sentence,
  tts_finished,
  alarm,
  schedule,
  presentation_card,
  presentation_hint,
  agent_status,
  settings,
  voice_mail_available,
  voice_mail_playback_ready,
  voice_mail_playback_finished,
  voice_mail_consumed,
  voice_mail_expired,
  voice_mail_failed,
  error,
};

struct VoiceMailMetadata {
  enum class Policy : uint8_t { unknown, ephemeral, retained };
  std::array<char, 129> voice_mail_id{};
  std::array<char, 129> from_device_id{};
  std::array<char, 16> media_format{};
  std::array<char, 65> checksum_sha256{};
  uint32_t duration_ms{};
  uint32_t size_bytes{};
  uint64_t expires_at_unix_ms{};
  Policy policy{Policy::unknown};

  void set_voice_mail_id(std::string_view value);
  void set_from_device_id(std::string_view value);
  void set_media_format(std::string_view value);
  void set_checksum_sha256(std::string_view value);
  std::string_view voice_mail_id_view() const;
  bool valid() const;
};

enum class BackendEventScope : uint8_t {
  global,
  session,
  generation,
};

constexpr BackendEventScope scope_for_event_type(BackendEventType type) {
  switch (type) {
  case BackendEventType::connected:
  case BackendEventType::disconnected:
    return BackendEventScope::session;

  case BackendEventType::transcript:
  case BackendEventType::tts_started:
  case BackendEventType::tts_sentence:
  case BackendEventType::tts_finished:
  case BackendEventType::presentation_card:
  case BackendEventType::presentation_hint:
  case BackendEventType::agent_status:
  case BackendEventType::voice_mail_playback_ready:
  case BackendEventType::voice_mail_playback_finished:
  case BackendEventType::voice_mail_failed:
    return BackendEventScope::generation;

  case BackendEventType::alarm:
  case BackendEventType::schedule:
  case BackendEventType::settings:
  case BackendEventType::voice_mail_available:
  case BackendEventType::voice_mail_consumed:
  case BackendEventType::voice_mail_expired:
  case BackendEventType::error:
    return BackendEventScope::global;
  }
  return BackendEventScope::global;
}

// FreeRTOS queues byte-copy BackendEvent values. Keep the payload tagged by
// BackendEventType and union-backed so typed presentation data does not multiply
// the queue's static DRAM footprint. Every member is trivially copyable.
union BackendEventPayload {
  SettingsTwin settings;
  VoiceMailMetadata voice_mail;
  PresentationCardV1 card;
  PresentationHint hint;
  AgentPresentationStatus agent_status;

  constexpr BackendEventPayload() : settings{} {}
};

struct BackendEvent {
  BackendEventType type{BackendEventType::error};
  BackendEventScope scope{BackendEventScope::global};
  uint64_t session_epoch{};
  uint64_t generation{};
  std::array<char, 96> text{};
  BackendEventPayload payload{};

  void set_text(std::string_view value) {
    text.fill('\0');
    const size_t count = std::min(value.size(), text.size() - 1);
    std::copy_n(value.begin(), count, text.begin());
  }

  std::string_view text_view() const {
    const auto end = std::find(text.begin(), text.end(), '\0');
    return {text.data(), static_cast<size_t>(end - text.begin())};
  }

  void set_settings(const SettingsTwin& value) {
    new (&payload.settings) SettingsTwin(value);
  }
  void set_voice_mail(const VoiceMailMetadata& value) {
    new (&payload.voice_mail) VoiceMailMetadata(value);
  }
  void set_card(const PresentationCardV1& value) {
    new (&payload.card) PresentationCardV1(value);
  }
  void set_hint(const PresentationHint& value) {
    new (&payload.hint) PresentationHint(value);
  }
  void set_agent_status(const AgentPresentationStatus& value) {
    new (&payload.agent_status) AgentPresentationStatus(value);
  }
};

static_assert(std::is_trivially_copyable_v<DeviceSettings>);
static_assert(std::is_trivially_copyable_v<SettingsTwin>);
static_assert(std::is_trivially_copyable_v<VoiceMailMetadata>);
static_assert(std::is_trivially_copyable_v<PresentationCardV1>);
static_assert(std::is_trivially_copyable_v<PresentationHint>);
static_assert(std::is_trivially_copyable_v<AgentPresentationStatus>);
static_assert(std::is_trivially_copyable_v<BackendEvent>);

// Single app-facing audio owner. It hides physical microphone/speaker adapters,
// optional vendor AFE state, playback-reference epochs and rate conversion from
// CompanionApp so there is only one audio lifecycle boundary in the product.
class AudioEngine {
public:
  virtual ~AudioEngine() = default;
  virtual bool start() = 0;
  virtual void reset() = 0;
  virtual bool frontend_enabled() const = 0;
  virtual bool start_capture() = 0;
  virtual size_t read_capture(std::span<int16_t> destination) = 0;
  virtual void stop_capture() = 0;
  virtual bool start_playback(uint32_t sample_rate_hz) = 0;
  virtual size_t write_playback(std::span<const int16_t> mono_pcm) = 0;
  virtual bool playback_drained() const = 0;
  virtual void stop_playback() = 0;
  virtual bool push_playback_reference(std::span<const int16_t> accepted_pcm,
                                       uint32_t source_sample_rate_hz) = 0;
  virtual AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                              std::span<int16_t> cleaned_16k) = 0;
};

class Display {
public:
  virtual ~Display() = default;
  virtual void show(UiState state, std::string_view text) = 0;
  virtual bool show_card(UiState, const PresentationCardV1&) { return false; }
  virtual bool show_hint(UiState, const PresentationHint&) { return false; }
  virtual bool show_agent_status(UiState, const AgentPresentationStatus&) {
    return false;
  }
  virtual void set_context(uint64_t /*session_epoch*/, uint64_t /*generation*/) {}
};

class VoiceBackend {
public:
  virtual ~VoiceBackend() = default;
  virtual bool start(uint64_t now_ms) = 0;
  virtual void tick(uint64_t now_ms) = 0;
  virtual bool begin_turn(uint64_t now_ms, ListenMode mode) = 0;
  virtual bool send_audio(std::span<const int16_t> pcm) = 0;
  virtual bool finish_turn(uint64_t now_ms) = 0;
  virtual void cancel_turn() = 0;
  virtual bool poll_event(BackendEvent& event) = 0;
  // Called only after CompanionApp has actually accepted or rejected a settings
  // event. Network backends use this to complete the pending capability RPC;
  // simple test backends may use the default no-op acknowledgement.
  virtual bool report_settings_apply(const SettingsTwin&, bool) { return true; }
  virtual bool claim_voice_mail(const VoiceMailMetadata& item, uint64_t now_ms) = 0;
  virtual bool report_voice_mail_playback(const VoiceMailMetadata& item,
                                          bool succeeded,
                                          std::string_view failure_code,
                                          uint64_t now_ms) = 0;
  virtual void cancel_voice_mail(const VoiceMailMetadata& item,
                                 std::string_view failure_code,
                                 uint64_t now_ms) = 0;
  virtual size_t read_playback(std::span<int16_t> destination) = 0;
  virtual bool playback_empty() const = 0;
  virtual uint32_t playback_sample_rate_hz() const = 0;
  virtual uint64_t session_epoch() const { return 0; }
  virtual uint64_t media_generation() const { return 0; }
};

struct AppConfig {
  uint32_t maximum_recording_ms{8'000};
  uint32_t idle_after_ms{5'000};
  uint32_t alarm_visible_ms{10'000};
  uint32_t alarm_tone_ms{900};
  uint16_t alarm_tone_hz{880};
  int16_t alarm_tone_amplitude{3'500};
  bool smart_vad_enabled{true};
  uint16_t vad_mean_abs_threshold{450};
  uint32_t vad_silence_ms{800};
  uint32_t vad_min_speech_ms{250};
  uint32_t voice_mail_operation_timeout_ms{15'000};
  uint32_t ota_poll_interval_s{21'600};
  uint8_t volume{70};
  float wake_threshold{0.60F};
  std::array<char, 64> wake_model{"default"};
};

class CompanionApp final {
public:
  CompanionApp(AudioEngine& audio, Display& display, InputRouter& input,
               VoiceBackend& backend, AppConfig config = {});

  bool start(uint64_t now_ms);
  void tick(uint64_t now_ms);
  UiState state() const { return state_; }
  const AppConfig& config() const { return config_; }
  uint64_t streamed_samples() const { return streamed_samples_; }
  uint64_t runtime_config_version() const { return runtime_config_version_; }
  uint64_t settings_version() const { return runtime_config_version_; }
  bool apply_settings(const SettingsTwin& twin);

private:
  AudioEngine& audio_;
  Display& display_;
  InputRouter& input_;
  VoiceBackend& backend_;
  AppConfig config_;
  UiState state_{UiState::booting};
  uint64_t recording_started_ms_{};
  uint64_t streamed_samples_{};
  uint64_t ready_since_ms_{};
  uint64_t last_idle_render_ms_{};
  uint64_t alarm_started_ms_{};
  uint64_t alarm_tone_generated_samples_{};
  uint64_t runtime_config_version_{};
  uint64_t last_voice_ms_{};
  uint64_t first_voice_ms_{};
  uint64_t voice_mail_operation_started_ms_{};
  uint64_t voice_mail_last_progress_ms_{};
  bool speech_detected_{};
  bool tts_finished_{};
  bool alarm_pending_{};
  bool alarm_tone_active_{};
  bool voice_mail_stream_finished_{};
  bool voice_mail_result_pending_{};
  static constexpr size_t kVoiceMailQueueCapacity = 4;
  std::array<VoiceMailMetadata, kVoiceMailQueueCapacity> voice_mail_queue_{};
  size_t voice_mail_count_{};
  std::array<char, 96> upcoming_{};
  std::array<char, 96> pending_alarm_{};
  std::array<int16_t, kAudioFrameSamples> capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> cleaned_capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_frame_{};
  size_t playback_count_{};
  size_t playback_offset_{};

  void process_backend_events(uint64_t now_ms);
  void process_backend_event(const BackendEvent& event, uint64_t now_ms);
  void pump_capture(uint64_t now_ms);
  void pump_frontend_monitor(uint64_t now_ms);
  void pump_playback(uint64_t now_ms);
  void pump_voice_mail(uint64_t now_ms);
  void pump_alarm_tone();
  void begin_listening(uint64_t now_ms);
  void finish_listening(uint64_t now_ms);
  void abort_and_listen(uint64_t now_ms);
  void enter_ready(uint64_t now_ms, std::string_view message = "PRESS TO TALK");
  void enter_alarm(uint64_t now_ms, std::string_view message);
  void enter_voice_mail_waiting();
  void begin_voice_mail(uint64_t now_ms);
  void fail_voice_mail(uint64_t now_ms, std::string_view code,
                       std::string_view message);
  bool enqueue_voice_mail(const VoiceMailMetadata& item);
  bool remove_voice_mail(std::string_view voice_mail_id);
  bool current_voice_mail_matches(const VoiceMailMetadata& item) const;
  void render_idle(uint64_t now_ms);
  void set_upcoming(std::string_view text);
  void set_pending_alarm(std::string_view text);
  bool frame_has_voice(std::span<const int16_t> pcm) const;
  bool ensure_monitor_capture();
  void handle_frontend_event(AudioFrontendEvent event, uint64_t now_ms);
  void stop_capture_if_owned_by_turn();
  void fail(std::string_view reason);
};

} // namespace companion

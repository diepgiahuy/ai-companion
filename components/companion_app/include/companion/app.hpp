#pragma once

#include "companion/audio_frontend.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <span>
#include <string_view>

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
  alarm,
  error,
};

enum class ListenMode : uint8_t {
  manual,
  auto_vad,
};

struct RuntimeConfigPatch {
  uint64_t version{};
  bool smart_vad_enabled{true};
  uint32_t vad_threshold{450};
  uint32_t vad_silence_ms{800};
  uint32_t vad_min_speech_ms{250};
  uint32_t idle_after_ms{5'000};
  uint32_t alarm_visible_ms{10'000};
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
  ui_card,
  config,
  error,
};

struct BackendEvent {
  BackendEventType type{BackendEventType::error};
  std::array<char, 96> text{};
  RuntimeConfigPatch config{};

  void set_text(std::string_view value) {
    text.fill('\0');
    const size_t count = std::min(value.size(), text.size() - 1);
    std::copy_n(value.begin(), count, text.begin());
  }

  std::string_view text_view() const {
    const auto end = std::find(text.begin(), text.end(), '\0');
    return {text.data(), static_cast<size_t>(end - text.begin())};
  }
};

class Microphone {
public:
  virtual ~Microphone() = default;
  virtual bool start_capture() = 0;
  virtual size_t read_capture(std::span<int16_t> destination) = 0;
  virtual void stop_capture() = 0;
};

class Speaker {
public:
  virtual ~Speaker() = default;
  virtual bool start_playback(uint32_t sample_rate_hz) = 0;
  virtual size_t write_playback(std::span<const int16_t> mono_pcm) = 0;
  virtual bool playback_drained() const = 0;
  virtual void stop_playback() = 0;
};

class Display {
public:
  virtual ~Display() = default;
  virtual void show(UiState state, std::string_view text) = 0;
};

class Button {
public:
  virtual ~Button() = default;
  virtual bool consume_press(uint64_t now_ms) = 0;
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
  virtual bool report_config(const RuntimeConfigPatch& config, bool applied) = 0;
  virtual size_t read_playback(std::span<int16_t> destination) = 0;
  virtual bool playback_empty() const = 0;
  virtual uint32_t playback_sample_rate_hz() const = 0;
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
};

class CompanionApp final {
public:
  CompanionApp(Microphone& microphone, Speaker& speaker, Display& display,
               Button& button, VoiceBackend& backend, AppConfig config = {});
  CompanionApp(Microphone& microphone, Speaker& speaker, Display& display,
               Button& button, VoiceBackend& backend, AudioFrontend& audio_frontend,
               AppConfig config = {});

  bool start(uint64_t now_ms);
  void tick(uint64_t now_ms);
  UiState state() const { return state_; }
  uint64_t streamed_samples() const { return streamed_samples_; }
  uint64_t runtime_config_version() const { return runtime_config_version_; }

private:
  Microphone& microphone_;
  Speaker& speaker_;
  Display& display_;
  Button& button_;
  VoiceBackend& backend_;
  AudioFrontend* audio_frontend_{};
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
  bool speech_detected_{};
  bool tts_finished_{};
  bool alarm_pending_{};
  bool alarm_tone_active_{};
  std::array<char, 96> upcoming_{};
  std::array<char, 96> pending_alarm_{};
  std::array<int16_t, kAudioFrameSamples> capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> cleaned_capture_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_frame_{};
  std::array<int16_t, kAudioFrameSamples> playback_reference_frame_{};
  PlaybackReference24To16 playback_reference_converter_{};
  size_t playback_count_{};
  size_t playback_offset_{};

  void process_backend_events(uint64_t now_ms);
  void process_backend_event(const BackendEvent& event, uint64_t now_ms);
  void pump_capture(uint64_t now_ms);
  void pump_playback(uint64_t now_ms);
  void pump_alarm_tone();
  void begin_listening(uint64_t now_ms);
  void finish_listening(uint64_t now_ms);
  void abort_and_listen(uint64_t now_ms);
  void enter_ready(uint64_t now_ms, std::string_view message = "PRESS TO TALK");
  void enter_alarm(uint64_t now_ms, std::string_view message);
  void render_idle(uint64_t now_ms);
  void set_upcoming(std::string_view text);
  void set_pending_alarm(std::string_view text);
  void push_playback_reference(std::span<const int16_t> pcm,
                               uint32_t sample_rate_hz);
  bool frame_has_voice(std::span<const int16_t> pcm) const;
  void fail(std::string_view reason);
};

} // namespace companion

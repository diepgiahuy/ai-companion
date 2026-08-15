#pragma once

#include "companion/app.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <deque>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <thread>
#include <vector>

struct OpusEncoder;
struct OpusDecoder;

namespace companion::software_device {

class WebSocketVoiceBackend final : public VoiceBackend {
public:
  struct Stats {
    uint64_t connections{};
    uint64_t turns_started{};
    uint64_t cancels{};
    uint64_t stale_controls{};
    uint64_t discarded_binary_packets{};
    uint64_t config_reports{};
    uint64_t capability_advertisements{};
    uint64_t capability_calls{};
    uint64_t capability_results{};
    uint64_t capability_cancels{};
    int capability_volume{-1};
  };

  WebSocketVoiceBackend(std::string url, std::string token, std::string device_id);
  ~WebSocketVoiceBackend() override;

  WebSocketVoiceBackend(const WebSocketVoiceBackend&) = delete;
  WebSocketVoiceBackend& operator=(const WebSocketVoiceBackend&) = delete;

  bool start(uint64_t now_ms) override;
  void tick(uint64_t now_ms) override;
  bool begin_turn(uint64_t now_ms, ListenMode mode) override;
  bool send_audio(std::span<const int16_t> pcm) override;
  bool finish_turn(uint64_t now_ms) override;
  void cancel_turn() override;
  bool poll_event(BackendEvent& event) override;
  bool report_config(const RuntimeConfigPatch& config, bool applied) override;
  bool claim_voice_mail(const VoiceMailMetadata& item, uint64_t now_ms) override;
  bool report_voice_mail_playback(const VoiceMailMetadata& item, bool succeeded,
                                  std::string_view failure_code,
                                  uint64_t now_ms) override;
  void cancel_voice_mail(const VoiceMailMetadata& item,
                         std::string_view failure_code,
                         uint64_t now_ms) override;
  size_t read_playback(std::span<int16_t> destination) override;
  bool playback_empty() const override;
  uint32_t playback_sample_rate_hz() const override;

  bool resend_last_begin_for_test();
  void disconnect_for_test();
  Stats stats() const;
  std::string session_id() const;

private:
  class Connection;

  std::string url_;
  std::string token_;
  std::string device_id_;
  std::shared_ptr<Connection> connection_;
  std::thread io_thread_;
  std::atomic<uint64_t> connection_generation_{0};
  std::atomic<uint64_t> message_sequence_{0};
  std::atomic<uint64_t> turn_sequence_{0};
  std::atomic<bool> protocol_connected_{false};
  std::atomic<bool> stopping_{false};
  std::atomic<bool> media_worker_running_{false};

  mutable std::mutex state_mutex_;
  std::string session_id_;
  std::string active_turn_id_;
  std::string last_begin_wire_;
  bool turn_active_{};
  bool tts_active_{};
  uint32_t playback_sample_rate_{24'000};
  std::vector<int16_t> upload_samples_;
  std::deque<int16_t> playback_samples_;
  std::vector<int16_t> voice_mail_samples_;
  size_t voice_mail_sample_offset_{};
  std::deque<BackendEvent> events_;
  Stats stats_{};

  VoiceMailMetadata active_voice_mail_{};
  std::string voice_mail_playback_id_;
  std::string voice_mail_claim_wire_;
  std::string voice_mail_claim_idempotency_key_;
  std::string voice_mail_result_wire_;
  bool voice_mail_claim_active_{};
  bool voice_mail_media_started_{};
  bool voice_mail_result_sent_{};
  std::thread media_thread_;

  OpusEncoder* encoder_{};
  OpusDecoder* decoder_{};
  mutable std::mutex codec_mutex_;

  void stop_connection(bool notify);
  bool send_text(std::string message);
  bool send_binary(std::vector<uint8_t> packet);
  std::string encode_control(int type_index, std::string payload_json,
                             std::string turn_id = {},
                             std::string correlation_id = {},
                             bool include_session = true,
                             std::optional<uint64_t> generation_id = std::nullopt,
                             std::string idempotency_key = {},
                             std::string occurred_at = {});
  void enqueue_event(BackendEventType type, std::string_view text = {});
  void enqueue_voice_mail_event(BackendEventType type,
                                const VoiceMailMetadata& item,
                                std::string_view text = {});
  void handle_text(uint64_t generation, const std::string& text);
  void handle_binary(uint64_t generation, std::span<const uint8_t> packet);
  void handle_connection_open(uint64_t generation);
  void handle_connection_closed(uint64_t generation, std::string_view reason,
                                bool notify);
  bool flush_upload_frame(std::span<const int16_t> samples);
  void clear_turn_media_locked();
  void start_voice_mail_fetch(std::string media_ref, uint64_t generation,
                              VoiceMailMetadata item,
                              std::string playback_id);
  void finish_media_worker();
  void clear_voice_mail_locked();

  friend class Connection;
};

} // namespace companion::software_device

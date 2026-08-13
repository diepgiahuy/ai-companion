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

  mutable std::mutex state_mutex_;
  std::string session_id_;
  std::string active_turn_id_;
  std::string last_begin_wire_;
  bool turn_active_{};
  bool tts_active_{};
  uint32_t playback_sample_rate_{24'000};
  std::vector<int16_t> upload_samples_;
  std::deque<int16_t> playback_samples_;
  std::deque<BackendEvent> events_;
  Stats stats_{};

  OpusEncoder* encoder_{};
  OpusDecoder* decoder_{};
  mutable std::mutex codec_mutex_;

  void stop_connection(bool notify);
  bool send_text(std::string message);
  bool send_binary(std::vector<uint8_t> packet);
  std::string encode_control(int type_index, std::string payload_json,
                             std::string turn_id = {},
                             std::string correlation_id = {},
                             bool include_session = true);
  void enqueue_event(BackendEventType type, std::string_view text = {});
  void handle_text(uint64_t generation, const std::string& text);
  void handle_binary(uint64_t generation, std::span<const uint8_t> packet);
  void handle_connection_open(uint64_t generation);
  void handle_connection_closed(uint64_t generation, std::string_view reason,
                                bool notify);
  bool flush_upload_frame(std::span<const int16_t> samples);
  void clear_turn_media_locked();

  friend class Connection;
};

} // namespace companion::software_device

#pragma once

#include "companion/app.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <span>
#include <string_view>

#include "esp_event.h"
#include "esp_audio_enc.h"
#include "esp_audio_types.h"
#include "esp_opus_dec.h"
#include "esp_opus_enc.h"
#include "esp_websocket_client.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

namespace companion {

class WebSocketVoiceBackend final : public VoiceBackend {
public:
  WebSocketVoiceBackend();
  ~WebSocketVoiceBackend() override;
  WebSocketVoiceBackend(const WebSocketVoiceBackend&) = delete;
  WebSocketVoiceBackend& operator=(const WebSocketVoiceBackend&) = delete;

  bool initialize(std::string_view url, std::string_view token,
                  std::string_view device_id, std::string_view client_id);
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
  uint32_t playback_sample_rate_hz() const override {
    return playback_sample_rate_hz_.load();
  }

private:
  static constexpr size_t kOutboundQueueCapacity = 16;
  static constexpr size_t kPlaybackQueueCapacity = 24;
  static constexpr size_t kEventQueueCapacity = 12;
  static constexpr size_t kWriterStackDepth = 3'072;
  static constexpr size_t kOpusFrameSamples = 960; // 60 ms at 16 kHz.
  static constexpr size_t kMaximumOpusPacketBytes = 1'275;
  static constexpr size_t kMaximumDecodedSamples = 1'440; // 60 ms at 24 kHz.

  enum class CommandType : uint8_t { hello, listen_start, listen_stop, abort, alarm_ack, config_report };
  struct Command {
    CommandType type{};
    ListenMode mode{ListenMode::manual};
    std::array<char, 40> turn_id{};
    RuntimeConfigPatch config{};
    bool applied{};
  };
  struct AudioFrame {
    std::array<int16_t, kAudioFrameSamples> samples{};
    uint16_t count{};
  };
  struct OpusPacket {
    std::array<uint8_t, kMaximumOpusPacketBytes> bytes{};
    uint16_t count{};
  };
  enum class OutboundType : uint8_t { control, audio };
  struct Outbound {
    OutboundType type{};
    Command command{};
    OpusPacket audio{};
  };

  esp_websocket_client_handle_t client_{};
  std::atomic<bool> client_started_{false};
  std::atomic<bool> socket_connected_{false};
  std::atomic<bool> protocol_connected_{false};
  std::atomic<bool> turn_active_{false};
  std::atomic<bool> tts_active_{false};
  std::atomic<uint32_t> playback_sample_rate_hz_{24'000};
  uint64_t turn_sequence_{};

  std::array<char, 192> url_{};
  std::array<char, 256> headers_{};
  std::array<char, 40> device_id_{};
  std::array<char, 40> client_id_{};
  std::array<char, 40> active_turn_id_{};
  std::array<char, 768> text_payload_{};
  size_t text_payload_size_{};
  int receive_opcode_{};
  OpusPacket binary_payload_{};
  std::array<int16_t, kOpusFrameSamples> upload_payload_{};
  size_t upload_payload_size_{};
  void* opus_encoder_{};
  void* opus_decoder_{};
  int encoder_output_bytes_{};

  StaticQueue_t outbound_queue_storage_{};
  StaticQueue_t playback_queue_storage_{};
  StaticQueue_t event_queue_storage_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kOutboundQueueCapacity * sizeof(Outbound)> outbound_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kPlaybackQueueCapacity * sizeof(AudioFrame)> playback_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kEventQueueCapacity * sizeof(BackendEvent)> event_queue_buffer_{};
  QueueHandle_t outbound_queue_{};
  QueueHandle_t playback_queue_{};
  QueueHandle_t event_queue_{};

  StaticTask_t writer_task_storage_{};
  std::array<StackType_t, kWriterStackDepth> writer_stack_{};
  TaskHandle_t writer_task_{};

  static void event_handler(void* context, esp_event_base_t base,
                            int32_t event_id, void* event_data);
  static void writer_entry(void* context);
  void on_event(int32_t event_id, esp_websocket_event_data_t* data);
  void writer_loop();
  void handle_text(std::string_view json);
  void handle_binary(const esp_websocket_event_data_t& data);
  bool enqueue_command(CommandType type, std::string_view turn = {}, ListenMode mode = ListenMode::manual);
  bool encode_and_enqueue(std::span<const int16_t, kOpusFrameSamples> pcm);
  bool configure_decoder(uint32_t sample_rate_hz);
  bool decode_and_enqueue(const OpusPacket& packet);
  bool enqueue_audio(const OpusPacket& frame);
  bool enqueue_event(BackendEventType type, std::string_view text = {});
  bool enqueue_config_event(const RuntimeConfigPatch& config);
  bool send_text(std::string_view text);
  void reset_turn_queues();
};

} // namespace companion

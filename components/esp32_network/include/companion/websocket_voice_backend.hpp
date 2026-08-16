#pragma once

#include "companion/app.hpp"
#include "companion/wire_protocol.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <span>
#include <string_view>

#include "esp_event.h"
#include "esp_audio_enc.h"
#include "esp_audio_types.h"
#include "esp_http_client.h"
#include "esp_opus_dec.h"
#include "esp_opus_enc.h"
#include "esp_websocket_client.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

namespace companion {

enum class PairingBackendEventType : uint8_t {
  session_created,
  succeeded,
  rejected,
  expired,
  disconnected,
};

struct PairingBackendEvent {
  PairingBackendEventType type{PairingBackendEventType::expired};
  std::array<char, 129> pairing_session_id{};
  std::array<char, 257> confirmation_nonce{};
  std::array<char, 65> reason{};
  uint64_t expires_at_unix_ms{};

  std::string_view session_id_view() const;
  std::string_view confirmation_nonce_view() const;
  std::string_view reason_view() const;
};

struct UserConfirmationRequest {
  std::array<char, 129> correlation_id{};
  std::array<char, 129> turn_id{};
  std::array<char, 97> tool_name{};
  std::array<char, 193> prompt{};
  uint64_t generation_id{};
  uint32_t deadline_ms{};

  std::string_view correlation_id_view() const;
  std::string_view turn_id_view() const;
  std::string_view tool_name_view() const;
  std::string_view prompt_view() const;
  bool valid() const;
};

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
  bool claim_voice_mail(const VoiceMailMetadata& item, uint64_t now_ms) override;
  bool report_voice_mail_playback(const VoiceMailMetadata& item, bool succeeded,
                                  std::string_view failure_code,
                                  uint64_t now_ms) override;
  void cancel_voice_mail(const VoiceMailMetadata& item,
                         std::string_view failure_code,
                         uint64_t now_ms) override;
  size_t read_playback(std::span<int16_t> destination) override;
  bool playback_empty() const override;
  uint32_t playback_sample_rate_hz() const override {
    return playback_sample_rate_hz_.load();
  }

  bool enable_confirmation_protocol();
  bool advertise_user_confirmation();
  bool poll_user_confirmation(UserConfirmationRequest& request);
  bool user_confirmation_current(const UserConfirmationRequest& request);
  bool respond_user_confirmation(const UserConfirmationRequest& request,
                                 bool approved);

  bool enable_pairing_protocol();
  bool pairing_discovery_alias(std::array<char, 20>& output);
  bool create_pairing_session(std::string_view candidate_discovery_id,
                              std::string_view proximity_evidence_id);
  bool confirm_pairing_session(std::string_view pairing_session_id,
                               std::string_view confirmation_nonce);
  bool reject_pairing_session(std::string_view pairing_session_id);
  bool poll_pairing_event(PairingBackendEvent& event);

private:
  static constexpr size_t kOutboundQueueCapacity = 16;
  static constexpr size_t kPlaybackQueueCapacity = 24;
  static constexpr size_t kEventQueueCapacity = 12;
  static constexpr size_t kPairingEventQueueCapacity = 8;
  static constexpr size_t kMediaQueueCapacity = 2;
  static constexpr size_t kWriterStackDepth = 5'120;
  static constexpr size_t kMediaStackDepth = 6'144;
  static constexpr size_t kOpusFrameSamples = 960;
  static constexpr size_t kMaximumOpusPacketBytes = 1'275;
  static constexpr size_t kMaximumDecodedSamples = 1'440;
  static constexpr size_t kConfirmationControlBytes = 2'049;

  enum class CommandType : uint8_t {
    hello,
    session_pong,
    listen_start,
    listen_stop,
    abort,
    alarm_ack,
    config_report,
    protocol_error,
    voice_mail_claim,
    voice_mail_playback_result,
  };
  struct Command {
    CommandType type{};
    ListenMode mode{ListenMode::manual};
    std::array<char, 40> turn_id{};
    std::array<char, 48> correlation_id{};
    std::array<char, 40> code{};
    std::array<char, 96> message{};
    VoiceMailMetadata voice_mail{};
    std::array<char, 129> playback_id{};
    std::array<char, 129> idempotency_key{};
    std::array<char, 36> occurred_at{};
    RuntimeConfigPatch config{};
    bool applied{};
    bool succeeded{};
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
    uint64_t media_generation{};
  };
  struct MediaJob {
    VoiceMailMetadata voice_mail{};
    std::array<char, 129> playback_id{};
    std::array<char, 257> media_ref{};
    uint64_t generation{};
  };

  esp_websocket_client_handle_t client_{};
  std::atomic<bool> client_started_{false};
  std::atomic<bool> socket_connected_{false};
  std::atomic<bool> protocol_connected_{false};
  std::atomic<bool> pairing_protocol_enabled_{false};
  std::atomic<bool> confirmation_protocol_enabled_{false};
  std::atomic<bool> confirmation_advertised_{false};
  std::atomic<bool> turn_active_{false};
  std::atomic<bool> tts_active_{false};
  std::atomic<uint32_t> playback_sample_rate_hz_{24'000};
  std::atomic<uint64_t> media_generation_{};
  uint64_t turn_sequence_{};
  std::atomic<uint64_t> message_sequence_{};
  std::atomic<uint64_t> voice_mail_generation_{};

  std::array<char, 192> url_{};
  std::array<char, 256> headers_{};
  std::array<char, 192> token_{};
  std::array<char, 320> http_origin_{};
  std::array<char, 40> device_id_{};
  std::array<char, 40> client_id_{};
  std::array<char, 64> session_id_{};
  portMUX_TYPE session_id_lock_ = portMUX_INITIALIZER_UNLOCKED;
  std::array<char, 40> active_turn_id_{};
  portMUX_TYPE turn_id_lock_ = portMUX_INITIALIZER_UNLOCKED;
  portMUX_TYPE confirmation_lock_ = portMUX_INITIALIZER_UNLOCKED;
  UserConfirmationRequest active_confirmation_{};
  bool confirmation_ready_{};
  bool confirmation_active_{};
  std::array<char, kConfirmationControlBytes> confirmation_text_payload_{};
  size_t confirmation_text_payload_size_{};
  int confirmation_receive_opcode_{};
  std::array<char, 8'193> text_payload_{};
  size_t text_payload_size_{};
  int receive_opcode_{};
  OpusPacket binary_payload_{};
  std::array<int16_t, kOpusFrameSamples> upload_payload_{};
  size_t upload_payload_size_{};
  portMUX_TYPE media_buffer_lock_ = portMUX_INITIALIZER_UNLOCKED;
  portMUX_TYPE voice_mail_lock_ = portMUX_INITIALIZER_UNLOCKED;
  VoiceMailMetadata active_voice_mail_{};
  std::array<char, 129> active_playback_id_{};
  std::array<char, 129> active_claim_key_{};
  std::array<char, 129> active_result_key_{};
  bool voice_mail_claim_pending_{};
  bool voice_mail_result_pending_{};
  void* opus_encoder_{};
  void* opus_decoder_{};
  int encoder_output_bytes_{};

  StaticQueue_t outbound_queue_storage_{};
  StaticQueue_t playback_queue_storage_{};
  StaticQueue_t event_queue_storage_{};
  StaticQueue_t pairing_event_queue_storage_{};
  StaticQueue_t media_queue_storage_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kOutboundQueueCapacity * sizeof(Outbound)> outbound_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kPlaybackQueueCapacity * sizeof(AudioFrame)> playback_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kEventQueueCapacity * sizeof(BackendEvent)> event_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kPairingEventQueueCapacity * sizeof(PairingBackendEvent)> pairing_event_queue_buffer_{};
  alignas(portBYTE_ALIGNMENT) std::array<uint8_t, kMediaQueueCapacity * sizeof(MediaJob)> media_queue_buffer_{};
  QueueHandle_t outbound_queue_{};
  QueueHandle_t playback_queue_{};
  QueueHandle_t event_queue_{};
  QueueHandle_t pairing_event_queue_{};
  QueueHandle_t media_queue_{};

  StaticTask_t writer_task_storage_{};
  std::array<StackType_t, kWriterStackDepth> writer_stack_{};
  TaskHandle_t writer_task_{};
  StaticTask_t media_task_storage_{};
  std::array<StackType_t, kMediaStackDepth> media_stack_{};
  TaskHandle_t media_task_{};

  static void event_handler(void* context, esp_event_base_t base,
                            int32_t event_id, void* event_data);
  static void pairing_event_handler(void* context, esp_event_base_t base,
                                    int32_t event_id, void* event_data);
  static void confirmation_event_handler(void* context, esp_event_base_t base,
                                         int32_t event_id, void* event_data);
  static void writer_entry(void* context);
  static void media_entry(void* context);
  void on_event(int32_t event_id, esp_websocket_event_data_t* data);
  void on_pairing_event(int32_t event_id, esp_websocket_event_data_t* data);
  void on_confirmation_event(int32_t event_id, esp_websocket_event_data_t* data);
  void writer_loop();
  void media_loop();
  void handle_text(std::string_view json);
  bool handle_confirmation_text(std::string_view json);
  bool handle_pairing_text(std::string_view json);
  void handle_binary(const esp_websocket_event_data_t& data);
  bool enqueue_command(CommandType type, std::string_view turn = {}, ListenMode mode = ListenMode::manual);
  bool enqueue_pong(std::string_view correlation_id);
  bool enqueue_protocol_error(std::string_view code, std::string_view message);
  bool enqueue_confirmation_result(const UserConfirmationRequest& request,
                                   bool approved);
  void clear_user_confirmation();
  bool encode_and_enqueue(std::span<const int16_t, kOpusFrameSamples> pcm,
                          uint64_t media_generation);
  bool configure_decoder(uint32_t sample_rate_hz);
  bool decode_and_enqueue(const OpusPacket& packet, uint64_t media_generation);
  bool enqueue_audio(const OpusPacket& frame, uint64_t media_generation);
  bool enqueue_event(BackendEventType type, std::string_view text = {});
  bool enqueue_config_event(const RuntimeConfigPatch& config);
  bool enqueue_voice_mail_event(BackendEventType type,
                                const VoiceMailMetadata& item,
                                std::string_view text = {});
  bool enqueue_pairing_event(const PairingBackendEvent& event);
  bool send_pairing_control(protocol::ControlType type, std::string_view payload_json);
  bool send_text(std::string_view text);
  bool set_session_id(std::string_view session_id);
  void clear_session_id();
  std::array<char, 64> session_id_snapshot();
  std::array<char, 40> active_turn_id_snapshot();
  bool active_turn_matches(std::string_view turn_id);
  bool activate_tts_for_matching_turn(std::string_view turn_id);
  bool deactivate_matching_turn(std::string_view turn_id);
  void reset_turn_queues();
  bool build_http_origin(std::string_view websocket_url);
  bool enqueue_media_job(const VoiceMailMetadata& item,
                         std::string_view playback_id,
                         std::string_view media_ref);
  bool download_voice_mail(const MediaJob& job, bool decode);
  bool decode_voice_mail_packet(const MediaJob& job,
                                std::span<const uint8_t> packet,
                                uint64_t& decoded_samples, bool& ready_sent);
  bool voice_mail_job_current(const MediaJob& job) const;
  void clear_voice_mail(bool reset_playback);
};

} // namespace companion

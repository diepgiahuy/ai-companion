#pragma once

#include <array>
#include <atomic>
#include <cstdint>
#include <string_view>

#include "freertos/FreeRTOS.h"

struct ble_gap_event;

namespace companion::pairing {

struct DiscoveryCandidate {
  std::array<char, 33> discovery_id{};
  int8_t rssi{-127};
  uint32_t sample_count{};

  std::string_view id() const {
    return {discovery_id.data(), 32};
  }
};

// Radio-only adapter. It knows only the opaque active WebSocket session ID.
// It never receives owner/device IDs, credentials, relationship IDs or backend
// authorization. Pairing authority remains in the authenticated server flow.
class NimblePairingDiscovery final {
public:
  bool init();
  bool ready() const { return ready_.load(); }

  // Starts non-connectable advertising plus passive discovery. discovery_id must
  // be the current 32-hex-character server session_id. Advertising stores it as
  // 16 raw bytes behind the local ACP1 marker, keeping the legacy ADV <31 bytes.
  bool start(std::string_view discovery_id);
  void stop();
  bool active() const { return active_.load(); }

  // One bounded best candidate is retained; scan traffic cannot grow memory.
  bool best_candidate(DiscoveryCandidate& out) const;
  void clear_candidate();

private:
  static void host_task(void* arg);
  static void on_sync();
  static void on_reset(int reason);
  static int gap_event(ble_gap_event* event, void* arg);

  bool start_radio();
  void observe(const uint8_t* payload, uint8_t length, int8_t rssi);

  mutable portMUX_TYPE mux_ = portMUX_INITIALIZER_UNLOCKED;
  DiscoveryCandidate best_{};
  std::array<uint8_t, 20> advertised_name_{};
  std::atomic<bool> initialized_{false};
  std::atomic<bool> ready_{false};
  std::atomic<bool> active_{false};
  uint8_t own_addr_type_{};
};

} // namespace companion::pairing

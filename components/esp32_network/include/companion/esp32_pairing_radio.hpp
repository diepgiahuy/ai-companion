#pragma once

#include "companion/pairing_fsm.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"

#include <array>
#include <atomic>
#include <cstdint>
#include <string_view>

namespace companion {

bool rotating_pairing_discovery_id(std::string_view device_credential,
                                   int64_t unix_seconds,
                                   std::array<char, pairing::kDiscoveryIDCapacity>& output);

class Esp32PairingRadio final {
public:
  bool initialize();
  bool start(std::string_view discovery_id, uint32_t duration_ms);
  void stop();
  bool poll(pairing::Observation& observation);
  bool active() const { return active_.load(); }
  uint32_t dropped_observations() const { return dropped_.load(); }

private:
  static void host_task(void* parameter);
  static void on_sync();
  static int gap_event(struct ble_gap_event* event, void* argument);
  void handle_discovery(const struct ble_gap_disc_desc& report);

  static Esp32PairingRadio* instance_;
  StaticQueue_t queue_storage_{};
  std::array<uint8_t, pairing::kMaximumObservations * sizeof(pairing::Observation)> queue_bytes_{};
  QueueHandle_t queue_{};
  std::atomic<bool> initialized_{false};
  std::atomic<bool> synced_{false};
  std::atomic<bool> active_{false};
  std::atomic<uint32_t> dropped_{0};
  uint8_t own_address_type_{};
};

} // namespace companion

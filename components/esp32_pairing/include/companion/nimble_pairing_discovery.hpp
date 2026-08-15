#pragma once

#include <array>
#include <atomic>
#include <cstdint>
#include <cstring>
#include <string_view>

#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "host/ble_gap.h"

namespace companion::pairing {

constexpr size_t kDiscoveryAliasLength = 19;
constexpr size_t kDiscoveryQueueCapacity = 8;

inline bool valid_discovery_alias(std::string_view value) {
  if (value.size() != kDiscoveryAliasLength || !value.starts_with("CP-")) return false;
  for (const char c : value.substr(3)) {
    if ((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) continue;
    return false;
  }
  return true;
}

struct DiscoveryObservation {
  std::array<char, kDiscoveryAliasLength + 1> discovery_id{};
  int8_t rssi{-127};
  uint64_t seen_at_ms{};

  std::string_view id() const {
    return {discovery_id.data(), std::strlen(discovery_id.data())};
  }
};

// Radio-only adapter. It sees only short-lived opaque aliases. Stable
// owner/device IDs, credentials, relationship IDs and backend authorization do
// not enter this component. RSSI is retained as evidence only; #100 owns
// calibrated ranking/threshold policy.
class NimblePairingDiscovery final {
public:
  bool init();
  bool ready() const { return ready_.load(); }

  // Non-connectable advertising + passive scanning. The window is hard-bounded
  // to one minute by the public contract and every observation queue is fixed.
  bool start(std::string_view local_alias, uint32_t duration_ms);
  void stop();
  bool active() const { return active_.load(); }
  bool poll(DiscoveryObservation& observation);
  uint32_t dropped_observations() const { return dropped_.load(); }

private:
  static void host_task(void* arg);
  static void on_sync();
  static void on_reset(int reason);
  static int gap_event(ble_gap_event* event, void* arg);
  void observe(const ble_gap_disc_desc& report);

  static NimblePairingDiscovery* instance_;
  StaticQueue_t queue_storage_{};
  alignas(portBYTE_ALIGNMENT)
      std::array<uint8_t, kDiscoveryQueueCapacity * sizeof(DiscoveryObservation)> queue_buffer_{};
  QueueHandle_t queue_{};
  std::array<char, kDiscoveryAliasLength + 1> local_alias_{};
  std::atomic<bool> initialized_{false};
  std::atomic<bool> ready_{false};
  std::atomic<bool> active_{false};
  std::atomic<uint32_t> dropped_{0};
  uint8_t own_addr_type_{};
};

} // namespace companion::pairing

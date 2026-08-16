#pragma once

#include "companion/nimble_pairing_discovery.hpp"

#include "esp_log.h"

#include <cinttypes>

namespace companion::pairing::hil {

// HIL-only formatter for raw radio observations. Call this from a trusted
// two-DUT capture build after poll(); production pairing policy must not depend
// on the log stream. The output format is consumed by pairing_rssi_capture.py.
inline void log_observation(const DiscoveryObservation& observation) {
  const std::string_view alias = observation.id();
  if (!valid_discovery_alias(alias)) return;
  ESP_LOGI("pairing_rssi",
           "PAIRING_RSSI alias=%.*s rssi=%d seen_ms=%" PRIu64,
           static_cast<int>(alias.size()), alias.data(),
           static_cast<int>(observation.rssi), observation.seen_at_ms);
}

} // namespace companion::pairing::hil

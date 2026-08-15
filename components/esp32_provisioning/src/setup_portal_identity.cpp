#include "companion/setup_portal.hpp"

#include "esp_mac.h"

#include <array>
#include <cstdio>

namespace companion::provisioning {

bool SetupPortal::start(std::string_view device_suffix) {
  std::array<uint8_t, 6> mac{};
  if (esp_read_mac(mac.data(), ESP_MAC_WIFI_STA) != ESP_OK) return false;
  std::array<char, 32> device_id{};
  std::snprintf(device_id.data(), device_id.size(), "%02x:%02x:%02x:%02x:%02x:%02x",
                mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
  return start(device_id.data(), device_suffix);
}

} // namespace companion::provisioning

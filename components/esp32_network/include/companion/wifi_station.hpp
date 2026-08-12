#pragma once

#include <cstdint>
#include <string_view>

namespace companion {

class WifiStation final {
public:
  bool connect(std::string_view ssid, std::string_view password,
               uint32_t timeout_ms = 15'000);
  bool start_time_sync(std::string_view timezone_rule = "ICT-7");
};

} // namespace companion

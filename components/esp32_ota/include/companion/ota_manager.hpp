#pragma once

#include <cstdint>
#include <string_view>

namespace companion {

class WifiStation;

class OtaManager final {
public:
  explicit OtaManager(WifiStation& wifi) : wifi_(wifi) {}

  bool initialize(std::string_view server_url, std::string_view token,
                  std::string_view device_id, std::string_view board,
                  std::string_view channel, uint32_t health_timeout_ms);

  // Checks once during a healthy boot before interactive turns begin. A verified
  // update selects the inactive partition and reboots into PENDING_VERIFY.
  bool check_and_apply();

  // Pending images are marked valid only after Wi-Fi, valid wall clock and an
  // authenticated backend request all succeed. Otherwise the image rolls back.
  void tick(uint64_t now_ms);

private:
  bool backend_auth_reachable();
  std::string target_url() const;

  WifiStation& wifi_;
  std::string_view server_url_{};
  std::string_view token_{};
  std::string_view device_id_{};
  std::string_view board_{};
  std::string_view channel_{};
  uint64_t health_deadline_ms_{};
  uint64_t next_health_probe_ms_{};
  bool pending_verify_{};
  bool enabled_{};
};

} // namespace companion

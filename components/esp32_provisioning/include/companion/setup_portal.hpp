#pragma once

#include "companion/provisioning_store.hpp"

#include "esp_http_server.h"
#include "esp_netif.h"

#include <array>
#include <atomic>
#include <string_view>

namespace companion::provisioning {

class SetupPortal final {
public:
  // Product call surface remains device-suffix only; the provisioning component
  // derives the exact stable Wi-Fi MAC device ID internally for the owner binding.
  bool start(std::string_view device_suffix);
  bool start(std::string_view device_id, std::string_view device_suffix);
  bool start_wifi_only(std::string_view device_suffix);
  bool configured() const { return configured_.load(); }
  bool take_pending(PendingConfig& out);
  bool take_wifi(WifiConfig& out);

  std::string_view ssid() const;
  std::string_view password() const;

private:
  bool start_impl(std::string_view device_id, std::string_view device_suffix, bool wifi_only);
  static esp_err_t handle_index(httpd_req_t* request);
  static esp_err_t handle_nonce(httpd_req_t* request);
  static esp_err_t handle_setup_info(httpd_req_t* request);
  static esp_err_t handle_configure(httpd_req_t* request);
  esp_err_t configure(httpd_req_t* request);

  httpd_handle_t server_{};
  PendingConfig pending_{};
  WifiConfig wifi_{};
  std::array<char, 129> device_id_{};
  std::array<char, 33> ssid_{};
  std::array<char, 17> password_{};
  std::array<char, 33> session_nonce_{};
  bool wifi_only_{};
  std::atomic<bool> configured_{false};
};

} // namespace companion::provisioning

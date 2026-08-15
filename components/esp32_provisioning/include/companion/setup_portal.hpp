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
  bool start(std::string_view device_suffix);
  bool configured() const { return configured_.load(); }
  bool take_pending(PendingConfig& out);

  std::string_view ssid() const;
  std::string_view password() const;

private:
  static esp_err_t handle_index(httpd_req_t* request);
  static esp_err_t handle_configure(httpd_req_t* request);
  esp_err_t configure(httpd_req_t* request);

  httpd_handle_t server_{};
  PendingConfig pending_{};
  std::array<char, 33> ssid_{};
  std::array<char, 17> password_{};
  std::atomic<bool> configured_{false};
};

} // namespace companion::provisioning

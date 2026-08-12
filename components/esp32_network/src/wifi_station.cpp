#include "companion/wifi_station.hpp"

#include <algorithm>
#include <array>
#include <cstdlib>
#include <cstring>
#include <ctime>

#include "esp_event.h"
#include "esp_netif.h"
#include "esp_sntp.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"

namespace companion {
namespace {
constexpr EventBits_t kConnected = BIT0;
constexpr EventBits_t kFailed = BIT1;
constexpr int kMaximumRetries = 5;
EventGroupHandle_t event_group{};
int retry_count{};

void wifi_event(void*, esp_event_base_t base, int32_t id, void*) {
  if (base == WIFI_EVENT && id == WIFI_EVENT_STA_START) {
    esp_wifi_connect();
  } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_DISCONNECTED) {
    if (retry_count++ < kMaximumRetries) {
      esp_wifi_connect();
    } else {
      xEventGroupSetBits(event_group, kFailed);
    }
  }
}

void ip_event(void*, esp_event_base_t base, int32_t id, void*) {
  if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
    retry_count = 0;
    xEventGroupSetBits(event_group, kConnected);
  }
}
} // namespace

bool WifiStation::connect(std::string_view ssid, std::string_view password,
                          uint32_t timeout_ms) {
  wifi_config_t config{};
  if (ssid.empty() || ssid.size() >= sizeof(config.sta.ssid) ||
      password.size() >= sizeof(config.sta.password)) {
    return false;
  }

  if (esp_netif_init() != ESP_OK) return false;
  if (esp_event_loop_create_default() != ESP_OK) return false;
  if (esp_netif_create_default_wifi_sta() == nullptr) return false;

  wifi_init_config_t init = WIFI_INIT_CONFIG_DEFAULT();
  if (esp_wifi_init(&init) != ESP_OK) return false;
  event_group = xEventGroupCreate();
  if (event_group == nullptr) return false;

  esp_event_handler_instance_t wifi_instance{};
  esp_event_handler_instance_t ip_instance{};
  if (esp_event_handler_instance_register(WIFI_EVENT, ESP_EVENT_ANY_ID,
                                          &wifi_event, nullptr,
                                          &wifi_instance) != ESP_OK ||
      esp_event_handler_instance_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                          &ip_event, nullptr,
                                          &ip_instance) != ESP_OK) {
    return false;
  }

  std::copy(ssid.begin(), ssid.end(), config.sta.ssid);
  std::copy(password.begin(), password.end(), config.sta.password);
  config.sta.threshold.authmode = password.empty() ? WIFI_AUTH_OPEN : WIFI_AUTH_WPA2_PSK;
  config.sta.pmf_cfg.capable = true;
  config.sta.pmf_cfg.required = false;

  if (esp_wifi_set_mode(WIFI_MODE_STA) != ESP_OK ||
      esp_wifi_set_config(WIFI_IF_STA, &config) != ESP_OK ||
      esp_wifi_start() != ESP_OK) {
    return false;
  }

  const EventBits_t bits = xEventGroupWaitBits(
      event_group, kConnected | kFailed, pdFALSE, pdFALSE,
      pdMS_TO_TICKS(timeout_ms));
  return (bits & kConnected) != 0;
}

bool WifiStation::start_time_sync(std::string_view timezone_rule) {
  if (timezone_rule.empty() || timezone_rule.size() >= 64) return false;
  std::array<char, 64> timezone{};
  std::copy(timezone_rule.begin(), timezone_rule.end(), timezone.begin());
  if (setenv("TZ", timezone.data(), 1) != 0) return false;
  tzset();
  if (esp_sntp_enabled()) esp_sntp_stop();
  esp_sntp_setoperatingmode(ESP_SNTP_OPMODE_POLL);
  // Keep one server so the code is valid even when LWIP_SNTP_MAX_SERVERS == 1.
  esp_sntp_setservername(0, "pool.ntp.org");
  esp_sntp_init();
  return true;
}

} // namespace companion

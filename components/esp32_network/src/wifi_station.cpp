#include "companion/wifi_station.hpp"

#include "companion/reconnect_backoff.hpp"

#include <algorithm>
#include <array>
#include <cstdlib>
#include <cstring>
#include <ctime>

#include "esp_event.h"
#include "esp_netif.h"
#include "esp_random.h"
#include "esp_sntp.h"
#include "esp_timer.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"
#include "freertos/task.h"

namespace companion {
namespace {
constexpr EventBits_t kConnected = BIT0;
constexpr std::time_t kMinimumValidWallClock = 1'577'836'800; // 2020-01-01 UTC.
EventGroupHandle_t event_group{};
esp_timer_handle_t reconnect_timer{};
ReconnectBackoff reconnect_backoff{};

void schedule_reconnect();

void reconnect_timer_callback(void*) {
  if (esp_wifi_connect() != ESP_OK) schedule_reconnect();
}

void schedule_reconnect() {
  if (reconnect_timer == nullptr) return;
  const uint32_t delay_ms = reconnect_backoff.next_delay_ms(esp_random());
  if (esp_timer_is_active(reconnect_timer)) {
    (void)esp_timer_stop(reconnect_timer);
  }
  (void)esp_timer_start_once(reconnect_timer,
                             static_cast<uint64_t>(delay_ms) * 1'000ULL);
}

void wifi_event(void*, esp_event_base_t base, int32_t id, void*) {
  if (base != WIFI_EVENT) return;
  if (id == WIFI_EVENT_STA_START) {
    if (esp_wifi_connect() != ESP_OK) schedule_reconnect();
  } else if (id == WIFI_EVENT_STA_DISCONNECTED) {
    if (event_group != nullptr) xEventGroupClearBits(event_group, kConnected);
    schedule_reconnect();
  }
}

void ip_event(void*, esp_event_base_t base, int32_t id, void*) {
  if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
    reconnect_backoff.reset();
    if (reconnect_timer != nullptr && esp_timer_is_active(reconnect_timer)) {
      (void)esp_timer_stop(reconnect_timer);
    }
    if (event_group != nullptr) xEventGroupSetBits(event_group, kConnected);
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
  if (esp_wifi_init(&init) != ESP_OK ||
      // Companion's provisioning namespace is the canonical secret store.
      // Keep the Wi-Fi driver's configuration in RAM so SSID/password are not
      // silently duplicated into the driver's own flash-backed NVS records.
      esp_wifi_set_storage(WIFI_STORAGE_RAM) != ESP_OK) {
    return false;
  }
  event_group = xEventGroupCreate();
  if (event_group == nullptr) return false;

  esp_timer_create_args_t timer_args{};
  timer_args.callback = &reconnect_timer_callback;
  timer_args.name = "wifi-reconnect";
  if (esp_timer_create(&timer_args, &reconnect_timer) != ESP_OK) return false;

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
      event_group, kConnected, pdFALSE, pdFALSE, pdMS_TO_TICKS(timeout_ms));
  // A timeout means "not connected yet", not a permanent terminal state. The
  // reconnect timer keeps trying with bounded exponential backoff + jitter.
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

bool WifiStation::connected() const {
  return event_group != nullptr &&
         (xEventGroupGetBits(event_group) & kConnected) != 0;
}

bool WifiStation::time_valid() const {
  return std::time(nullptr) >= kMinimumValidWallClock;
}

bool WifiStation::wait_for_valid_time(uint32_t timeout_ms) const {
  constexpr uint32_t kPollMs = 250;
  uint32_t waited = 0;
  while (waited < timeout_ms) {
    if (time_valid()) return true;
    vTaskDelay(pdMS_TO_TICKS(kPollMs));
    waited += kPollMs;
  }
  return time_valid();
}

} // namespace companion

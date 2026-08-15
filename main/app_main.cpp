#include "companion/app.hpp"
#include "companion/esp32_audio.hpp"
#include "companion/esp_sr_audio_frontend.hpp"
#include "companion/gpio_button.hpp"
#include "companion/ssd1306_display.hpp"
#include "companion/transport_policy.hpp"
#include "companion/websocket_voice_backend.hpp"
#include "companion/wifi_station.hpp"

#include "esp_log.h"
#include "esp_mac.h"
#include "esp_timer.h"
#include "nvs_flash.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <array>
#include <cstdio>

namespace {
constexpr char kTag[] = "companion";
uint64_t now_ms() { return static_cast<uint64_t>(esp_timer_get_time() / 1000); }
}

extern "C" void app_main() {
  using namespace companion;

  static Esp32Audio audio;
  static EspSrAudioFrontend audio_frontend;
  static Ssd1306Display display;
  static GpioButton button;
  static WifiStation wifi;
  static WebSocketVoiceBackend backend;

  if (!display.initialize()) {
    ESP_LOGE(kTag, "SSD1306 initialization failed");
    return;
  }
  if (!button.initialize()) {
    ESP_LOGE(kTag, "button initialization failed");
    display.show(UiState::error, "BUTTON ERROR");
    return;
  }
  if (!audio.initialize()) {
    ESP_LOGE(kTag, "I2S audio initialization failed");
    display.show(UiState::error, "AUDIO ERROR");
    return;
  }
  if (!secure_product_transport(CONFIG_COMPANION_SERVER_URL,
                                CONFIG_COMPANION_DEVICE_CREDENTIAL)) {
    ESP_LOGE(kTag, "product transport requires wss:// and an enrolled device credential");
    display.show(UiState::error, "SECURE CONFIG ERROR");
    return;
  }

  esp_err_t nvs_result = nvs_flash_init();
  if (nvs_result == ESP_ERR_NVS_NO_FREE_PAGES ||
      nvs_result == ESP_ERR_NVS_NEW_VERSION_FOUND) {
    ESP_ERROR_CHECK(nvs_flash_erase());
    nvs_result = nvs_flash_init();
  }
  if (nvs_result != ESP_OK) {
    ESP_LOGE(kTag, "NVS initialization failed");
    display.show(UiState::error, "STORAGE ERROR");
    return;
  }

  const bool initially_connected =
      wifi.connect(CONFIG_COMPANION_WIFI_SSID, CONFIG_COMPANION_WIFI_PASSWORD);
  if (!initially_connected) {
    // This is no longer terminal: WifiStation keeps reconnecting with bounded
    // exponential backoff + jitter after the initial wait expires.
    ESP_LOGW(kTag, "Wi-Fi not connected yet; continuing in reconnecting state");
    display.show(UiState::connecting, "WIFI RETRY");
  }

  if (!wifi.start_time_sync(CONFIG_COMPANION_TZ_RULE)) {
    ESP_LOGW(kTag, "SNTP initialization failed; wall clock remains invalid");
  } else if (initially_connected && !wifi.wait_for_valid_time()) {
    // Starting SNTP is not evidence that wall clock is valid. The WebSocket client
    // keeps its own reconnect lifecycle; once Wi-Fi/SNTP converge a fresh session
    // hello causes the server to replay current config/schedule state.
    ESP_LOGW(kTag, "wall clock not valid yet; secure backend may retry until SNTP converges");
  }

  std::array<uint8_t, 6> mac{};
  ESP_ERROR_CHECK(esp_read_mac(mac.data(), ESP_MAC_WIFI_STA));
  std::array<char, 32> device_id{};
  std::snprintf(device_id.data(), device_id.size(), "%02x:%02x:%02x:%02x:%02x:%02x",
                mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
  if (!backend.initialize(CONFIG_COMPANION_SERVER_URL,
                          CONFIG_COMPANION_DEVICE_CREDENTIAL,
                          device_id.data(), device_id.data())) {
    // initialize() validates/allocates the backend. Network loss itself is handled
    // by the WebSocket client's auto-reconnect and is not a reason to recreate it.
    ESP_LOGE(kTag, "WebSocket backend initialization failed");
    display.show(UiState::error, "BACKEND INIT ERROR");
    return;
  }

  AppConfig app_config{};
  app_config.idle_after_ms = CONFIG_COMPANION_IDLE_AFTER_MS;
  app_config.alarm_visible_ms = CONFIG_COMPANION_ALARM_VISIBLE_MS;
  app_config.alarm_tone_ms = CONFIG_COMPANION_ALARM_TONE_MS;
#if CONFIG_COMPANION_SMART_VAD
  app_config.smart_vad_enabled = true;
  app_config.vad_mean_abs_threshold = CONFIG_COMPANION_VAD_THRESHOLD;
  app_config.vad_silence_ms = CONFIG_COMPANION_VAD_SILENCE_MS;
  app_config.vad_min_speech_ms = CONFIG_COMPANION_VAD_MIN_SPEECH_MS;
#else
  app_config.smart_vad_enabled = false;
#endif

  static CompanionApp app(audio, audio, display, button, backend,
                          audio_frontend, app_config);
  app.start(now_ms());
  ESP_LOGI(kTag, "hardware POC using ESP-SR AEC/WakeNet/VAD + secure WebSocket protocol v2");

  while (true) {
    app.tick(now_ms());
    vTaskDelay(pdMS_TO_TICKS(5));
  }
}

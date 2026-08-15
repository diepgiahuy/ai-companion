#include "companion/app.hpp"
#include "companion/claim_client.hpp"
#include "companion/esp32_audio.hpp"
#include "companion/esp_sr_audio_frontend.hpp"
#include "companion/gpio_button.hpp"
#include "companion/ota_manager.hpp"
#include "companion/provisioning_store.hpp"
#include "companion/setup_portal.hpp"
#include "companion/ssd1306_display.hpp"
#include "companion/transport_policy.hpp"
#include "companion/websocket_voice_backend.hpp"
#include "companion/wifi_station.hpp"

#include "esp_log.h"
#include "esp_mac.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "nvs_flash.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <array>
#include <cstdio>
#include <string_view>

namespace {
constexpr char kTag[] = "companion";
constexpr uint64_t kWifiReprovisionHoldMs = 3'000;
constexpr uint64_t kFactoryResetHoldMs = 8'000;
uint64_t now_ms() { return static_cast<uint64_t>(esp_timer_get_time() / 1000); }

enum class BootGesture {
  none,
  wifi_reprovision,
  factory_reset,
};

BootGesture boot_gesture(companion::GpioButton& button,
                         companion::Ssd1306Display& display) {
  if (!button.is_pressed()) return BootGesture::none;
  const uint64_t started = now_ms();
  display.show(companion::UiState::connecting, "HOLD 3S WIFI");
  while (button.is_pressed()) {
    const uint64_t elapsed = now_ms() - started;
    if (elapsed >= kFactoryResetHoldMs) {
      display.show(companion::UiState::error, "FACTORY RESET");
      return BootGesture::factory_reset;
    }
    if (elapsed >= kWifiReprovisionHoldMs) {
      display.show(companion::UiState::connecting, "RELEASE WIFI / 8S RESET");
    }
    vTaskDelay(pdMS_TO_TICKS(50));
  }
  return now_ms() - started >= kWifiReprovisionHoldMs
      ? BootGesture::wifi_reprovision
      : BootGesture::none;
}

[[noreturn]] void restart_after_message(companion::Ssd1306Display& display,
                                        std::string_view message) {
  display.show(companion::UiState::connecting, message);
  vTaskDelay(pdMS_TO_TICKS(750));
  esp_restart();
  while (true) vTaskDelay(portMAX_DELAY);
}

void show_portal_access(companion::Ssd1306Display& display,
                        const companion::provisioning::SetupPortal& portal,
                        uint32_t& frame) {
  std::array<char, 24> line{};
  switch (frame++ % 3) {
  case 0:
    std::snprintf(line.data(), line.size(), "AP %.*s", static_cast<int>(portal.ssid().size()), portal.ssid().data());
    break;
  case 1:
    std::snprintf(line.data(), line.size(), "PASS %.*s", static_cast<int>(portal.password().size()), portal.password().data());
    break;
  default:
    std::snprintf(line.data(), line.size(), "GO 192.168.4.1");
    break;
  }
  display.show(companion::UiState::connecting, line.data());
}

[[noreturn]] void run_setup_portal(companion::Ssd1306Display& display,
                                   const companion::provisioning::ProvisioningStore& store,
                                   std::string_view device_suffix) {
  companion::provisioning::SetupPortal portal;
  if (!portal.start(device_suffix)) {
    display.show(companion::UiState::error, "SETUP AP ERROR");
    while (true) vTaskDelay(portMAX_DELAY);
  }
  uint32_t frame = 0;
  uint64_t next_render = 0;
  while (true) {
    const uint64_t now = now_ms();
    if (now >= next_render) {
      show_portal_access(display, portal, frame);
      next_render = now + 2'000;
    }
    companion::provisioning::PendingConfig pending{};
    if (portal.take_pending(pending)) {
      if (!store.save_pending(pending)) {
        display.show(companion::UiState::error, "SETUP SAVE ERROR");
        while (true) vTaskDelay(portMAX_DELAY);
      }
      restart_after_message(display, "SETUP SAVED");
    }
    vTaskDelay(pdMS_TO_TICKS(50));
  }
}

[[noreturn]] void run_wifi_reprovision_portal(
    companion::Ssd1306Display& display,
    const companion::provisioning::ProvisioningStore& store,
    std::string_view device_suffix) {
  companion::provisioning::SetupPortal portal;
  if (!portal.start_wifi_only(device_suffix)) {
    display.show(companion::UiState::error, "WIFI SETUP ERROR");
    while (true) vTaskDelay(portMAX_DELAY);
  }
  uint32_t frame = 0;
  uint64_t next_render = 0;
  while (true) {
    const uint64_t now = now_ms();
    if (now >= next_render) {
      show_portal_access(display, portal, frame);
      next_render = now + 2'000;
    }
    companion::provisioning::WifiConfig wifi{};
    if (portal.take_wifi(wifi)) {
      if (!store.update_wifi(wifi)) {
        display.show(companion::UiState::error, "WIFI SAVE ERROR");
        while (true) vTaskDelay(portMAX_DELAY);
      }
      restart_after_message(display, "WIFI SAVED");
    }
    vTaskDelay(pdMS_TO_TICKS(50));
  }
}

[[noreturn]] void run_claim_phase(companion::Ssd1306Display& display,
                                  companion::WifiStation& wifi,
                                  const companion::provisioning::ProvisioningStore& store,
                                  const companion::provisioning::PendingConfig& pending,
                                  std::string_view device_id) {
  const bool initially_connected = wifi.connect(pending.wifi_ssid.view(), pending.wifi_password.view());
  if (!initially_connected) display.show(companion::UiState::connecting, "WIFI RETRY");
  if (!wifi.start_time_sync(CONFIG_COMPANION_TZ_RULE)) {
    ESP_LOGW(kTag, "SNTP initialization failed during claim phase");
  }
  companion::provisioning::ClaimClient claims;
  uint64_t next_claim = 0;
  while (true) {
    if (!wifi.connected()) {
      display.show(companion::UiState::connecting, "WIFI RETRY");
      vTaskDelay(pdMS_TO_TICKS(1'000));
      continue;
    }
    if (!wifi.time_valid()) {
      display.show(companion::UiState::connecting, "TIME SYNC");
      (void)wifi.wait_for_valid_time(5'000);
      vTaskDelay(pdMS_TO_TICKS(250));
      continue;
    }
    const uint64_t now = now_ms();
    if (now < next_claim) {
      vTaskDelay(pdMS_TO_TICKS(250));
      continue;
    }
    display.show(companion::UiState::connecting, "CLAIMING");
    companion::provisioning::ClaimResult result{};
    const auto status = claims.claim(pending, device_id, result);
    if (status == companion::provisioning::ClaimStatus::success) {
      if (!store.commit_runtime(pending, result.credential_view())) {
        display.show(companion::UiState::error, "CLAIM SAVE ERROR");
        while (true) vTaskDelay(portMAX_DELAY);
      }
      restart_after_message(display, "CLAIMED");
    }
    if (status == companion::provisioning::ClaimStatus::setup_required) {
      // Expired/invalid claim authorization means the pending setup must be
      // re-entered. No long-lived credential has been accepted locally yet.
      if (!store.clear()) {
        display.show(companion::UiState::error, "SETUP RESET ERROR");
        while (true) vTaskDelay(portMAX_DELAY);
      }
      restart_after_message(display, "SETUP REQUIRED");
    }
    if (status == companion::provisioning::ClaimStatus::owner_recovery_required) {
      // 409/410 can mean backend ownership or a committed delivery already
      // exists. Never loop a fresh claim or silently transfer ownership.
      ESP_LOGW(kTag, "owner recovery required before device claim can continue");
      display.show(companion::UiState::error, "OWNER RECOVERY");
      while (true) vTaskDelay(portMAX_DELAY);
    }
    display.show(companion::UiState::connecting, "CLAIM RETRY");
    next_claim = now + 10'000;
  }
}
} // namespace

extern "C" void app_main() {
  using namespace companion;
  namespace provisioning = companion::provisioning;

  static Esp32Audio audio;
  static EspSrAudioFrontend audio_frontend;
  static Ssd1306Display display;
  static GpioButton button;
  static WifiStation wifi;
  static WebSocketVoiceBackend backend;
  static OtaManager ota(wifi);
  static provisioning::ProvisioningStore provisioning_store;

  if (!display.initialize()) {
    ESP_LOGE(kTag, "SSD1306 initialization failed");
    return;
  }
  if (!button.initialize()) {
    ESP_LOGE(kTag, "button initialization failed");
    display.show(UiState::error, "BUTTON ERROR");
    return;
  }

  const BootGesture gesture = boot_gesture(button, display);

  // Product identity now lives in NVS. Never auto-erase an initialized product
  // identity merely because NVS reports a version/space problem. If NVS cannot
  // mount, only the explicit long factory-reset gesture may erase the full
  // partition; the shorter Wi-Fi gesture cannot destroy identity.
  const esp_err_t nvs_result = nvs_flash_init();
  if (nvs_result != ESP_OK) {
    ESP_LOGE(kTag, "NVS initialization failed: %s", esp_err_to_name(nvs_result));
    if (gesture == BootGesture::factory_reset) {
      if (nvs_flash_erase() != ESP_OK) {
        display.show(UiState::error, "RESET ERROR");
        return;
      }
      restart_after_message(display, "LOCAL RESET");
    }
    display.show(UiState::error, "STORAGE ERROR");
    return;
  }

  std::array<uint8_t, 6> mac{};
  ESP_ERROR_CHECK(esp_read_mac(mac.data(), ESP_MAC_WIFI_STA));
  std::array<char, 32> device_id{};
  std::snprintf(device_id.data(), device_id.size(), "%02x:%02x:%02x:%02x:%02x:%02x",
                mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
  std::array<char, 8> device_suffix{};
  std::snprintf(device_suffix.data(), device_suffix.size(), "%02X%02X", mac[4], mac[5]);

  if (gesture == BootGesture::factory_reset) {
    if (!provisioning_store.clear()) {
      display.show(UiState::error, "RESET ERROR");
      return;
    }
    restart_after_message(display, "LOCAL RESET");
  }

  provisioning::PersistedState persisted = provisioning_store.state();
  if (persisted == provisioning::PersistedState::invalid) {
    display.show(UiState::error, "CONFIG CORRUPT");
    ESP_LOGE(kTag, "provisioning state invalid; hold the boot button 8 seconds to clear local provisioning");
    return;
  }

  // A short boot gesture changes only Wi-Fi for an already-enrolled device.
  // Backend origin, device ID and long-lived device credential remain intact.
  if (gesture == BootGesture::wifi_reprovision &&
      (persisted == provisioning::PersistedState::ready ||
       persisted == provisioning::PersistedState::validating)) {
    provisioning::RuntimeConfig existing{};
    if (!provisioning_store.load_runtime(existing)) {
      display.show(UiState::error, "RUNTIME CORRUPT");
      return;
    }
    run_wifi_reprovision_portal(display, provisioning_store, device_suffix.data());
  }

  if (persisted == provisioning::PersistedState::unprovisioned) {
    run_setup_portal(display, provisioning_store, device_suffix.data());
  }
  if (persisted == provisioning::PersistedState::pending_claim) {
    provisioning::PendingConfig pending{};
    if (!provisioning_store.load_pending(pending)) {
      display.show(UiState::error, "PENDING CORRUPT");
      return;
    }
    run_claim_phase(display, wifi, provisioning_store, pending, device_id.data());
  }

  provisioning::RuntimeConfig runtime{};
  if (!provisioning_store.load_runtime(runtime) ||
      !secure_product_transport(runtime.server_url.view(), runtime.device_credential.view())) {
    ESP_LOGE(kTag, "stored product transport/runtime configuration rejected");
    display.show(UiState::error, "SECURE CONFIG ERROR");
    return;
  }

  // Interactive/audio resources are intentionally initialized only after the
  // local setup/claim phases are complete.
  if (!audio.initialize()) {
    ESP_LOGE(kTag, "I2S audio initialization failed");
    display.show(UiState::error, "AUDIO ERROR");
    return;
  }

  const bool initially_connected = wifi.connect(runtime.wifi_ssid.view(), runtime.wifi_password.view());
  if (!initially_connected) {
    ESP_LOGW(kTag, "Wi-Fi not connected yet; continuing in reconnecting state");
    display.show(UiState::connecting, "WIFI RETRY");
  }
  if (!wifi.start_time_sync(CONFIG_COMPANION_TZ_RULE)) {
    ESP_LOGW(kTag, "SNTP initialization failed; wall clock remains invalid");
  } else if (initially_connected && !wifi.wait_for_valid_time()) {
    ESP_LOGW(kTag, "wall clock not valid yet; secure backend may retry until SNTP converges");
  }

  ota.initialize(runtime.server_url.view(), runtime.device_credential.view(),
                 device_id.data(), CONFIG_COMPANION_OTA_BOARD,
                 CONFIG_COMPANION_OTA_CHANNEL,
                 CONFIG_COMPANION_OTA_HEALTH_TIMEOUT_MS);
  if (!ota.check_and_apply()) {
    ESP_LOGE(kTag, "OTA target/image rejected; continuing current valid firmware");
  }

  if (!backend.initialize(runtime.server_url.view(), runtime.device_credential.view(),
                          device_id.data(), device_id.data())) {
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
  ESP_LOGI(kTag, "hardware product path using stored provisioning + ESP-SR + secure WebSocket protocol v2");

  bool readiness_committed = persisted == provisioning::PersistedState::ready;
  while (true) {
    const uint64_t now = now_ms();
    app.tick(now);
    ota.tick(now);
    if (!readiness_committed && app.state() == UiState::ready) {
      if (provisioning_store.mark_ready()) {
        readiness_committed = true;
        ESP_LOGI(kTag, "provisioning validated by authenticated backend session");
      } else {
        ESP_LOGE(kTag, "failed to persist validated provisioning state");
      }
    }
    vTaskDelay(pdMS_TO_TICKS(5));
  }
}

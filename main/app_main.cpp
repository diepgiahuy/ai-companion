#include "companion/app.hpp"
#include "companion/claim_client.hpp"
#include "companion/esp32_audio.hpp"
#include "companion/esp_sr_audio_frontend.hpp"
#include "companion/gpio_button.hpp"
#include "companion/nimble_pairing_discovery.hpp"
#include "companion/ota_manager.hpp"
#include "companion/pairing_controller.hpp"
#include "companion/presentation_display.hpp"
#include "companion/press_gesture.hpp"
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
constexpr uint32_t kRuntimePairingHoldMs = 2'000;
uint64_t now_ms() { return static_cast<uint64_t>(esp_timer_get_time() / 1000); }

enum class BootGesture {
  none,
  wifi_reprovision,
  factory_reset,
};

class RuntimeButton final : public companion::Button {
public:
  explicit RuntimeButton(companion::GpioButton& physical)
      : physical_(physical), gesture_({.debounce_ms = 30,
                                      .hold_ms = kRuntimePairingHoldMs}) {}

  void reset(uint64_t now) { gesture_.reset(physical_.is_pressed(), now); }

  void suppress_current_press(uint64_t now) {
    gesture_.suppress_current_press(physical_.is_pressed(), now);
  }

  bool consume_press(uint64_t now) override {
    gesture_.sample(physical_.is_pressed(), now);
    return gesture_.consume_short();
  }

  bool consume_pairing_hold(uint64_t now) {
    gesture_.sample(physical_.is_pressed(), now);
    return gesture_.consume_hold();
  }

private:
  companion::GpioButton& physical_;
  companion::PressGesture gesture_;
};

BootGesture boot_gesture(companion::GpioButton& button,
                         companion::Display& display) {
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

[[noreturn]] void restart_after_message(companion::Display& display,
                                        std::string_view message) {
  display.show(companion::UiState::connecting, message);
  vTaskDelay(pdMS_TO_TICKS(750));
  esp_restart();
  while (true) vTaskDelay(portMAX_DELAY);
}

void show_portal_access(companion::Display& display,
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

[[noreturn]] void run_setup_portal(companion::Display& display,
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
    companion::Display& display,
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

[[noreturn]] void run_claim_phase(companion::Display& display,
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
      if (!store.clear()) {
        display.show(companion::UiState::error, "SETUP RESET ERROR");
        while (true) vTaskDelay(portMAX_DELAY);
      }
      restart_after_message(display, "SETUP REQUIRED");
    }
    if (status == companion::provisioning::ClaimStatus::owner_recovery_required) {
      ESP_LOGW(kTag, "owner recovery required before device claim can continue");
      display.show(companion::UiState::error, "OWNER RECOVERY");
      while (true) vTaskDelay(portMAX_DELAY);
    }
    display.show(companion::UiState::connecting, "CLAIM RETRY");
    next_claim = now + 10'000;
  }
}

std::string_view pairing_status(companion::pairing::State state) {
  switch (state) {
  case companion::pairing::State::discovering:
    return "PAIR SEARCH";
  case companion::pairing::State::session_pending:
    return "PAIR WAIT";
  case companion::pairing::State::awaiting_confirmation:
    return "TAP TO PAIR";
  case companion::pairing::State::confirming:
    return "PAIR CONFIRM";
  case companion::pairing::State::idle:
    break;
  }
  return "PAIRING";
}
} // namespace

extern "C" void app_main() {
  using namespace companion;
  namespace provisioning = companion::provisioning;

  static Esp32Audio audio;
  static EspSrAudioFrontend audio_frontend;
  static Ssd1306Display display;
  static PresentationDisplay presentation(display);
  static GpioButton physical_button;
  static RuntimeButton button(physical_button);
  static WifiStation wifi;
  static WebSocketVoiceBackend backend;
  static pairing::NimblePairingDiscovery pairing_radio;
  static pairing::PairingController pairing_controller(pairing_radio, backend);
  static OtaManager ota(wifi);
  static provisioning::ProvisioningStore provisioning_store;

  if (!display.initialize()) {
    ESP_LOGE(kTag, "SSD1306 initialization failed");
    return;
  }
  if (!physical_button.initialize()) {
    ESP_LOGE(kTag, "button initialization failed");
    presentation.show(UiState::error, "BUTTON ERROR");
    return;
  }

  const BootGesture gesture = boot_gesture(physical_button, presentation);
  button.reset(now_ms());

  if (!provisioning::secure_storage_preflight()) {
    ESP_LOGE(kTag, "secure storage preflight failed before NVS initialization");
    presentation.show(UiState::error, "SECURE STORAGE");
    return;
  }

  const esp_err_t nvs_result = nvs_flash_init();
  if (nvs_result != ESP_OK) {
    ESP_LOGE(kTag, "NVS initialization failed: %s", esp_err_to_name(nvs_result));
    if (gesture == BootGesture::factory_reset) {
      if (nvs_flash_erase() != ESP_OK) {
        presentation.show(UiState::error, "RESET ERROR");
        return;
      }
      restart_after_message(presentation, "LOCAL RESET");
    }
    presentation.show(UiState::error, "STORAGE ERROR");
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
      presentation.show(UiState::error, "RESET ERROR");
      return;
    }
    restart_after_message(presentation, "LOCAL RESET");
  }

  provisioning::PersistedState persisted = provisioning_store.state();
  if (persisted == provisioning::PersistedState::invalid) {
    presentation.show(UiState::error, "CONFIG CORRUPT");
    ESP_LOGE(kTag, "provisioning state invalid; hold the boot button 8 seconds to clear local provisioning");
    return;
  }

  if (gesture == BootGesture::wifi_reprovision &&
      (persisted == provisioning::PersistedState::ready ||
       persisted == provisioning::PersistedState::validating)) {
    provisioning::RuntimeConfig existing{};
    if (!provisioning_store.load_runtime(existing)) {
      presentation.show(UiState::error, "RUNTIME CORRUPT");
      return;
    }
    run_wifi_reprovision_portal(presentation, provisioning_store, device_suffix.data());
  }

  if (persisted == provisioning::PersistedState::unprovisioned) {
    run_setup_portal(presentation, provisioning_store, device_suffix.data());
  }
  if (persisted == provisioning::PersistedState::pending_claim) {
    provisioning::PendingConfig pending{};
    if (!provisioning_store.load_pending(pending)) {
      presentation.show(UiState::error, "PENDING CORRUPT");
      return;
    }
    run_claim_phase(presentation, wifi, provisioning_store, pending, device_id.data());
  }

  provisioning::RuntimeConfig runtime{};
  if (!provisioning_store.load_runtime(runtime) ||
      !secure_product_transport(runtime.server_url.view(), runtime.device_credential.view())) {
    ESP_LOGE(kTag, "stored product transport/runtime configuration rejected");
    presentation.show(UiState::error, "SECURE CONFIG ERROR");
    return;
  }

  if (!audio.initialize()) {
    ESP_LOGE(kTag, "I2S audio initialization failed");
    presentation.show(UiState::error, "AUDIO ERROR");
    return;
  }

  const bool initially_connected = wifi.connect(runtime.wifi_ssid.view(), runtime.wifi_password.view());
  if (!initially_connected) {
    ESP_LOGW(kTag, "Wi-Fi not connected yet; continuing in reconnecting state");
    presentation.show(UiState::connecting, "WIFI RETRY");
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
    presentation.show(UiState::error, "BACKEND INIT ERROR");
    return;
  }

  bool pairing_available = pairing_radio.init();
  if (pairing_available) pairing_available = backend.enable_pairing_protocol();
  if (!pairing_available) {
    ESP_LOGW(kTag, "pairing runtime unavailable; voice path remains enabled");
  }
  if (!backend.enable_confirmation_protocol()) {
    ESP_LOGE(kTag, "destructive confirmation protocol initialization failed");
    presentation.show(UiState::error, "CONFIRM INIT ERROR");
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

  static CompanionApp app(audio, audio, presentation, button, backend,
                          audio_frontend, app_config);
  app.start(now_ms());
  ESP_LOGI(kTag, "hardware product path using stored provisioning + ESP-SR + secure WebSocket protocol v2");

  bool readiness_committed = persisted == provisioning::PersistedState::ready;
  pairing::State previous_pairing_state = pairing_controller.state();
  UserConfirmationRequest confirmation{};
  bool confirmation_pending = false;
  uint64_t confirmation_deadline_ms = 0;
  while (true) {
    const uint64_t now = now_ms();

    (void)backend.advertise_user_confirmation();
    if (!confirmation_pending && backend.poll_user_confirmation(confirmation)) {
      button.suppress_current_press(now);
      confirmation_pending = true;
      confirmation_deadline_ms = now + confirmation.deadline_ms;
      (void)presentation.show_attention(PresentationDomain::confirmation,
                                        UiState::ready,
                                        confirmation.prompt_view());
    }
    if (confirmation_pending) {
      if (!backend.user_confirmation_current(confirmation)) {
        button.suppress_current_press(now);
        confirmation_pending = false;
        (void)presentation.clear_attention(PresentationDomain::confirmation);
      } else if (now >= confirmation_deadline_ms) {
        button.suppress_current_press(now);
        (void)backend.respond_user_confirmation(confirmation, false);
        confirmation_pending = false;
        (void)presentation.clear_attention(PresentationDomain::confirmation);
      } else if (button.consume_press(now)) {
        (void)backend.respond_user_confirmation(confirmation, true);
        confirmation_pending = false;
        (void)presentation.clear_attention(PresentationDomain::confirmation);
      }
    }

    if (!confirmation_pending && pairing_available) {
      const bool held = button.consume_pairing_hold(now);
      if (!pairing_controller.active() && held &&
          (app.state() == UiState::ready || app.state() == UiState::idle)) {
        if (!pairing_controller.start(now)) {
          (void)presentation.show_transient(UiState::error, "PAIR START FAIL");
        }
      } else if (pairing_controller.active()) {
        if (held) {
          pairing_controller.cancel();
        } else if (button.consume_press(now)) {
          if (pairing_controller.state() == pairing::State::awaiting_confirmation) {
            (void)pairing_controller.confirm(now);
          } else {
            pairing_controller.cancel();
          }
        }
      }
      pairing_controller.tick(now);
    }

    app.tick(now);
    ota.set_poll_interval_ms(static_cast<uint64_t>(app.config().ota_poll_interval_s) * 1000);
    ota.tick(now);

    const pairing::State current_pairing_state = pairing_controller.state();
    if (pairing_controller.active()) {
      if (current_pairing_state != previous_pairing_state) {
        (void)presentation.show_attention(PresentationDomain::pairing,
                                          UiState::ready,
                                          pairing_status(current_pairing_state));
      }
    } else if (previous_pairing_state != pairing::State::idle) {
      (void)presentation.clear_attention(PresentationDomain::pairing);
      const pairing::StopReason stopped = pairing_controller.last_stop_reason();
      (void)presentation.show_transient(
          stopped == pairing::StopReason::success ? UiState::ready : UiState::error,
          stopped == pairing::StopReason::success ? "PAIRED" : "PAIR ENDED");
    }
    previous_pairing_state = current_pairing_state;

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

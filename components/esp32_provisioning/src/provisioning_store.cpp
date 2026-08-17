#include "companion/provisioning_store.hpp"

#include "companion/provisioning_fsm.hpp"

#include "nvs.h"

#include <cstring>
#include <string>

namespace companion::provisioning {
namespace {
constexpr char kNamespace[] = "companion";
constexpr char kState[] = "state";
constexpr char kWifiSsid[] = "wifi_ssid";
constexpr char kWifiPass[] = "wifi_pass";
constexpr char kServerUrl[] = "server_url";
constexpr char kDeviceCred[] = "device_cred";
constexpr char kBootstrap[] = "bootstrap";
constexpr char kClaimCode[] = "claim_code";
constexpr char kClaimAuth[] = "claim_auth";
constexpr char kIdemKey[] = "idem_key";

bool get_string(nvs_handle_t handle, const char* key, char* out, size_t capacity) {
  if (out == nullptr || capacity == 0) return false;
  size_t required = capacity;
  const esp_err_t err = nvs_get_str(handle, key, out, &required);
  if (err != ESP_OK || required == 0 || required > capacity) {
    out[0] = '\0';
    return false;
  }
  out[capacity - 1] = '\0';
  return true;
}

bool set_string(nvs_handle_t handle, const char* key, std::string_view value) {
  if (value.find('\0') != std::string_view::npos) return false;
  return nvs_set_str(handle, key, std::string(value).c_str()) == ESP_OK;
}

bool erase_optional(nvs_handle_t handle, const char* key) {
  const esp_err_t err = nvs_erase_key(handle, key);
  return err == ESP_OK || err == ESP_ERR_NVS_NOT_FOUND;
}

template <size_t N>
bool get_fixed(nvs_handle_t handle, const char* key, FixedSecret<N>& out) {
  out.value.fill('\0');
  return get_string(handle, key, out.value.data(), out.value.size());
}

template <size_t N>
bool get_optional_fixed(nvs_handle_t handle, const char* key, FixedSecret<N>& out) {
  out.value.fill('\0');
  size_t required = out.value.size();
  const esp_err_t err = nvs_get_str(handle, key, out.value.data(), &required);
  if (err == ESP_ERR_NVS_NOT_FOUND) return true;
  if (err != ESP_OK || required == 0 || required > out.value.size()) {
    out.value.fill('\0');
    return false;
  }
  out.value.back() = '\0';
  return true;
}

bool write_optional(nvs_handle_t handle, const char* key, std::string_view value) {
  return value.empty() ? erase_optional(handle, key) : set_string(handle, key, value);
}

PersistedState parse_state(const char* raw) {
  if (raw == nullptr) return PersistedState::unprovisioned;
  if (std::strcmp(raw, "pending") == 0) return PersistedState::pending_claim;
  if (std::strcmp(raw, "validating") == 0) return PersistedState::validating;
  if (std::strcmp(raw, "ready") == 0) return PersistedState::ready;
  return PersistedState::invalid;
}

bool valid_pending_config(const PendingConfig& pending) {
  return valid_wifi(pending.wifi_ssid.view(), pending.wifi_password.view()) &&
         valid_pending_claim(PendingClaimView{
             pending.bootstrap_id.view(), pending.claim_code.view(),
             pending.claim_authorization.view(), pending.idempotency_key.view(),
             pending.server_url.view()});
}

bool valid_runtime(const RuntimeConfig& runtime) {
  return valid_runtime_config(RuntimeConfigView{
      runtime.wifi_ssid.view(), runtime.wifi_password.view(), runtime.server_url.view(),
      runtime.device_credential.view()});
}
} // namespace

PersistedState ProvisioningStore::state() const {
  nvs_handle_t handle{};
  const esp_err_t open_err = nvs_open(kNamespace, NVS_READONLY, &handle);
  if (open_err == ESP_ERR_NVS_NOT_FOUND) return PersistedState::unprovisioned;
  if (open_err != ESP_OK) return PersistedState::invalid;

  char raw[16]{};
  size_t size = sizeof(raw);
  const esp_err_t err = nvs_get_str(handle, kState, raw, &size);
  nvs_close(handle);
  // If the namespace already exists but its phase marker disappeared, other
  // credential/config keys may still exist. Fail closed rather than treating
  // that partial/corrupt state as a factory-new device.
  if (err != ESP_OK || size == 0 || size > sizeof(raw)) return PersistedState::invalid;
  return parse_state(raw);
}

bool ProvisioningStore::load_pending(PendingConfig& out) const {
  out = {};
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READONLY, &handle) != ESP_OK) return false;
  const bool ok = get_fixed(handle, kWifiSsid, out.wifi_ssid) &&
                  get_fixed(handle, kWifiPass, out.wifi_password) &&
                  get_fixed(handle, kServerUrl, out.server_url) &&
                  get_fixed(handle, kBootstrap, out.bootstrap_id) &&
                  get_optional_fixed(handle, kClaimCode, out.claim_code) &&
                  get_optional_fixed(handle, kClaimAuth, out.claim_authorization) &&
                  get_fixed(handle, kIdemKey, out.idempotency_key);
  nvs_close(handle);
  return ok && valid_pending_config(out);
}

bool ProvisioningStore::load_runtime(RuntimeConfig& out) const {
  out = {};
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READONLY, &handle) != ESP_OK) return false;
  const bool ok = get_fixed(handle, kWifiSsid, out.wifi_ssid) &&
                  get_fixed(handle, kWifiPass, out.wifi_password) &&
                  get_fixed(handle, kServerUrl, out.server_url) &&
                  get_fixed(handle, kDeviceCred, out.device_credential);
  nvs_close(handle);
  return ok && valid_runtime(out);
}

bool ProvisioningStore::save_pending(const PendingConfig& pending) const {
  if (!valid_pending_config(pending)) return false;
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  const bool ok = set_string(handle, kWifiSsid, pending.wifi_ssid.view()) &&
                  set_string(handle, kWifiPass, pending.wifi_password.view()) &&
                  set_string(handle, kServerUrl, pending.server_url.view()) &&
                  set_string(handle, kBootstrap, pending.bootstrap_id.view()) &&
                  write_optional(handle, kClaimCode, pending.claim_code.view()) &&
                  write_optional(handle, kClaimAuth, pending.claim_authorization.view()) &&
                  set_string(handle, kIdemKey, pending.idempotency_key.view()) &&
                  erase_optional(handle, kDeviceCred) &&
                  set_string(handle, kState, "pending") && nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

bool ProvisioningStore::commit_runtime(const PendingConfig& pending,
                                       std::string_view device_credential) const {
  RuntimeConfig runtime{};
  if (pending.wifi_ssid.view().size() >= runtime.wifi_ssid.value.size() ||
      pending.wifi_password.view().size() >= runtime.wifi_password.value.size() ||
      pending.server_url.view().size() >= runtime.server_url.value.size() ||
      device_credential.empty() || device_credential.size() >= runtime.device_credential.value.size()) {
    return false;
  }
  std::memcpy(runtime.wifi_ssid.value.data(), pending.wifi_ssid.view().data(), pending.wifi_ssid.view().size());
  std::memcpy(runtime.wifi_password.value.data(), pending.wifi_password.view().data(), pending.wifi_password.view().size());
  std::memcpy(runtime.server_url.value.data(), pending.server_url.view().data(), pending.server_url.view().size());
  std::memcpy(runtime.device_credential.value.data(), device_credential.data(), device_credential.size());
  if (!valid_runtime(runtime)) return false;

  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  const bool ok = set_string(handle, kWifiSsid, runtime.wifi_ssid.view()) &&
                  set_string(handle, kWifiPass, runtime.wifi_password.view()) &&
                  set_string(handle, kServerUrl, runtime.server_url.view()) &&
                  set_string(handle, kDeviceCred, runtime.device_credential.view()) &&
                  erase_optional(handle, kBootstrap) && erase_optional(handle, kClaimCode) &&
                  erase_optional(handle, kClaimAuth) && erase_optional(handle, kIdemKey) &&
                  set_string(handle, kState, "validating") && nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

bool ProvisioningStore::update_wifi(const WifiConfig& wifi) const {
  if (!valid_wifi(wifi.ssid.view(), wifi.password.view())) return false;
  RuntimeConfig existing{};
  if (!load_runtime(existing)) return false;

  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  // Preserve server_url + device_cred untouched. Force validating so the new
  // network is not considered READY until the normal enrolled WSS path succeeds.
  const bool ok = set_string(handle, kWifiSsid, wifi.ssid.view()) &&
                  set_string(handle, kWifiPass, wifi.password.view()) &&
                  set_string(handle, kState, "validating") && nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

bool ProvisioningStore::mark_ready() const {
  RuntimeConfig runtime{};
  if (!load_runtime(runtime)) return false;
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  const bool ok = set_string(handle, kState, "ready") && nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

bool ProvisioningStore::clear() const {
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  const bool ok = nvs_erase_all(handle) == ESP_OK && nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

bool ProvisioningStore::load_settings(companion::SettingsTwin& out) const {
  out = {};
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READONLY, &handle) != ESP_OK) return false;
  uint64_t ver = 0;
  if (nvs_get_u64(handle, "set_ver", &ver) != ESP_OK || ver == 0) {
    nvs_close(handle);
    return false;
  }
  out.version = ver;
  uint8_t smart_vad = 1;
  nvs_get_u8(handle, "set_vad_smart", &smart_vad);
  out.settings.smart_vad_enabled = (smart_vad != 0);

  uint32_t vad_thr = 450;
  nvs_get_u32(handle, "set_vad_thr", &vad_thr);
  out.settings.vad_threshold = vad_thr;

  uint32_t vad_sil = 800;
  nvs_get_u32(handle, "set_vad_sil", &vad_sil);
  out.settings.vad_silence_ms = vad_sil;

  uint32_t vad_min = 250;
  nvs_get_u32(handle, "set_vad_min", &vad_min);
  out.settings.vad_min_speech_ms = vad_min;

  uint32_t idle_ms = 5000;
  nvs_get_u32(handle, "set_idle_ms", &idle_ms);
  out.settings.idle_after_ms = idle_ms;

  uint32_t alarm_ms = 10000;
  nvs_get_u32(handle, "set_alarm_ms", &alarm_ms);
  out.settings.alarm_visible_ms = alarm_ms;

  uint32_t altone_ms = 900;
  nvs_get_u32(handle, "set_altone_ms", &altone_ms);
  out.settings.alarm_tone_ms = altone_ms;

  uint16_t altone_hz = 880;
  nvs_get_u16(handle, "set_altone_hz", &altone_hz);
  out.settings.alarm_tone_hz = altone_hz;

  int16_t altone_amp = 3500;
  nvs_get_i16(handle, "set_altone_amp", &altone_amp);
  out.settings.alarm_tone_amplitude = altone_amp;

  uint32_t ota_s = 21600;
  nvs_get_u32(handle, "set_ota_s", &ota_s);
  out.settings.ota_poll_interval_s = ota_s;

  uint8_t vol = 70;
  nvs_get_u8(handle, "set_volume", &vol);
  out.settings.volume = vol;

  uint32_t wake_thr = 6000;
  if (nvs_get_u32(handle, "set_wake_thr", &wake_thr) == ESP_OK) {
    out.settings.wake_threshold = static_cast<float>(wake_thr) / 10000.0F;
  }

  char mdl[64]{};
  if (get_string(handle, "set_wake_mdl", mdl, sizeof(mdl))) {
    out.settings.set_wake_model(mdl);
  }

  nvs_close(handle);
  return out.valid();
}

bool ProvisioningStore::save_settings(const companion::SettingsTwin& in) const {
  if (!in.valid()) return false;
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READWRITE, &handle) != ESP_OK) return false;
  const uint32_t wake_thr_int = static_cast<uint32_t>(in.settings.wake_threshold * 10000.0F);
  const bool ok = nvs_set_u64(handle, "set_ver", in.version) == ESP_OK &&
                  nvs_set_u8(handle, "set_vad_smart", in.settings.smart_vad_enabled ? 1 : 0) == ESP_OK &&
                  nvs_set_u32(handle, "set_vad_thr", in.settings.vad_threshold) == ESP_OK &&
                  nvs_set_u32(handle, "set_vad_sil", in.settings.vad_silence_ms) == ESP_OK &&
                  nvs_set_u32(handle, "set_vad_min", in.settings.vad_min_speech_ms) == ESP_OK &&
                  nvs_set_u32(handle, "set_idle_ms", in.settings.idle_after_ms) == ESP_OK &&
                  nvs_set_u32(handle, "set_alarm_ms", in.settings.alarm_visible_ms) == ESP_OK &&
                  nvs_set_u32(handle, "set_altone_ms", in.settings.alarm_tone_ms) == ESP_OK &&
                  nvs_set_u16(handle, "set_altone_hz", in.settings.alarm_tone_hz) == ESP_OK &&
                  nvs_set_i16(handle, "set_altone_amp", in.settings.alarm_tone_amplitude) == ESP_OK &&
                  nvs_set_u32(handle, "set_ota_s", in.settings.ota_poll_interval_s) == ESP_OK &&
                  nvs_set_u8(handle, "set_volume", in.settings.volume) == ESP_OK &&
                  nvs_set_u32(handle, "set_wake_thr", wake_thr_int) == ESP_OK &&
                  set_string(handle, "set_wake_mdl", in.settings.wake_model_view()) &&
                  nvs_commit(handle) == ESP_OK;
  nvs_close(handle);
  return ok;
}

} // namespace companion::provisioning

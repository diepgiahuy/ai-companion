#include "companion/provisioning_store.hpp"

#include "companion/provisioning_fsm.hpp"

#include "nvs.h"

#include <cstring>

namespace companion::provisioning {
namespace {
constexpr char kNamespace[] = "companion";
constexpr char kState[] = "state";
constexpr char kWifiSsid[] = "wifi_ssid";
constexpr char kWifiPass[] = "wifi_pass";
constexpr char kServerUrl[] = "server_url";
constexpr char kDeviceCred[] = "device_cred";
constexpr char kBootstrap[] = "bootstrap";
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

template <size_t N>
bool get_fixed(nvs_handle_t handle, const char* key, FixedSecret<N>& out) {
  out.value.fill('\0');
  return get_string(handle, key, out.value.data(), out.value.size());
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
             pending.bootstrap_id.view(), pending.claim_authorization.view(),
             pending.idempotency_key.view(), pending.server_url.view()});
}

bool valid_runtime(const RuntimeConfig& runtime) {
  return valid_runtime_config(RuntimeConfigView{
      runtime.wifi_ssid.view(), runtime.wifi_password.view(), runtime.server_url.view(),
      runtime.device_credential.view()});
}
} // namespace

PersistedState ProvisioningStore::state() const {
  nvs_handle_t handle{};
  if (nvs_open(kNamespace, NVS_READONLY, &handle) != ESP_OK) {
    return PersistedState::unprovisioned;
  }
  char raw[16]{};
  size_t size = sizeof(raw);
  const esp_err_t err = nvs_get_str(handle, kState, raw, &size);
  nvs_close(handle);
  if (err == ESP_ERR_NVS_NOT_FOUND) return PersistedState::unprovisioned;
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
                  get_fixed(handle, kClaimAuth, out.claim_authorization) &&
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
                  set_string(handle, kClaimAuth, pending.claim_authorization.view()) &&
                  set_string(handle, kIdemKey, pending.idempotency_key.view()) &&
                  nvs_erase_key(handle, kDeviceCred) != ESP_ERR_NVS_INVALID_HANDLE &&
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
                  nvs_erase_key(handle, kBootstrap) != ESP_ERR_NVS_INVALID_HANDLE &&
                  nvs_erase_key(handle, kClaimAuth) != ESP_ERR_NVS_INVALID_HANDLE &&
                  nvs_erase_key(handle, kIdemKey) != ESP_ERR_NVS_INVALID_HANDLE &&
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

} // namespace companion::provisioning

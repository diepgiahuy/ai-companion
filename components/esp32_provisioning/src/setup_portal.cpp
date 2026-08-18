#include "companion/setup_portal.hpp"

#include "companion/provisioning_fsm.hpp"

#include "cJSON.h"
#include "esp_event.h"
#include "esp_random.h"
#include "esp_wifi.h"

#include <array>
#include <cctype>
#include <cstdio>
#include <cstring>
#include <string>
#include <string_view>

namespace companion::provisioning {
namespace {
constexpr size_t kMaximumBodyBytes = 4 * 1024;
constexpr char kNonceHeader[] = "X-Companion-Setup-Nonce";
constexpr char kFullHtml[] = R"HTML(<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Companion Setup</title><style>body{font-family:sans-serif;max-width:34rem;margin:2rem auto;padding:0 1rem}label{display:block;margin:.8rem 0}input{width:100%;padding:.6rem;box-sizing:border-box}button{padding:.7rem 1rem}.ref{padding:.7rem;background:#f4f4f4;overflow-wrap:anywhere}code{font-weight:700}</style><h1>Companion Setup</h1><p>Connect Companion to your Wi-Fi network. After connecting, follow the prompt on your Companion display to complete setup without typing codes.</p><div class=ref>Device: <code id=device>loading…</code></div><form id=f><label>Wi-Fi SSID<input name=wifi_ssid required maxlength=32></label><label>Wi-Fi password<input name=wifi_password type=password maxlength=63></label><label>Companion WSS URL<input id=server name=server_url required placeholder="wss://..." maxlength=512></label><button>Connect and Continue</button></form><pre id=o></pre><script>let nonce='',info=null;async function setupNonce(){const r=await fetch('/nonce',{cache:'no-store'});if(!r.ok)throw new Error('nonce');nonce=(await r.text()).trim()}async function setupInfo(){const r=await fetch('/setup-info',{cache:'no-store'});if(!r.ok)throw new Error('info');info=await r.json();device.textContent=info.device_id;}Promise.all([setupNonce(),setupInfo()]).catch(()=>o.textContent='Setup session unavailable. Reload this page.');f.onsubmit=async e=>{e.preventDefault();try{if(!nonce)await setupNonce();o.textContent='Connecting...';const x=Object.fromEntries(new FormData(f));const r=await fetch('/configure',{method:'POST',headers:{'Content-Type':'application/json','X-Companion-Setup-Nonce':nonce},body:JSON.stringify(x)});o.textContent=r.ok?'Saved. Connecting to Wi-Fi...':'Invalid setup data.'}catch(_){o.textContent='Setup session unavailable. Reload this page.'}}</script>)HTML";
constexpr char kWifiHtml[] = R"HTML(<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Companion Wi-Fi</title><style>body{font-family:sans-serif;max-width:34rem;margin:2rem auto;padding:0 1rem}label{display:block;margin:.8rem 0}input{width:100%;padding:.6rem;box-sizing:border-box}button{padding:.7rem 1rem}</style><h1>Change Companion Wi-Fi</h1><p>Backend identity and device credential will be preserved.</p><form id=f><label>Wi-Fi SSID<input name=wifi_ssid required maxlength=32></label><label>Wi-Fi password<input name=wifi_password type=password maxlength=63></label><button>Save Wi-Fi and reboot</button></form><pre id=o></pre><script>let nonce='';async function setupNonce(){const r=await fetch('/nonce',{cache:'no-store'});if(!r.ok)throw new Error('nonce');nonce=(await r.text()).trim()}setupNonce().catch(()=>o.textContent='Setup session unavailable. Reload this page.');f.onsubmit=async e=>{e.preventDefault();try{if(!nonce)await setupNonce();o.textContent='Saving...';const x=Object.fromEntries(new FormData(f));const r=await fetch('/configure',{method:'POST',headers:{'Content-Type':'application/json','X-Companion-Setup-Nonce':nonce},body:JSON.stringify(x)});o.textContent=r.ok?'Saved. Companion will reboot.':'Invalid or expired setup session.'}catch(_){o.textContent='Setup session unavailable. Reload this page.'}}</script>)HTML";

template <size_t N>
bool copy_json_string(const cJSON* root, const char* name, FixedSecret<N>& output) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(root, name);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return false;
  const std::string_view value(item->valuestring);
  if (value.size() >= output.value.size()) return false;
  output.value.fill('\0');
  std::memcpy(output.value.data(), value.data(), value.size());
  return true;
}

bool copy_value(std::string_view value, char* destination, size_t capacity) {
  if (value.empty() || value.size() >= capacity) return false;
  std::memset(destination, 0, capacity);
  std::memcpy(destination, value.data(), value.size());
  return true;
}

template <size_t N>
bool copy_value(std::string_view value, FixedSecret<N>& destination) {
  return copy_value(value, destination.value.data(), destination.value.size());
}

bool validate_pending(const PendingConfig& pending) {
  return valid_wifi(pending.wifi_ssid.view(), pending.wifi_password.view()) &&
         valid_pending_claim(PendingClaimView{
             pending.bootstrap_id.view(), pending.device_code.view(),
             pending.user_code.view(), pending.claim_authorization.view(),
             pending.idempotency_key.view(), pending.server_url.view()});
}

bool validate_wifi(const WifiConfig& wifi) {
  return valid_wifi(wifi.ssid.view(), wifi.password.view());
}

void random_password(std::array<char, 17>& output) {
  static constexpr char alphabet[] = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
  output.fill('\0');
  for (size_t i = 0; i < 12; ++i) {
    output[i] = alphabet[esp_random() % (sizeof(alphabet) - 1)];
  }
}

void random_nonce(std::array<char, 33>& output) {
  static constexpr char hex[] = "0123456789abcdef";
  output.fill('\0');
  for (size_t i = 0; i < 16; i += 4) {
    const uint32_t value = esp_random();
    for (size_t j = 0; j < 4; ++j) {
      const uint8_t byte = static_cast<uint8_t>(value >> (j * 8));
      output[(i + j) * 2] = hex[(byte >> 4) & 0x0f];
      output[(i + j) * 2 + 1] = hex[byte & 0x0f];
    }
  }
}

bool random_pending_identifier(FixedSecret<129>& output) {
  std::array<char, 33> value{};
  random_nonce(value);
  return copy_value(value.data(), output);
}
} // namespace

std::string_view SetupPortal::ssid() const {
  return {ssid_.data(), std::strlen(ssid_.data())};
}

std::string_view SetupPortal::password() const {
  return {password_.data(), std::strlen(password_.data())};
}

bool SetupPortal::start(std::string_view device_id, std::string_view device_suffix) {
  return start_impl(device_id, device_suffix, false);
}

bool SetupPortal::start_wifi_only(std::string_view device_suffix) {
  return start_impl({}, device_suffix, true);
}

bool SetupPortal::start_impl(std::string_view device_id, std::string_view device_suffix, bool wifi_only) {
  if (device_suffix.empty() || (!wifi_only && (device_id.empty() || device_id.size() > 128))) return false;
  configured_.store(false);
  pending_ = {};
  wifi_ = {};
  device_id_.fill('\0');
  wifi_only_ = wifi_only;
  ssid_.fill('\0');
  password_.fill('\0');
  session_nonce_.fill('\0');
  if (!wifi_only) {
    if (!copy_value(device_id, device_id_.data(), device_id_.size()) ||
        !random_pending_identifier(pending_.bootstrap_id) ||
        !random_pending_identifier(pending_.idempotency_key)) {
      return false;
    }
  }
  const std::string_view suffix = device_suffix.substr(device_suffix.size() > 4 ? device_suffix.size() - 4 : 0);
  std::snprintf(ssid_.data(), ssid_.size(), "Companion-%.*s", static_cast<int>(suffix.size()), suffix.data());
  random_password(password_);
  random_nonce(session_nonce_);

  esp_err_t err = esp_netif_init();
  if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) return false;
  err = esp_event_loop_create_default();
  if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) return false;
  if (esp_netif_create_default_wifi_ap() == nullptr) return false;

  wifi_init_config_t init = WIFI_INIT_CONFIG_DEFAULT();
  if (esp_wifi_init(&init) != ESP_OK || esp_wifi_set_storage(WIFI_STORAGE_RAM) != ESP_OK ||
      esp_wifi_set_mode(WIFI_MODE_AP) != ESP_OK) {
    return false;
  }
  wifi_config_t config{};
  std::memcpy(config.ap.ssid, ssid_.data(), std::strlen(ssid_.data()));
  config.ap.ssid_len = static_cast<uint8_t>(std::strlen(ssid_.data()));
  std::memcpy(config.ap.password, password_.data(), std::strlen(password_.data()));
  config.ap.channel = 1;
  config.ap.max_connection = 2;
  config.ap.authmode = WIFI_AUTH_WPA2_PSK;
  config.ap.pmf_cfg.capable = true;
  config.ap.pmf_cfg.required = false;
  if (esp_wifi_set_config(WIFI_IF_AP, &config) != ESP_OK || esp_wifi_start() != ESP_OK) {
    return false;
  }

  httpd_config_t http = HTTPD_DEFAULT_CONFIG();
  http.max_uri_handlers = 5;
  http.lru_purge_enable = true;
  if (httpd_start(&server_, &http) != ESP_OK) return false;

  httpd_uri_t index{};
  index.uri = "/";
  index.method = HTTP_GET;
  index.handler = &SetupPortal::handle_index;
  index.user_ctx = this;
  httpd_uri_t nonce_uri{};
  nonce_uri.uri = "/nonce";
  nonce_uri.method = HTTP_GET;
  nonce_uri.handler = &SetupPortal::handle_nonce;
  nonce_uri.user_ctx = this;
  httpd_uri_t info_uri{};
  info_uri.uri = "/setup-info";
  info_uri.method = HTTP_GET;
  info_uri.handler = &SetupPortal::handle_setup_info;
  info_uri.user_ctx = this;
  httpd_uri_t configure_uri{};
  configure_uri.uri = "/configure";
  configure_uri.method = HTTP_POST;
  configure_uri.handler = &SetupPortal::handle_configure;
  configure_uri.user_ctx = this;
  return httpd_register_uri_handler(server_, &index) == ESP_OK &&
         httpd_register_uri_handler(server_, &nonce_uri) == ESP_OK &&
         httpd_register_uri_handler(server_, &info_uri) == ESP_OK &&
         httpd_register_uri_handler(server_, &configure_uri) == ESP_OK;
}

bool SetupPortal::take_pending(PendingConfig& out) {
  if (wifi_only_ || !configured_.exchange(false)) return false;
  out = pending_;
  pending_ = {};
  session_nonce_.fill('\0');
  return true;
}

bool SetupPortal::take_wifi(WifiConfig& out) {
  if (!wifi_only_ || !configured_.exchange(false)) return false;
  out = wifi_;
  wifi_ = {};
  session_nonce_.fill('\0');
  return true;
}

esp_err_t SetupPortal::handle_index(httpd_req_t* request) {
  if (request == nullptr || request->user_ctx == nullptr) return ESP_FAIL;
  auto* portal = static_cast<SetupPortal*>(request->user_ctx);
  httpd_resp_set_type(request, "text/html; charset=utf-8");
  httpd_resp_set_hdr(request, "Cache-Control", "no-store");
  return httpd_resp_send(request, portal->wifi_only_ ? kWifiHtml : kFullHtml,
                         HTTPD_RESP_USE_STRLEN);
}

esp_err_t SetupPortal::handle_nonce(httpd_req_t* request) {
  if (request == nullptr || request->user_ctx == nullptr) return ESP_FAIL;
  auto* portal = static_cast<SetupPortal*>(request->user_ctx);
  httpd_resp_set_type(request, "text/plain; charset=utf-8");
  httpd_resp_set_hdr(request, "Cache-Control", "no-store");
  return httpd_resp_send(request, portal->session_nonce_.data(), HTTPD_RESP_USE_STRLEN);
}

esp_err_t SetupPortal::handle_setup_info(httpd_req_t* request) {
  if (request == nullptr || request->user_ctx == nullptr) return ESP_FAIL;
  auto* portal = static_cast<SetupPortal*>(request->user_ctx);
  if (portal->wifi_only_ || portal->pending_.bootstrap_id.view().empty() || portal->device_id_[0] == '\0') {
    httpd_resp_send_err(request, HTTPD_404_NOT_FOUND, "setup reference unavailable");
    return ESP_OK;
  }
  cJSON* root = cJSON_CreateObject();
  if (root == nullptr) return ESP_FAIL;
  cJSON_AddStringToObject(root, "device_id", portal->device_id_.data());
  const std::string bootstrap(portal->pending_.bootstrap_id.view());
  cJSON_AddStringToObject(root, "bootstrap_id", bootstrap.c_str());
  char* encoded = cJSON_PrintUnformatted(root);
  cJSON_Delete(root);
  if (encoded == nullptr) return ESP_FAIL;
  httpd_resp_set_type(request, "application/json");
  httpd_resp_set_hdr(request, "Cache-Control", "no-store");
  const esp_err_t result = httpd_resp_sendstr(request, encoded);
  cJSON_free(encoded);
  return result;
}

esp_err_t SetupPortal::handle_configure(httpd_req_t* request) {
  if (request == nullptr || request->user_ctx == nullptr) return ESP_FAIL;
  return static_cast<SetupPortal*>(request->user_ctx)->configure(request);
}

esp_err_t SetupPortal::configure(httpd_req_t* request) {
  if (configured_.load() || request->content_len <= 0 ||
      request->content_len > static_cast<int>(kMaximumBodyBytes)) {
    httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "invalid setup data");
    return ESP_OK;
  }

  std::array<char, 33> provided_nonce{};
  const size_t nonce_length = httpd_req_get_hdr_value_len(request, kNonceHeader);
  if (nonce_length == 0 || nonce_length >= provided_nonce.size() ||
      httpd_req_get_hdr_value_str(request, kNonceHeader, provided_nonce.data(), provided_nonce.size()) != ESP_OK ||
      std::string_view(provided_nonce.data(), nonce_length) != std::string_view(session_nonce_.data())) {
    httpd_resp_send_err(request, HTTPD_403_FORBIDDEN, "invalid setup session");
    return ESP_OK;
  }

  std::array<char, kMaximumBodyBytes + 1> body{};
  size_t total = 0;
  while (total < static_cast<size_t>(request->content_len)) {
    const int read = httpd_req_recv(request, body.data() + total,
                                    static_cast<size_t>(request->content_len) - total);
    if (read <= 0) {
      httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "invalid setup data");
      return ESP_OK;
    }
    total += static_cast<size_t>(read);
  }
  cJSON* root = cJSON_ParseWithLength(body.data(), total);
  if (root == nullptr || !cJSON_IsObject(root)) {
    if (root != nullptr) cJSON_Delete(root);
    httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "invalid setup data");
    return ESP_OK;
  }

  bool ok = false;
  if (wifi_only_) {
    WifiConfig candidate{};
    ok = copy_json_string(root, "wifi_ssid", candidate.ssid) &&
         copy_json_string(root, "wifi_password", candidate.password) &&
         validate_wifi(candidate);
    if (ok) wifi_ = candidate;
  } else {
    PendingConfig candidate{};
    candidate.bootstrap_id = pending_.bootstrap_id;
    candidate.idempotency_key = pending_.idempotency_key;
    ok = copy_json_string(root, "wifi_ssid", candidate.wifi_ssid) &&
         copy_json_string(root, "wifi_password", candidate.wifi_password) &&
         copy_json_string(root, "server_url", candidate.server_url) &&
         validate_pending(candidate);
    if (ok) pending_ = candidate;
  }
  cJSON_Delete(root);
  if (!ok) {
    httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "invalid setup data");
    return ESP_OK;
  }

  configured_.store(true);
  httpd_resp_set_hdr(request, "Cache-Control", "no-store");
  httpd_resp_sendstr(request, "saved");
  return ESP_OK;
}

} // namespace companion::provisioning

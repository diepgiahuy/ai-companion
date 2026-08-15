#include "companion/ota_manager.hpp"

#include "companion/wifi_station.hpp"

#include <array>
#include <cctype>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <string>

#include "cJSON.h"
#include "esp_app_desc.h"
#include "esp_crt_bundle.h"
#include "esp_http_client.h"
#include "esp_log.h"
#include "esp_ota_ops.h"
#include "esp_partition.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "psa/crypto.h"

namespace companion {
namespace {
constexpr char kTag[] = "companion_ota";
constexpr size_t kMaximumManifestBytes = 16 * 1024;
constexpr int kProtocolVersion = 2;
constexpr int kSecurityVersion = 0; // irreversible eFuse anti-rollback is not enabled here.
constexpr uint64_t kHealthProbeIntervalMs = 5'000;

struct Manifest {
  std::string version;
  std::string board;
  std::string channel;
  std::string url;
  std::array<uint8_t, 32> sha256{};
  size_t size{};
  int protocol_min{};
  int security_version{};
  std::string expires_at;
};

struct HttpResult {
  int status{-1};
  std::string body;
};

bool safe_name(std::string_view value) {
  if (value.empty() || value.size() > 64) return false;
  for (unsigned char c : value) {
    if (!(std::isalnum(c) || c == '-' || c == '_' || c == '.')) return false;
  }
  return true;
}

std::string percent_encode(std::string_view value) {
  static constexpr char kHex[] = "0123456789ABCDEF";
  std::string output;
  output.reserve(value.size() * 3);
  for (unsigned char c : value) {
    if (std::isalnum(c) || c == '-' || c == '_' || c == '.' || c == '~') {
      output.push_back(static_cast<char>(c));
    } else {
      output.push_back('%');
      output.push_back(kHex[c >> 4]);
      output.push_back(kHex[c & 0x0f]);
    }
  }
  return output;
}

bool https_origin(std::string_view websocket, std::string& output) {
  constexpr std::string_view prefix = "wss://";
  if (!websocket.starts_with(prefix)) return false;
  const size_t host_begin = prefix.size();
  const size_t path = websocket.find('/', host_begin);
  const std::string_view authority = path == std::string_view::npos
                                         ? websocket.substr(host_begin)
                                         : websocket.substr(host_begin, path - host_begin);
  if (authority.empty() || authority.find('@') != std::string_view::npos) return false;
  output = "https://";
  output.append(authority);
  return true;
}

const cJSON* field(const cJSON* root, const char* lower, const char* upper) {
  const cJSON* item = cJSON_GetObjectItemCaseSensitive(root, lower);
  return item != nullptr ? item : cJSON_GetObjectItemCaseSensitive(root, upper);
}

bool json_string(const cJSON* root, const char* lower, const char* upper,
                 std::string& out, size_t maximum) {
  const cJSON* item = field(root, lower, upper);
  if (!cJSON_IsString(item) || item->valuestring == nullptr) return false;
  out = item->valuestring;
  return !out.empty() && out.size() <= maximum;
}

bool json_integer(const cJSON* root, const char* lower, const char* upper,
                  int64_t& out) {
  const cJSON* item = field(root, lower, upper);
  if (!cJSON_IsNumber(item) || item->valuedouble < 0 ||
      item->valuedouble > 9'007'199'254'740'991.0) return false;
  const int64_t value = static_cast<int64_t>(item->valuedouble);
  if (item->valuedouble != static_cast<double>(value)) return false;
  out = value;
  return true;
}

bool hex_digest(std::string_view value, std::array<uint8_t, 32>& out) {
  if (value.size() != 64) return false;
  const auto nibble = [](char c) -> int {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
  };
  for (size_t i = 0; i < out.size(); ++i) {
    const int hi = nibble(value[2 * i]);
    const int lo = nibble(value[2 * i + 1]);
    if (hi < 0 || lo < 0) return false;
    out[i] = static_cast<uint8_t>((hi << 4) | lo);
  }
  return true;
}

bool parse_utc(std::string_view value, std::time_t& out) {
  if (value.size() < 20 || value.back() != 'Z') return false;
  int year{}, month{}, day{}, hour{}, minute{}, second{};
  const std::string prefix(value.substr(0, 19));
  if (std::sscanf(prefix.c_str(), "%4d-%2d-%2dT%2d:%2d:%2d",
                  &year, &month, &day, &hour, &minute, &second) != 6) return false;
  if (year < 2020 || month < 1 || month > 12 || day < 1 || day > 31 ||
      hour > 23 || minute > 59 || second > 60) return false;
  int y = year - (month <= 2);
  const int era = (y >= 0 ? y : y - 399) / 400;
  const unsigned yoe = static_cast<unsigned>(y - era * 400);
  const unsigned m = static_cast<unsigned>(month);
  const unsigned doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 +
                       static_cast<unsigned>(day) - 1;
  const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  const int64_t days = static_cast<int64_t>(era) * 146097 + doe - 719468;
  out = static_cast<std::time_t>(days * 86400 + hour * 3600 + minute * 60 + second);
  return true;
}

bool parse_manifest(std::string_view raw, Manifest& manifest) {
  cJSON* root = cJSON_ParseWithLength(raw.data(), raw.size());
  if (root == nullptr) return false;
  std::string digest;
  int64_t size{}, protocol{}, security{};
  const bool ok = cJSON_IsObject(root) &&
      json_string(root, "version", "Version", manifest.version, 128) &&
      json_string(root, "channel", "Channel", manifest.channel, 64) &&
      json_string(root, "board", "Board", manifest.board, 64) &&
      json_string(root, "url", "URL", manifest.url, 512) &&
      json_string(root, "sha256", "SHA256", digest, 64) &&
      json_string(root, "expires_at", "ExpiresAt", manifest.expires_at, 64) &&
      json_integer(root, "size", "Size", size) && size > 0 &&
      json_integer(root, "protocol_min", "ProtocolMin", protocol) &&
      json_integer(root, "security_version", "SecurityVersion", security) &&
      hex_digest(digest, manifest.sha256);
  cJSON_Delete(root);
  if (!ok || size > static_cast<int64_t>(SIZE_MAX) ||
      protocol > INT32_MAX || security > INT32_MAX) return false;
  manifest.size = static_cast<size_t>(size);
  manifest.protocol_min = static_cast<int>(protocol);
  manifest.security_version = static_cast<int>(security);
  return true;
}

HttpResult authenticated_get(std::string_view url, std::string_view token,
                             std::string_view device_id) {
  HttpResult result;
  const std::string owned_url(url);
  const std::string authorization = "Bearer " + std::string(token);
  const std::string owned_device_id(device_id);
  esp_http_client_config_t config{};
  config.url = owned_url.c_str();
  config.crt_bundle_attach = esp_crt_bundle_attach;
  config.timeout_ms = 15'000;
  config.keep_alive_enable = true;
  esp_http_client_handle_t client = esp_http_client_init(&config);
  if (client == nullptr) return result;
  esp_http_client_set_header(client, "Authorization", authorization.c_str());
  esp_http_client_set_header(client, "Device-Id", owned_device_id.c_str());
  esp_http_client_set_header(client, "Accept", "application/json");
  if (esp_http_client_open(client, 0) != ESP_OK) {
    esp_http_client_cleanup(client);
    return result;
  }
  const int64_t content_length = esp_http_client_fetch_headers(client);
  result.status = esp_http_client_get_status_code(client);
  if (content_length > static_cast<int64_t>(kMaximumManifestBytes)) {
    result.status = -1;
  } else if (result.status == 200) {
    std::array<char, 1024> buffer{};
    while (result.body.size() <= kMaximumManifestBytes) {
      const int read = esp_http_client_read(client, buffer.data(), buffer.size());
      if (read < 0) { result.body.clear(); result.status = -1; break; }
      if (read == 0) break;
      result.body.append(buffer.data(), static_cast<size_t>(read));
    }
    if (result.body.empty() || result.body.size() > kMaximumManifestBytes) result.status = -1;
  }
  esp_http_client_close(client);
  esp_http_client_cleanup(client);
  return result;
}

bool stream_image(const Manifest& manifest, const esp_partition_t* update_partition) {
  if (update_partition == nullptr || manifest.size > update_partition->size) return false;
  esp_http_client_config_t config{};
  config.url = manifest.url.c_str();
  config.crt_bundle_attach = esp_crt_bundle_attach;
  config.timeout_ms = 30'000;
  config.keep_alive_enable = true;
  esp_http_client_handle_t client = esp_http_client_init(&config);
  if (client == nullptr) return false;
  if (esp_http_client_open(client, 0) != ESP_OK) {
    esp_http_client_cleanup(client);
    return false;
  }
  const int64_t content_length = esp_http_client_fetch_headers(client);
  if (esp_http_client_get_status_code(client) != 200 ||
      (content_length >= 0 && static_cast<uint64_t>(content_length) != manifest.size)) {
    esp_http_client_close(client);
    esp_http_client_cleanup(client);
    return false;
  }

  esp_ota_handle_t ota{};
  if (esp_ota_begin(update_partition, manifest.size, &ota) != ESP_OK) {
    esp_http_client_close(client);
    esp_http_client_cleanup(client);
    return false;
  }
  bool ok = psa_crypto_init() == PSA_SUCCESS;
  psa_hash_operation_t hash = PSA_HASH_OPERATION_INIT;
  if (ok) ok = psa_hash_setup(&hash, PSA_ALG_SHA_256) == PSA_SUCCESS;
  std::array<uint8_t, 4096> buffer{};
  size_t total = 0;
  while (ok && total < manifest.size) {
    const int read = esp_http_client_read(client, reinterpret_cast<char*>(buffer.data()), buffer.size());
    if (read <= 0 || total + static_cast<size_t>(read) > manifest.size) { ok = false; break; }
    if (psa_hash_update(&hash, buffer.data(), static_cast<size_t>(read)) != PSA_SUCCESS ||
        esp_ota_write(ota, buffer.data(), static_cast<size_t>(read)) != ESP_OK) {
      ok = false;
      break;
    }
    total += static_cast<size_t>(read);
  }
  std::array<uint8_t, 32> digest{};
  size_t digest_size = 0;
  if (ok) {
    ok = total == manifest.size &&
         psa_hash_finish(&hash, digest.data(), digest.size(), &digest_size) == PSA_SUCCESS &&
         digest_size == digest.size() && digest == manifest.sha256;
  } else {
    (void)psa_hash_abort(&hash);
  }
  esp_http_client_close(client);
  esp_http_client_cleanup(client);
  if (!ok) {
    (void)esp_ota_abort(ota);
    return false;
  }
  if (esp_ota_end(ota) != ESP_OK) return false;
  return esp_ota_set_boot_partition(update_partition) == ESP_OK;
}
} // namespace

bool OtaManager::initialize(std::string_view server_url, std::string_view token,
                            std::string_view device_id, std::string_view board,
                            std::string_view channel, uint32_t health_timeout_ms) {
  server_url_ = server_url;
  token_ = token;
  device_id_ = device_id;
  board_ = board;
  channel_ = channel;
  enabled_ = safe_name(board_) && safe_name(channel_) && !token_.empty() &&
             !device_id_.empty() && server_url_.starts_with("wss://");
  const esp_partition_t* running = esp_ota_get_running_partition();
  esp_ota_img_states_t state{};
  pending_verify_ = running != nullptr &&
      esp_ota_get_state_partition(running, &state) == ESP_OK &&
      state == ESP_OTA_IMG_PENDING_VERIFY;
  const uint64_t now_ms = static_cast<uint64_t>(esp_timer_get_time() / 1000);
  health_deadline_ms_ = now_ms + health_timeout_ms;
  next_health_probe_ms_ = now_ms;
  if (!enabled_) {
    ESP_LOGW(kTag, "OTA disabled: configure secure WSS, device credential, board and channel");
  }
  return true;
}

std::string OtaManager::target_url() const {
  std::string origin;
  if (!https_origin(server_url_, origin)) return {};
  const esp_app_desc_t* app = esp_app_get_description();
  const std::string current = app != nullptr ? app->version : "unknown";
  return origin + "/v1/device/firmware/" + percent_encode(channel_) + "/" +
         percent_encode(board_) + "?current=" + percent_encode(current);
}

bool OtaManager::backend_auth_reachable() {
  if (!enabled_) return false;
  const std::string target = target_url();
  if (target.empty()) return false;
  const HttpResult result = authenticated_get(target, token_, device_id_);
  return result.status == 200 || result.status == 204;
}

void OtaManager::tick(uint64_t now_ms) {
  if (!pending_verify_) return;
  if (now_ms >= health_deadline_ms_) {
    ESP_LOGE(kTag, "pending OTA image did not reach network/auth health; rolling back");
    if (esp_ota_mark_app_invalid_rollback_and_reboot() != ESP_OK) {
      ESP_LOGE(kTag, "OTA rollback request failed");
    }
    return;
  }
  if (now_ms < next_health_probe_ms_ || !wifi_.connected() || !wifi_.time_valid()) return;
  next_health_probe_ms_ = now_ms + kHealthProbeIntervalMs;
  if (backend_auth_reachable() && esp_ota_mark_app_valid_cancel_rollback() == ESP_OK) {
    pending_verify_ = false;
    ESP_LOGI(kTag, "pending OTA image marked valid after authenticated backend health");
  }
}

bool OtaManager::check_and_apply() {
  if (!enabled_ || pending_verify_ || !wifi_.connected() || !wifi_.time_valid()) return true;
  const std::string target = target_url();
  if (target.empty()) return false;
  const HttpResult response = authenticated_get(target, token_, device_id_);
  if (response.status == 204) return true;
  if (response.status < 0) {
    ESP_LOGW(kTag, "OTA target check unavailable; continuing current firmware");
    return true;
  }
  if (response.status != 200) return false;

  Manifest manifest;
  if (!parse_manifest(response.body, manifest)) return false;
  const esp_app_desc_t* app = esp_app_get_description();
  const std::string current = app != nullptr ? app->version : "unknown";
  if (manifest.version == current) return true;
  if (manifest.board != board_ || manifest.channel != channel_ ||
      manifest.protocol_min > kProtocolVersion ||
      manifest.security_version > kSecurityVersion ||
      !manifest.url.starts_with("https://")) return false;
  std::time_t expiry{};
  if (!parse_utc(manifest.expires_at, expiry) || std::time(nullptr) >= expiry) return false;
  const esp_partition_t* update = esp_ota_get_next_update_partition(nullptr);
  if (update == nullptr || manifest.size > update->size) return false;

  ESP_LOGI(kTag, "applying OTA version=%s bytes=%u", manifest.version.c_str(),
           static_cast<unsigned>(manifest.size));
  if (!stream_image(manifest, update)) return false;
  ESP_LOGI(kTag, "OTA image verified and selected; rebooting into pending-verify state");
  esp_restart();
  return true;
}

} // namespace companion

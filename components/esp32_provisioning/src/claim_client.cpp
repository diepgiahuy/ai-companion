#include "companion/claim_client.hpp"

#include "cJSON.h"
#include "esp_crt_bundle.h"
#include "esp_http_client.h"

#include <array>
#include <cstring>
#include <string>

namespace companion::provisioning {
namespace {
constexpr size_t kMaximumClaimResponseBytes = 2 * 1024;

struct HttpResult {
  int status{-1};
  std::string body;
};

bool copy_fixed(std::string_view source, char* destination, size_t capacity) {
  if (destination == nullptr || capacity == 0 || source.empty() || source.size() >= capacity) {
    return false;
  }
  std::memset(destination, 0, capacity);
  std::memcpy(destination, source.data(), source.size());
  return true;
}

HttpResult post_claim(std::string_view url, std::string_view authorization,
                      std::string_view idempotency_key, std::string_view body) {
  HttpResult result;
  const std::string owned_url(url);
  const std::string bearer = "Bearer " + std::string(authorization);
  const std::string idem(idempotency_key);
  const std::string payload(body);

  esp_http_client_config_t config{};
  config.url = owned_url.c_str();
  config.crt_bundle_attach = esp_crt_bundle_attach;
  config.timeout_ms = 15'000;
  config.keep_alive_enable = false;
  esp_http_client_handle_t client = esp_http_client_init(&config);
  if (client == nullptr) return result;

  esp_http_client_set_method(client, HTTP_METHOD_POST);
  esp_http_client_set_header(client, "Authorization", bearer.c_str());
  esp_http_client_set_header(client, "Idempotency-Key", idem.c_str());
  esp_http_client_set_header(client, "Content-Type", "application/json");
  esp_http_client_set_header(client, "Accept", "application/json");
  if (esp_http_client_open(client, payload.size()) != ESP_OK) {
    esp_http_client_cleanup(client);
    return result;
  }
  const int written = esp_http_client_write(client, payload.data(), payload.size());
  if (written != static_cast<int>(payload.size())) {
    esp_http_client_close(client);
    esp_http_client_cleanup(client);
    return result;
  }
  const int64_t content_length = esp_http_client_fetch_headers(client);
  result.status = esp_http_client_get_status_code(client);
  if (content_length > static_cast<int64_t>(kMaximumClaimResponseBytes)) {
    result.status = -1;
  } else if (result.status >= 200 && result.status < 300) {
    std::array<char, 512> buffer{};
    while (result.body.size() <= kMaximumClaimResponseBytes) {
      const int read = esp_http_client_read(client, buffer.data(), buffer.size());
      if (read < 0) {
        result.status = -1;
        result.body.clear();
        break;
      }
      if (read == 0) break;
      result.body.append(buffer.data(), static_cast<size_t>(read));
    }
    if (result.body.empty() || result.body.size() > kMaximumClaimResponseBytes) {
      result.status = -1;
      result.body.clear();
    }
  }
  esp_http_client_close(client);
  esp_http_client_cleanup(client);
  return result;
}
} // namespace

std::string_view ClaimResult::credential_view() const {
  size_t length = 0;
  while (length < device_credential.size() && device_credential[length] != '\0') ++length;
  return {device_credential.data(), length};
}

bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output) {
  output.fill('\0');
  constexpr std::string_view prefix = "wss://";
  if (!websocket_url.starts_with(prefix) || websocket_url.find('@') != std::string_view::npos ||
      websocket_url.find('?') != std::string_view::npos || websocket_url.find('#') != std::string_view::npos) {
    return false;
  }
  const size_t authority_begin = prefix.size();
  const size_t slash = websocket_url.find('/', authority_begin);
  const std::string_view authority = slash == std::string_view::npos
      ? websocket_url.substr(authority_begin)
      : websocket_url.substr(authority_begin, slash - authority_begin);
  if (authority.empty()) return false;
  const std::string value = "https://" + std::string(authority) + "/v1/owner/device-claims";
  if (value.size() >= output.size()) return false;
  std::memcpy(output.data(), value.data(), value.size());
  return true;
}

bool ClaimClient::claim(const PendingConfig& pending, std::string_view device_id,
                        ClaimResult& result) const {
  result = {};
  if (device_id.empty() || device_id.size() > 128) return false;
  std::array<char, 640> url{};
  if (!owner_claim_url(pending.server_url.view(), url)) return false;

  cJSON* request = cJSON_CreateObject();
  if (request == nullptr) return false;
  const std::string owned_device(device_id);
  const std::string owned_bootstrap(pending.bootstrap_id.view());
  cJSON_AddStringToObject(request, "device_id", owned_device.c_str());
  cJSON_AddStringToObject(request, "bootstrap_id", owned_bootstrap.c_str());
  char* encoded = cJSON_PrintUnformatted(request);
  cJSON_Delete(request);
  if (encoded == nullptr) return false;
  const std::string body(encoded);
  cJSON_free(encoded);

  const HttpResult response = post_claim(url.data(), pending.claim_authorization.view(),
                                         pending.idempotency_key.view(), body);
  if (response.status < 200 || response.status >= 300) return false;

  cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
  if (root == nullptr) return false;
  const cJSON* response_device = cJSON_GetObjectItemCaseSensitive(root, "device_id");
  const cJSON* credential = cJSON_GetObjectItemCaseSensitive(root, "device_credential");
  const cJSON* replayed = cJSON_GetObjectItemCaseSensitive(root, "replayed");
  const bool ok = cJSON_IsString(response_device) && response_device->valuestring != nullptr &&
                  std::string_view(response_device->valuestring) == device_id &&
                  cJSON_IsString(credential) && credential->valuestring != nullptr &&
                  copy_fixed(credential->valuestring, result.device_credential.data(),
                             result.device_credential.size()) &&
                  cJSON_IsBool(replayed);
  if (ok) result.replayed = cJSON_IsTrue(replayed);
  cJSON_Delete(root);
  if (!ok) result = {};
  return ok;
}

} // namespace companion::provisioning

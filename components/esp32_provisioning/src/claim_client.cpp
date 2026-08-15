#include "companion/claim_client.hpp"

#include "companion/provisioning_fsm.hpp"

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

ClaimStatus classify_claim_status(int status) {
  if (status >= 200 && status < 300) return ClaimStatus::success;
  if (status == 400 || status == 401 || status == 403) return ClaimStatus::setup_required;
  if (status == 409 || status == 410) return ClaimStatus::owner_recovery_required;
  if (status < 0 || status == 408 || status == 425 || status == 429 || status >= 500) {
    return ClaimStatus::retryable;
  }
  return ClaimStatus::setup_required;
}

ClaimStatus classify_redemption_status(int status) {
  if (status >= 200 && status < 300) return ClaimStatus::success;
  if (status == 400 || status == 401 || status == 403 || status == 404 || status == 410) {
    return ClaimStatus::setup_required;
  }
  if (status < 0 || status == 408 || status == 425 || status == 429 || status >= 500) {
    return ClaimStatus::retryable;
  }
  return ClaimStatus::setup_required;
}

HttpResult post_json(std::string_view url, std::string_view authorization,
                     std::string_view idempotency_key, std::string_view body) {
  HttpResult result;
  const std::string owned_url(url);
  const std::string payload(body);
  const std::string bearer = authorization.empty() ? std::string{} : "Bearer " + std::string(authorization);
  const std::string idem(idempotency_key);

  esp_http_client_config_t config{};
  config.url = owned_url.c_str();
  config.crt_bundle_attach = esp_crt_bundle_attach;
  config.timeout_ms = 15'000;
  config.keep_alive_enable = false;
  config.disable_auto_redirect = true;
  esp_http_client_handle_t client = esp_http_client_init(&config);
  if (client == nullptr) return result;

  esp_http_client_set_method(client, HTTP_METHOD_POST);
  if (!bearer.empty()) esp_http_client_set_header(client, "Authorization", bearer.c_str());
  if (!idem.empty()) esp_http_client_set_header(client, "Idempotency-Key", idem.c_str());
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

bool owner_endpoint_url(std::string_view websocket_url, std::string_view path,
                        std::array<char, 640>& output) {
  output.fill('\0');
  constexpr std::string_view prefix = "wss://";
  if (!websocket_url.starts_with(prefix) || websocket_url.find('@') != std::string_view::npos ||
      websocket_url.find('?') != std::string_view::npos || websocket_url.find('#') != std::string_view::npos ||
      !path.starts_with('/')) {
    return false;
  }
  const size_t authority_begin = prefix.size();
  const size_t slash = websocket_url.find('/', authority_begin);
  const std::string_view authority = slash == std::string_view::npos
      ? websocket_url.substr(authority_begin)
      : websocket_url.substr(authority_begin, slash - authority_begin);
  if (authority.empty()) return false;
  const std::string value = "https://" + std::string(authority) + std::string(path);
  if (value.size() >= output.size()) return false;
  std::memcpy(output.data(), value.data(), value.size());
  return true;
}
} // namespace

std::string_view ClaimResult::credential_view() const {
  size_t length = 0;
  while (length < device_credential.size() && device_credential[length] != '\0') ++length;
  return {device_credential.data(), length};
}

std::string_view ClaimAuthorizationResult::authorization_view() const {
  size_t length = 0;
  while (length < claim_authorization.size() && claim_authorization[length] != '\0') ++length;
  return {claim_authorization.data(), length};
}

bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output) {
  return owner_endpoint_url(websocket_url, "/v1/owner/device-claims", output);
}

bool owner_claim_code_redeem_url(std::string_view websocket_url,
                                 std::array<char, 640>& output) {
  return owner_endpoint_url(websocket_url, "/v1/owner/device-claim-codes/redeem", output);
}

ClaimStatus ClaimClient::redeem_code(const PendingConfig& pending, std::string_view device_id,
                                     ClaimAuthorizationResult& result) const {
  result = {};
  if (device_id.empty() || device_id.size() > 128 ||
      !valid_human_claim_code(pending.claim_code.view()) ||
      !pending.claim_authorization.view().empty()) {
    return ClaimStatus::setup_required;
  }
  std::array<char, 640> url{};
  if (!owner_claim_code_redeem_url(pending.server_url.view(), url)) return ClaimStatus::setup_required;

  cJSON* request = cJSON_CreateObject();
  if (request == nullptr) return ClaimStatus::retryable;
  const std::string owned_code(pending.claim_code.view());
  const std::string owned_bootstrap(pending.bootstrap_id.view());
  const std::string owned_device(device_id);
  cJSON_AddStringToObject(request, "claim_code", owned_code.c_str());
  cJSON_AddStringToObject(request, "bootstrap_id", owned_bootstrap.c_str());
  cJSON_AddStringToObject(request, "device_id", owned_device.c_str());
  char* encoded = cJSON_PrintUnformatted(request);
  cJSON_Delete(request);
  if (encoded == nullptr) return ClaimStatus::retryable;
  const std::string body(encoded);
  cJSON_free(encoded);

  const HttpResult response = post_json(url.data(), {}, {}, body);
  const ClaimStatus status = classify_redemption_status(response.status);
  if (status != ClaimStatus::success) return status;

  cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
  if (root == nullptr) return ClaimStatus::retryable;
  const cJSON* authorization = cJSON_GetObjectItemCaseSensitive(root, "claim_authorization");
  const bool ok = cJSON_IsString(authorization) && authorization->valuestring != nullptr &&
                  copy_fixed(authorization->valuestring, result.claim_authorization.data(),
                             result.claim_authorization.size());
  cJSON_Delete(root);
  if (!ok) {
    result = {};
    return ClaimStatus::retryable;
  }
  return ClaimStatus::success;
}

ClaimStatus ClaimClient::claim(const PendingConfig& pending, std::string_view device_id,
                               ClaimResult& result) const {
  result = {};
  if (device_id.empty() || device_id.size() > 128) return ClaimStatus::setup_required;

  // Reload the canonical pending phase on every retry. After a human code is
  // redeemed, persist the opaque authorization before attempting credential
  // issuance; a reboot therefore resumes from authorization rather than trying
  // to replay the one-time human code.
  PendingConfig effective = pending;
  ProvisioningStore persistence;
  PendingConfig persisted{};
  if (persistence.load_pending(persisted)) effective = persisted;

  if (!effective.claim_code.view().empty()) {
    ClaimAuthorizationResult authorization{};
    const ClaimStatus redemption = redeem_code(effective, device_id, authorization);
    if (redemption != ClaimStatus::success) return redemption;
    effective.claim_authorization.value.fill('\0');
    if (!copy_fixed(authorization.authorization_view(), effective.claim_authorization.value.data(),
                    effective.claim_authorization.value.size())) {
      return ClaimStatus::retryable;
    }
    effective.claim_code.value.fill('\0');
    if (!persistence.save_pending(effective)) return ClaimStatus::retryable;
  }

  if (!effective.claim_code.view().empty() || effective.claim_authorization.view().empty()) {
    return ClaimStatus::setup_required;
  }
  std::array<char, 640> url{};
  if (!owner_claim_url(effective.server_url.view(), url)) return ClaimStatus::setup_required;

  cJSON* request = cJSON_CreateObject();
  if (request == nullptr) return ClaimStatus::retryable;
  const std::string owned_device(device_id);
  const std::string owned_bootstrap(effective.bootstrap_id.view());
  cJSON_AddStringToObject(request, "device_id", owned_device.c_str());
  cJSON_AddStringToObject(request, "bootstrap_id", owned_bootstrap.c_str());
  char* encoded = cJSON_PrintUnformatted(request);
  cJSON_Delete(request);
  if (encoded == nullptr) return ClaimStatus::retryable;
  const std::string body(encoded);
  cJSON_free(encoded);

  const HttpResult response = post_json(url.data(), effective.claim_authorization.view(),
                                        effective.idempotency_key.view(), body);
  const ClaimStatus status = classify_claim_status(response.status);
  if (status != ClaimStatus::success) return status;

  cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
  if (root == nullptr) return ClaimStatus::retryable;
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
  if (!ok) {
    result = {};
    return ClaimStatus::retryable;
  }
  return ClaimStatus::success;
}

} // namespace companion::provisioning

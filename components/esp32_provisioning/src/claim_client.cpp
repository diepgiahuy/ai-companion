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
constexpr size_t kMaximumClaimResponseBytes = 4 * 1024;

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
  } else {
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

std::string_view ClaimSessionCreateResult::device_code_view() const {
  size_t length = 0;
  while (length < device_code.size() && device_code[length] != '\0') ++length;
  return {device_code.data(), length};
}

std::string_view ClaimSessionCreateResult::user_code_view() const {
  size_t length = 0;
  while (length < user_code.size() && user_code[length] != '\0') ++length;
  return {user_code.data(), length};
}

std::string_view ClaimSessionCreateResult::verification_uri_view() const {
  size_t length = 0;
  while (length < verification_uri.size() && verification_uri[length] != '\0') ++length;
  return {verification_uri.data(), length};
}

std::string_view ClaimSessionCreateResult::verification_uri_complete_view() const {
  size_t length = 0;
  while (length < verification_uri_complete.size() && verification_uri_complete[length] != '\0') ++length;
  return {verification_uri_complete.data(), length};
}

bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output) {
  return owner_endpoint_url(websocket_url, "/v1/owner/device-claims", output);
}

bool owner_device_claim_session_url(std::string_view websocket_url,
                                    std::array<char, 640>& output) {
  return owner_endpoint_url(websocket_url, "/v1/device-claim-sessions", output);
}

bool owner_device_claim_session_token_url(std::string_view websocket_url,
                                          std::array<char, 640>& output) {
  return owner_endpoint_url(websocket_url, "/v1/device-claim-sessions/token", output);
}

ClaimStatus ClaimClient::create_session(const PendingConfig& pending, std::string_view device_id,
                                        ClaimSessionCreateResult& result) const {
  result = {};
  if (device_id.empty() || device_id.size() > 128 || pending.bootstrap_id.view().empty()) {
    return ClaimStatus::setup_required;
  }
  std::array<char, 640> url{};
  if (!owner_device_claim_session_url(pending.server_url.view(), url)) {
    return ClaimStatus::setup_required;
  }

  cJSON* request = cJSON_CreateObject();
  if (request == nullptr) return ClaimStatus::retryable;
  const std::string owned_device(device_id);
  const std::string owned_bootstrap(pending.bootstrap_id.view());
  cJSON_AddStringToObject(request, "device_id", owned_device.c_str());
  cJSON_AddStringToObject(request, "bootstrap_id", owned_bootstrap.c_str());
  char* encoded = cJSON_PrintUnformatted(request);
  cJSON_Delete(request);
  if (encoded == nullptr) return ClaimStatus::retryable;
  const std::string body(encoded);
  cJSON_free(encoded);

  const HttpResult response = post_json(url.data(), {}, {}, body);
  if (response.status < 200 || response.status >= 300) {
    return classify_claim_status(response.status);
  }

  cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
  if (root == nullptr) return ClaimStatus::retryable;

  const cJSON* device_code = cJSON_GetObjectItemCaseSensitive(root, "device_code");
  const cJSON* user_code = cJSON_GetObjectItemCaseSensitive(root, "user_code");
  const cJSON* verification_uri = cJSON_GetObjectItemCaseSensitive(root, "verification_uri");
  const cJSON* verification_uri_complete = cJSON_GetObjectItemCaseSensitive(root, "verification_uri_complete");
  const cJSON* expires_in = cJSON_GetObjectItemCaseSensitive(root, "expires_in");
  const cJSON* interval = cJSON_GetObjectItemCaseSensitive(root, "interval");

  const bool ok = cJSON_IsString(device_code) && device_code->valuestring != nullptr &&
                  copy_fixed(device_code->valuestring, result.device_code.data(), result.device_code.size()) &&
                  cJSON_IsString(user_code) && user_code->valuestring != nullptr &&
                  copy_fixed(user_code->valuestring, result.user_code.data(), result.user_code.size()) &&
                  cJSON_IsString(verification_uri) && verification_uri->valuestring != nullptr &&
                  copy_fixed(verification_uri->valuestring, result.verification_uri.data(), result.verification_uri.size()) &&
                  cJSON_IsString(verification_uri_complete) && verification_uri_complete->valuestring != nullptr &&
                  copy_fixed(verification_uri_complete->valuestring, result.verification_uri_complete.data(), result.verification_uri_complete.size()) &&
                  cJSON_IsNumber(expires_in) && cJSON_IsNumber(interval);
  if (ok) {
    result.expires_in = expires_in->valueint;
    result.interval = interval->valueint;
  }
  cJSON_Delete(root);
  if (!ok) {
    result = {};
    return ClaimStatus::retryable;
  }
  return ClaimStatus::success;
}

ClaimStatus ClaimClient::poll_session(const PendingConfig& pending, std::string_view device_code,
                                      ClaimAuthorizationResult& result, int& next_interval_sec) const {
  result = {};
  if (device_code.empty() || device_code.size() > 128) return ClaimStatus::setup_required;

  std::array<char, 640> url{};
  if (!owner_device_claim_session_token_url(pending.server_url.view(), url)) {
    return ClaimStatus::setup_required;
  }

  cJSON* request = cJSON_CreateObject();
  if (request == nullptr) return ClaimStatus::retryable;
  const std::string owned_code(device_code);
  cJSON_AddStringToObject(request, "device_code", owned_code.c_str());
  char* encoded = cJSON_PrintUnformatted(request);
  cJSON_Delete(request);
  if (encoded == nullptr) return ClaimStatus::retryable;
  const std::string body(encoded);
  cJSON_free(encoded);

  const HttpResult response = post_json(url.data(), {}, {}, body);

  if (response.status == 200) {
    cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
    if (root == nullptr) return ClaimStatus::retryable;
    const cJSON* auth = cJSON_GetObjectItemCaseSensitive(root, "claim_authorization");
    const bool ok = cJSON_IsString(auth) && auth->valuestring != nullptr &&
                    copy_fixed(auth->valuestring, result.claim_authorization.data(), result.claim_authorization.size());
    cJSON_Delete(root);
    return ok ? ClaimStatus::success : ClaimStatus::retryable;
  }

  if (response.status == 400 || response.status == 403 || response.status == 429) {
    cJSON* root = cJSON_ParseWithLength(response.body.data(), response.body.size());
    if (root != nullptr) {
      const cJSON* err = cJSON_GetObjectItemCaseSensitive(root, "error");
      const cJSON* interval = cJSON_GetObjectItemCaseSensitive(root, "interval");
      if (cJSON_IsNumber(interval) && interval->valueint > 0) {
        next_interval_sec = interval->valueint;
      }
      if (cJSON_IsString(err) && err->valuestring != nullptr) {
        const std::string_view err_str(err->valuestring);
        cJSON_Delete(root);
        if (err_str == "authorization_pending") return ClaimStatus::authorization_pending;
        if (err_str == "slow_down") return ClaimStatus::slow_down;
        if (err_str == "access_denied") return ClaimStatus::setup_required;
        if (err_str == "expired_token" || err_str == "invalid_grant") return ClaimStatus::setup_required;
      } else {
        cJSON_Delete(root);
      }
    }
  }

  if (response.status == 403) return ClaimStatus::setup_required;
  if (response.status < 0 || response.status == 408 || response.status == 425 || response.status == 429 || response.status >= 500) {
    return ClaimStatus::retryable;
  }
  return ClaimStatus::setup_required;
}

ClaimStatus ClaimClient::claim(const PendingConfig& pending, std::string_view device_id,
                               ClaimResult& result) const {
  result = {};
  if (device_id.empty() || device_id.size() > 128) return ClaimStatus::setup_required;

  PendingConfig effective = pending;
  ProvisioningStore persistence;
  PendingConfig persisted{};
  if (persistence.load_pending(persisted)) effective = persisted;

  if (effective.claim_authorization.view().empty()) {
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

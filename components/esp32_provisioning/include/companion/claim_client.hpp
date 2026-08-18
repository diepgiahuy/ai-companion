#pragma once

#include "companion/provisioning_store.hpp"

#include <array>
#include <string_view>

namespace companion::provisioning {

enum class ClaimStatus {
  success,
  authorization_pending,
  slow_down,
  retryable,
  setup_required,
  owner_recovery_required,
};

struct ClaimResult {
  std::array<char, 513> device_credential{};
  bool replayed{};

  std::string_view credential_view() const;
};

struct ClaimAuthorizationResult {
  std::array<char, 1025> claim_authorization{};

  std::string_view authorization_view() const;
};

struct ClaimSessionCreateResult {
  std::array<char, 129> device_code{};
  std::array<char, 33> user_code{};
  std::array<char, 640> verification_uri{};
  std::array<char, 640> verification_uri_complete{};
  int expires_in{};
  int interval{};

  std::string_view device_code_view() const;
  std::string_view user_code_view() const;
  std::string_view verification_uri_view() const;
  std::string_view verification_uri_complete_view() const;
};

// Converts the product WSS endpoint to fixed same-origin onboarding endpoints.
// Userinfo, query and fragment are rejected; an optional WebSocket path is
// intentionally discarded because onboarding uses fixed backend routes.
bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output);
bool owner_device_claim_session_url(std::string_view websocket_url,
                                    std::array<char, 640>& output);
bool owner_device_claim_session_token_url(std::string_view websocket_url,
                                          std::array<char, 640>& output);

class ClaimClient final {
public:
  ClaimStatus create_session(const PendingConfig& pending, std::string_view device_id,
                             ClaimSessionCreateResult& result) const;
  ClaimStatus poll_session(const PendingConfig& pending, std::string_view device_code,
                           ClaimAuthorizationResult& result, int& next_interval_sec) const;
  ClaimStatus claim(const PendingConfig& pending, std::string_view device_id,
                    ClaimResult& result) const;
};

} // namespace companion::provisioning

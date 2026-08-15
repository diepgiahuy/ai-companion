#pragma once

#include "companion/provisioning_store.hpp"

#include <array>
#include <string_view>

namespace companion::provisioning {

enum class ClaimStatus {
  success,
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

// Converts the product WSS endpoint to fixed same-origin onboarding endpoints.
// Userinfo, query and fragment are rejected; an optional WebSocket path is
// intentionally discarded because onboarding uses fixed backend routes.
bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output);
bool owner_claim_code_redeem_url(std::string_view websocket_url,
                                 std::array<char, 640>& output);

class ClaimClient final {
public:
  ClaimStatus redeem_code(const PendingConfig& pending, std::string_view device_id,
                          ClaimAuthorizationResult& result) const;
  ClaimStatus claim(const PendingConfig& pending, std::string_view device_id,
                    ClaimResult& result) const;
};

} // namespace companion::provisioning

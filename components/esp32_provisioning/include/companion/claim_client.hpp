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

// Convert the product WSS endpoint to fixed same-origin HTTPS owner endpoints.
// Userinfo, query and fragment are rejected; an optional WebSocket path is
// intentionally discarded so onboarding cannot redirect secrets to a second host.
bool owner_claim_url(std::string_view websocket_url,
                     std::array<char, 640>& output);
bool owner_claim_code_exchange_url(std::string_view websocket_url,
                                   std::array<char, 640>& output);

class ClaimClient final {
public:
  // Exchanges the one human claim code for the existing opaque, short-lived
  // claim authorization and replaces pending.claim_authorization in memory.
  // The caller persists the updated PendingConfig before attempting claim().
  ClaimStatus exchange_claim_code(PendingConfig& pending,
                                  std::string_view device_id) const;

  ClaimStatus claim(const PendingConfig& pending, std::string_view device_id,
                    ClaimResult& result) const;
};

} // namespace companion::provisioning

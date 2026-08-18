#pragma once

#include <array>
#include <string_view>

namespace companion::provisioning {

// Must run before nvs_flash_init(). In the normal development build this is a
// no-op. A secure-storage build fails closed unless its configured eFuse block
// was deliberately provisioned with an HMAC_UP key outside the application.
bool secure_storage_preflight();

enum class PersistedState {
  unprovisioned,
  pending_claim,
  validating,
  ready,
  invalid,
};

template <size_t N>
struct FixedSecret {
  std::array<char, N> value{};

  std::string_view view() const {
    size_t length = 0;
    while (length < value.size() && value[length] != '\0') ++length;
    return {value.data(), length};
  }
};

struct WifiConfig {
  FixedSecret<33> ssid;
  FixedSecret<64> password;
};

struct PendingConfig {
  FixedSecret<33> wifi_ssid;
  FixedSecret<64> wifi_password;
  FixedSecret<513> server_url;
  FixedSecret<129> bootstrap_id;
  FixedSecret<17> claim_code;
  FixedSecret<1025> claim_authorization;
  FixedSecret<129> idempotency_key;
};

struct RuntimeConfig {
  FixedSecret<33> wifi_ssid;
  FixedSecret<64> wifi_password;
  FixedSecret<513> server_url;
  FixedSecret<513> device_credential;
};

class ProvisioningStore final {
public:
  PersistedState state() const;
  bool load_pending(PendingConfig& out) const;
  bool load_runtime(RuntimeConfig& out) const;

  // save_pending and commit_runtime write all phase fields plus the phase marker
  // under one NVS handle and call nvs_commit once. A reboot therefore observes
  // either the previous committed phase or the new complete phase. Pending may
  // contain either the one-time human code or, after redemption, only the opaque
  // short-lived claim authorization.
  bool save_pending(const PendingConfig& pending) const;
  bool commit_runtime(const PendingConfig& pending,
                      std::string_view device_credential) const;

  // update_wifi changes only SSID/password while preserving the already-enrolled
  // backend origin and device credential. This is deliberately distinct from a
  // factory reset so changing networks cannot duplicate or transfer ownership.
  bool update_wifi(const WifiConfig& wifi) const;

  bool mark_ready() const;
  bool clear() const;
};

} // namespace companion::provisioning
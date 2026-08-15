#pragma once

#include <array>
#include <string_view>

namespace companion::provisioning {

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

struct PendingConfig {
  FixedSecret<33> wifi_ssid;
  FixedSecret<64> wifi_password;
  FixedSecret<513> server_url;
  FixedSecret<129> bootstrap_id;
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
  // either the previous committed phase or the new complete phase.
  bool save_pending(const PendingConfig& pending) const;
  bool commit_runtime(const PendingConfig& pending,
                      std::string_view device_credential) const;
  bool mark_ready() const;
  bool clear() const;
};

} // namespace companion::provisioning

#pragma once

#include <cstddef>
#include <string_view>

namespace companion::provisioning {

enum class State {
  unprovisioned,
  setup,
  connecting_wifi,
  claiming,
  validating,
  ready,
  retry,
};

enum class Event {
  begin_setup,
  config_received,
  wifi_connected,
  claim_succeeded,
  backend_authenticated,
  retryable_failure,
  reset,
};

struct RuntimeConfigView {
  std::string_view wifi_ssid;
  std::string_view wifi_password;
  std::string_view server_url;
  std::string_view device_credential;
};

struct PendingClaimView {
  std::string_view bootstrap_id;
  std::string_view claim_authorization;
  std::string_view idempotency_key;
  std::string_view server_url;
};

constexpr bool valid_wss_url(std::string_view value) {
  if (!value.starts_with("wss://") || value.size() <= 6 || value.size() > 512 ||
      value.find('?') != std::string_view::npos ||
      value.find('#') != std::string_view::npos ||
      value.find('@') != std::string_view::npos) {
    return false;
  }
  const auto authority = value.substr(6);
  const auto slash = authority.find('/');
  const auto host = authority.substr(0, slash);
  return !host.empty();
}

constexpr bool valid_wifi(std::string_view ssid, std::string_view password) {
  return !ssid.empty() && ssid.size() <= 32 && password.size() <= 63;
}

constexpr bool valid_runtime_config(const RuntimeConfigView &config) {
  return valid_wifi(config.wifi_ssid, config.wifi_password) &&
         valid_wss_url(config.server_url) && !config.device_credential.empty() &&
         config.device_credential.size() <= 512;
}

constexpr bool valid_pending_claim(const PendingClaimView &claim) {
  return !claim.bootstrap_id.empty() && claim.bootstrap_id.size() <= 128 &&
         !claim.claim_authorization.empty() && claim.claim_authorization.size() <= 1024 &&
         claim.idempotency_key.size() >= 8 && claim.idempotency_key.size() <= 128 &&
         valid_wss_url(claim.server_url);
}

// Consumer onboarding uses ten symbols from a deliberately unambiguous alphabet,
// rendered as XXXXX-XXXXX. The backend accepts upper/lower case and ignores the
// single separator, but local validation rejects arbitrary opaque values so the
// setup page cannot accidentally send a long-lived credential-shaped secret.
constexpr bool valid_human_claim_code(std::string_view value) {
  if (value.size() != 10 && value.size() != 11) return false;
  size_t symbols = 0;
  for (const char raw : value) {
    if (raw == '-') {
      if (value.size() != 11 || symbols != 5) return false;
      continue;
    }
    const char c = raw >= 'a' && raw <= 'z' ? static_cast<char>(raw - ('a' - 'A')) : raw;
    const bool valid = (c >= 'A' && c <= 'Z' && c != 'I' && c != 'O') ||
                       (c >= '2' && c <= '9');
    if (!valid) return false;
    ++symbols;
  }
  return symbols == 10;
}

constexpr State transition(State state, Event event) {
  if (event == Event::reset) {
    return State::setup;
  }
  if (event == Event::retryable_failure) {
    switch (state) {
    case State::connecting_wifi:
    case State::claiming:
    case State::validating:
      return State::retry;
    default:
      return state;
    }
  }
  switch (state) {
  case State::unprovisioned:
    return event == Event::begin_setup ? State::setup : state;
  case State::setup:
    return event == Event::config_received ? State::connecting_wifi : state;
  case State::connecting_wifi:
    return event == Event::wifi_connected ? State::claiming : state;
  case State::claiming:
    return event == Event::claim_succeeded ? State::validating : state;
  case State::validating:
    return event == Event::backend_authenticated ? State::ready : state;
  case State::retry:
    return event == Event::begin_setup ? State::setup : state;
  case State::ready:
    return state;
  }
  return state;
}

constexpr bool terminal_ready(State state) { return state == State::ready; }

} // namespace companion::provisioning

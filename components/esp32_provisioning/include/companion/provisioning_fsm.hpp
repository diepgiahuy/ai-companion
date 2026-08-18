#pragma once

#include <cstddef>
#include <string_view>

namespace companion::provisioning {

enum class State {
  unprovisioned,
  setup,
  connecting_wifi,
  create_approval,
  waiting_owner,
  claiming,
  validating,
  ready,
  retry,
};

enum class Event {
  begin_setup,
  config_received,
  wifi_connected,
  session_created,
  owner_approved,
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
  std::string_view device_code;
  std::string_view user_code;
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

constexpr bool valid_user_code(std::string_view value) {
  if (value.empty()) return true; // Optional before session creation
  if (value.size() != 9 || value[4] != '-') return false;
  constexpr std::string_view alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  for (size_t i = 0; i < value.size(); ++i) {
    if (i == 4) continue;
    if (alphabet.find(value[i]) == std::string_view::npos) return false;
  }
  return true;
}

constexpr bool valid_runtime_config(const RuntimeConfigView &config) {
  return valid_wifi(config.wifi_ssid, config.wifi_password) &&
         valid_wss_url(config.server_url) && !config.device_credential.empty() &&
         config.device_credential.size() <= 512;
}

// In zero-typing onboarding, initial configuration holds bootstrap_id,
// idempotency_key and server_url, with empty claim_authorization.
// After owner approval and polling, claim_authorization is populated.
constexpr bool valid_pending_claim(const PendingClaimView &claim) {
  const bool unapproved_phase = claim.claim_authorization.empty() &&
                                claim.device_code.size() <= 128 &&
                                valid_user_code(claim.user_code);
  const bool approved_phase = !claim.claim_authorization.empty() &&
                              claim.claim_authorization.size() <= 1024;
  return !claim.bootstrap_id.empty() && claim.bootstrap_id.size() <= 128 &&
         (unapproved_phase || approved_phase) &&
         claim.idempotency_key.size() >= 8 && claim.idempotency_key.size() <= 128 &&
         valid_wss_url(claim.server_url);
}

constexpr State transition(State state, Event event) {
  if (event == Event::reset) {
    return State::setup;
  }
  if (event == Event::retryable_failure) {
    switch (state) {
    case State::connecting_wifi:
    case State::create_approval:
    case State::waiting_owner:
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
    return event == Event::wifi_connected ? State::create_approval : state;
  case State::create_approval:
    return event == Event::session_created ? State::waiting_owner : state;
  case State::waiting_owner:
    return event == Event::owner_approved ? State::claiming : state;
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

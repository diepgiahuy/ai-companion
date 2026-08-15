#include "companion/provisioning_fsm.hpp"

#include <cassert>

using companion::provisioning::Event;
using companion::provisioning::PendingClaimView;
using companion::provisioning::RuntimeConfigView;
using companion::provisioning::State;
using companion::provisioning::transition;
using companion::provisioning::valid_human_claim_code;
using companion::provisioning::valid_pending_claim;
using companion::provisioning::valid_runtime_config;

int main() {
  auto state = State::unprovisioned;
  state = transition(state, Event::begin_setup);
  assert(state == State::setup);
  state = transition(state, Event::config_received);
  assert(state == State::connecting_wifi);
  state = transition(state, Event::wifi_connected);
  assert(state == State::claiming);
  state = transition(state, Event::claim_succeeded);
  assert(state == State::validating);
  state = transition(state, Event::backend_authenticated);
  assert(state == State::ready);

  assert(transition(State::claiming, Event::retryable_failure) == State::retry);
  assert(transition(State::validating, Event::reset) == State::setup);
  assert(transition(State::ready, Event::retryable_failure) == State::ready);

  assert(valid_runtime_config(RuntimeConfigView{
      .wifi_ssid = "home",
      .wifi_password = "secret123",
      .server_url = "wss://companion.example/v2/device",
      .device_credential = "device-token",
  }));
  assert(!valid_runtime_config(RuntimeConfigView{
      .wifi_ssid = "home",
      .wifi_password = "secret123",
      .server_url = "ws://plaintext.example/v2/device",
      .device_credential = "device-token",
  }));
  assert(!valid_runtime_config(RuntimeConfigView{
      .wifi_ssid = "home",
      .wifi_password = "",
      .server_url = "wss://user@evil.example/v2/device",
      .device_credential = "device-token",
  }));
  assert(!valid_runtime_config(RuntimeConfigView{
      .wifi_ssid = "home",
      .wifi_password = "secret123",
      .server_url = "wss://companion.example/v2/device?token=secret",
      .device_credential = "device-token",
  }));
  assert(!valid_runtime_config(RuntimeConfigView{
      .wifi_ssid = "home",
      .wifi_password = "secret123",
      .server_url = "wss://companion.example/v2/device#fragment",
      .device_credential = "device-token",
  }));

  assert(valid_human_claim_code("ABCDE23456"));
  assert(!valid_human_claim_code("abcde23456"));
  assert(!valid_human_claim_code("ABCDE-23456"));
  assert(!valid_human_claim_code("ABCDE12345")); // 1 is intentionally excluded.

  assert(valid_pending_claim(PendingClaimView{
      .bootstrap_id = "boot-123",
      .claim_code = "ABCDE23456",
      .claim_authorization = "",
      .idempotency_key = "claim-idem-123",
      .server_url = "wss://companion.example/v2/device",
  }));
  assert(valid_pending_claim(PendingClaimView{
      .bootstrap_id = "boot-123",
      .claim_code = "",
      .claim_authorization = "short-lived-authorization",
      .idempotency_key = "claim-idem-123",
      .server_url = "wss://companion.example/v2/device",
  }));
  assert(!valid_pending_claim(PendingClaimView{
      .bootstrap_id = "boot-123",
      .claim_code = "ABCDE23456",
      .claim_authorization = "short-lived-authorization",
      .idempotency_key = "claim-idem-123",
      .server_url = "wss://companion.example/v2/device",
  }));
  assert(!valid_pending_claim(PendingClaimView{
      .bootstrap_id = "boot-123",
      .claim_code = "ABCDE23456",
      .claim_authorization = "",
      .idempotency_key = "short",
      .server_url = "wss://companion.example/v2/device",
  }));

  return 0;
}

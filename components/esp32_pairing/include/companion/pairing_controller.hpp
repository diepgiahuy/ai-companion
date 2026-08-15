#pragma once

#include "companion/nimble_pairing_discovery.hpp"
#include "companion/pairing_fsm.hpp"
#include "companion/websocket_voice_backend.hpp"

#include <cstdint>

namespace companion::pairing {

// Product-path pairing orchestrator. It owns no identity or persistence: radio
// yields opaque aliases, PairingFsm owns bounded local state, and the existing
// authenticated WebSocket backend remains the only server transport.
class PairingController final {
public:
  PairingController(NimblePairingDiscovery& radio,
                    WebSocketVoiceBackend& backend)
      : radio_(radio), backend_(backend), fsm_({.discovery_window_ms = 30'000,
                                               .confirmation_window_ms = 30'000}) {}

  bool start(uint64_t now_ms);
  void tick(uint64_t now_ms);
  bool confirm(uint64_t now_ms);
  void cancel();

  State state() const { return fsm_.state(); }
  StopReason last_stop_reason() const { return fsm_.last_stop_reason(); }
  bool active() const { return fsm_.state() != State::idle; }
  std::string_view pairing_session_id() const { return fsm_.session_id(); }

private:
  static constexpr uint32_t kScanWindowMs = 8'000;

  void handle_backend_event(const PairingBackendEvent& event, uint64_t now_ms);
  void stop_radio();

  NimblePairingDiscovery& radio_;
  WebSocketVoiceBackend& backend_;
  PairingFsm fsm_;
  uint64_t scan_until_ms_{};
};

} // namespace companion::pairing

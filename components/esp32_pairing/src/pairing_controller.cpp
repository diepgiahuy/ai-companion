#include "companion/pairing_controller.hpp"

#include <algorithm>
#include <ctime>

namespace companion::pairing {

bool PairingController::start(uint64_t now_ms) {
  if (active() || !radio_.ready()) return false;
  std::array<char, 20> local_alias{};
  if (!backend_.pairing_discovery_alias(local_alias)) return false;
  if (!fsm_.begin(now_ms, local_alias.data())) return false;
  if (!radio_.start(local_alias.data(), kScanWindowMs)) {
    fsm_.disconnected();
    return false;
  }
  scan_until_ms_ = now_ms + kScanWindowMs;
  return true;
}

void PairingController::tick(uint64_t now_ms) {
  PairingBackendEvent backend_event{};
  while (backend_.poll_pairing_event(backend_event)) {
    handle_backend_event(backend_event, now_ms);
  }
  if (!active()) {
    stop_radio();
    return;
  }

  DiscoveryObservation observation{};
  while (radio_.poll(observation)) {
    // #100 owns calibrated RSSI ranking. For #99 the opaque alias itself is the
    // only bounded evidence reference and ambiguity prevents auto-selection.
    (void)fsm_.observe_candidate(now_ms, observation.id(), observation.id());
  }

  if (fsm_.state() == State::discovering && now_ms >= scan_until_ms_) {
    stop_radio();
    const std::string_view candidate = fsm_.candidate_alias();
    const std::string_view evidence = fsm_.proximity_evidence_id();
    if (!fsm_.commit_candidate(now_ms)) {
      if (fsm_.state() != State::idle) fsm_.cancel();
      return;
    }
    if (!backend_.create_pairing_session(candidate, evidence)) {
      fsm_.disconnected();
      return;
    }
  }

  const State before = fsm_.state();
  fsm_.tick(now_ms);
  if (before != State::idle && fsm_.state() == State::idle) stop_radio();
}

bool PairingController::confirm(uint64_t now_ms) {
  if (fsm_.state() != State::awaiting_confirmation) return false;
  const std::string_view session = fsm_.session_id();
  const std::string_view nonce = fsm_.confirmation_nonce();
  if (!fsm_.confirm(now_ms, session)) return false;
  if (!backend_.confirm_pairing_session(session, nonce)) {
    fsm_.disconnected();
    return false;
  }
  return true;
}

void PairingController::cancel() {
  if (!active()) return;
  const std::string_view session = fsm_.session_id();
  if (!session.empty()) (void)backend_.reject_pairing_session(session);
  stop_radio();
  fsm_.cancel();
}

void PairingController::handle_backend_event(const PairingBackendEvent& event,
                                             uint64_t now_ms) {
  switch (event.type) {
  case PairingBackendEventType::session_created: {
    const std::time_t wall = std::time(nullptr);
    if (wall < 1'577'836'800) {
      fsm_.disconnected();
      stop_radio();
      return;
    }
    const uint64_t wall_ms = static_cast<uint64_t>(wall) * 1'000ULL;
    if (event.expires_at_unix_ms <= wall_ms) {
      // A peer can receive this while still in discovery and therefore has no
      // local session ID to match. Expire the active local attempt directly.
      fsm_.tick(fsm_.deadline_ms());
      stop_radio();
      return;
    }
    // Translate server wall-clock expiry into the FSM's monotonic domain and
    // cap it to the backend's two-minute contract.
    const uint64_t remaining = std::min<uint64_t>(
        event.expires_at_unix_ms - wall_ms, 120'000ULL);
    if (!fsm_.session_created(now_ms, event.session_id_view(),
                              event.confirmation_nonce_view(),
                              now_ms + remaining)) {
      stop_radio();
      return;
    }
    stop_radio();
    break;
  }
  case PairingBackendEventType::succeeded:
    if (!fsm_.server_success(now_ms, event.session_id_view())) fsm_.disconnected();
    stop_radio();
    break;
  case PairingBackendEventType::rejected:
    if (!fsm_.server_rejected(event.session_id_view())) fsm_.disconnected();
    stop_radio();
    break;
  case PairingBackendEventType::expired:
    if (!fsm_.server_expired(event.session_id_view())) fsm_.disconnected();
    stop_radio();
    break;
  case PairingBackendEventType::disconnected:
    fsm_.disconnected();
    stop_radio();
    break;
  }
}

void PairingController::stop_radio() {
  if (radio_.active()) radio_.stop();
  scan_until_ms_ = 0;
}

} // namespace companion::pairing

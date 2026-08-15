#include "companion/pairing_fsm.hpp"

#include <cassert>

using companion::pairing::PairingFsm;
using companion::pairing::State;
using companion::pairing::StopReason;

int main() {
  PairingFsm fsm;
  assert(fsm.begin(1'000, "local-opaque-0001"));
  assert(fsm.state() == State::discovering);
  assert(!fsm.observe_candidate(1'100, "local-opaque-0001", "evidence-self"));
  assert(fsm.observe_candidate(1'200, "peer-opaque-00001", "evidence-1"));
  assert(fsm.state() == State::session_pending);
  assert(!fsm.observe_candidate(1'300, "other-opaque-0001", "evidence-2"));

  assert(fsm.session_created(1'400, "peer-opaque-00001", "session-1",
                             "participant-nonce-123456", 20'000));
  assert(fsm.state() == State::awaiting_confirmation);
  assert(!fsm.confirm(1'500, "stale-session"));
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::invalid_event);

  assert(fsm.begin(2'000, "local-opaque-0002"));
  assert(fsm.observe_candidate(2'100, "peer-opaque-00002", "evidence-3"));
  assert(fsm.session_created(2'200, "peer-opaque-00002", "session-2",
                             "participant-nonce-223456", 25'000));
  assert(fsm.confirm(2'300, "session-2"));
  assert(fsm.state() == State::confirming);
  assert(fsm.server_success(2'400, "session-2"));
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::success);

  assert(fsm.begin(3'000, "local-opaque-0003"));
  fsm.cancel();
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::cancelled);

  assert(fsm.begin(4'000, "local-opaque-0004"));
  fsm.disconnected();
  assert(fsm.last_stop_reason() == StopReason::disconnected);

  assert(fsm.begin(5'000, "local-opaque-0005"));
  fsm.tick(35'000);
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::timed_out);

  assert(fsm.begin(40'000, "local-opaque-0006"));
  assert(fsm.observe_candidate(40'100, "peer-opaque-00006", "evidence-6"));
  assert(fsm.session_created(40'200, "peer-opaque-00006", "session-6",
                             "participant-nonce-623456", 41'000));
  fsm.tick(41'000);
  assert(fsm.last_stop_reason() == StopReason::timed_out);

  return 0;
}

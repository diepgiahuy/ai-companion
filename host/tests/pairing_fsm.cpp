#include "companion/pairing_fsm.hpp"

#include <cassert>

using companion::pairing::PairingFsm;
using companion::pairing::State;
using companion::pairing::StopReason;

int main() {
  PairingFsm fsm;
  assert(fsm.begin(1'000, "CP-AAAAAAAAAAAAAAAA"));
  assert(fsm.state() == State::discovering);
  assert(!fsm.observe_candidate(1'100, "CP-AAAAAAAAAAAAAAAA", "evidence-self"));
  assert(fsm.observe_candidate(1'200, "CP-BBBBBBBBBBBBBBBB", "evidence-1"));
  assert(fsm.state() == State::discovering);
  assert(fsm.commit_candidate(1'300));
  assert(fsm.state() == State::session_pending);

  assert(fsm.session_created(1'400, "session-1",
                             "participant-nonce-123456", 20'000));
  assert(fsm.state() == State::awaiting_confirmation);
  assert(!fsm.confirm(1'500, "stale-session"));
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::invalid_event);

  assert(fsm.begin(2'000, "CP-CCCCCCCCCCCCCCCC"));
  assert(fsm.observe_candidate(2'100, "CP-DDDDDDDDDDDDDDDD", "evidence-3"));
  assert(fsm.commit_candidate(2'150));
  assert(fsm.session_created(2'200, "session-2",
                             "participant-nonce-223456", 25'000));
  assert(fsm.confirm(2'300, "session-2"));
  assert(fsm.state() == State::confirming);
  assert(fsm.server_success(2'400, "session-2"));
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::success);

  PairingFsm ambiguous;
  assert(ambiguous.begin(3'000, "CP-EEEEEEEEEEEEEEEE"));
  assert(ambiguous.observe_candidate(3'100, "CP-FFFFFFFFFFFFFFFF", "evidence-a"));
  assert(!ambiguous.observe_candidate(3'200, "CP-GGGGGGGGGGGGGGGG", "evidence-b"));
  assert(ambiguous.ambiguous());
  assert(!ambiguous.commit_candidate(3'300));
  assert(ambiguous.state() == State::idle);
  assert(ambiguous.last_stop_reason() == StopReason::ambiguous);

  assert(fsm.begin(4'000, "CP-HHHHHHHHHHHHHHHH"));
  fsm.cancel();
  assert(fsm.state() == State::idle);
  assert(fsm.last_stop_reason() == StopReason::cancelled);

  assert(fsm.begin(5'000, "CP-JJJJJJJJJJJJJJJJ"));
  fsm.disconnected();
  assert(fsm.last_stop_reason() == StopReason::disconnected);

  PairingFsm timeout;
  assert(timeout.begin(6'000, "CP-KKKKKKKKKKKKKKKK"));
  timeout.tick(36'000);
  assert(timeout.state() == State::idle);
  assert(timeout.last_stop_reason() == StopReason::timed_out);

  PairingFsm expiry;
  assert(expiry.begin(40'000, "CP-LLLLLLLLLLLLLLLL"));
  assert(expiry.observe_candidate(40'100, "CP-MMMMMMMMMMMMMMMM", "evidence-6"));
  assert(expiry.commit_candidate(40'150));
  assert(expiry.session_created(40'200, "session-6",
                                "participant-nonce-623456", 41'000));
  expiry.tick(41'000);
  assert(expiry.last_stop_reason() == StopReason::timed_out);

  PairingFsm invalid_window({.discovery_window_ms = 60'001, .confirmation_window_ms = 30'000});
  assert(!invalid_window.begin(0, "CP-NNNNNNNNNNNNNNNN"));

  return 0;
}

#include "companion/pairing_fsm.hpp"

#include <cassert>
#include <string>

using companion::pairing::Fsm;
using companion::pairing::State;
using companion::pairing::kMaximumObservations;

int main() {
  Fsm initiator;
  assert(initiator.start(1'000, 60'000));
  assert(initiator.state() == State::discovering);
  assert(initiator.observe("CP-ABCDEFGHIJKLMNOP", -42, 1'100));
  assert(initiator.observe("CP-ABCDEFGHIJKLMNOP", -40, 1'200));
  assert(initiator.observation_count() == 1);
  assert(initiator.unique_candidate() == "CP-ABCDEFGHIJKLMNOP");
  assert(initiator.request_session(1'300));
  assert(initiator.state() == State::awaiting_session);
  assert(initiator.session_created("session-1", "nonce-1", 30'000, 1'400));
  assert(initiator.state() == State::awaiting_confirmation);
  assert(initiator.confirmation_nonce() == "nonce-1");
  assert(initiator.confirm(1'500));
  assert(initiator.succeeded("session-1", 1'600));
  assert(initiator.state() == State::succeeded);

  Fsm ambiguous;
  assert(ambiguous.start(0));
  assert(ambiguous.observe("CP-ABCDEFGHIJKLMNOP", -20, 10));
  assert(ambiguous.observe("CP-QRSTUVWXYZ234567", -10, 11));
  assert(ambiguous.unique_candidate().empty());
  assert(!ambiguous.request_session(12));

  Fsm bounded;
  assert(bounded.start(0));
  for (size_t i = 0; i < kMaximumObservations; ++i) {
    std::string id = "CP-AAAAAAAAAAAAAAA";
    id.back() = static_cast<char>('A' + i);
    assert(bounded.observe(id, -50, 100 + i));
  }
  assert(bounded.observation_count() == kMaximumObservations);
  assert(!bounded.observe("CP-ZZZZZZZZZZZZZZZZ", -5, 200));
  assert(bounded.overflowed());
  assert(bounded.unique_candidate().empty());

  Fsm timeout;
  assert(timeout.start(100, 500));
  timeout.tick(600);
  assert(timeout.state() == State::expired);
  assert(timeout.terminal());

  Fsm stale;
  assert(stale.start(0));
  assert(stale.observe("CP-ABCDEFGHIJKLMNOP", -30, 1));
  assert(stale.request_session(2));
  assert(stale.session_created("session-live", "nonce-live", 1'000, 3));
  assert(!stale.succeeded("other-session", 4));
  assert(stale.state() == State::awaiting_confirmation);
  stale.tick(1'000);
  assert(stale.state() == State::expired);
  assert(!stale.confirm(1'001));

  Fsm peer;
  assert(peer.start(10));
  assert(peer.session_created("peer-session", "peer-nonce", 5'000, 20));
  assert(peer.state() == State::awaiting_confirmation);
  peer.disconnected();
  assert(peer.state() == State::disconnected);

  return 0;
}

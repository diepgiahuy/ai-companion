#include "companion/reconnect_backoff.hpp"

#include <cassert>

int main() {
  using companion::ReconnectBackoff;

  ReconnectBackoff backoff;
  assert(backoff.attempt() == 0);
  assert(backoff.next_delay_ms(0) == 500);
  assert(backoff.next_delay_ms(250) == 1'250);
  assert(backoff.next_delay_ms(0) == 2'000);
  assert(backoff.next_delay_ms(0) == 4'000);
  assert(backoff.next_delay_ms(0) == 8'000);
  assert(backoff.next_delay_ms(0) == 16'000);
  assert(backoff.next_delay_ms(0) == 30'000);
  assert(backoff.next_delay_ms(250) == 30'000);
  assert(backoff.attempt() == 8);

  backoff.reset();
  assert(backoff.attempt() == 0);
  assert(backoff.next_delay_ms(251) == 500); // jitter wraps into the bounded window.

  return 0;
}

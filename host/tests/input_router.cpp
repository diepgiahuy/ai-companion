#include "companion/input_router.hpp"

#include <cassert>

using namespace companion;

namespace {

InputContext normal_context() {
  return InputContext{
      .confirmation_pending = false,
      .pairing_available = true,
      .pairing_active = false,
      .pairing_awaiting_confirmation = false,
      .pairing_start_allowed = true,
  };
}

void destructive_confirmation_has_priority() {
  InputContext context = normal_context();
  context.confirmation_pending = true;
  assert(InputRouter::route({.short_press = true}, context) ==
         InputIntent::confirm_destructive_action);
  assert(InputRouter::route({.pairing_hold = true}, context) ==
         InputIntent::none);
  assert(InputRouter::route({.short_press = true, .pairing_hold = true}, context) ==
         InputIntent::confirm_destructive_action);
}

void active_pairing_owns_pairing_inputs() {
  InputContext context = normal_context();
  context.pairing_active = true;
  context.pairing_start_allowed = false;

  assert(InputRouter::route({.pairing_hold = true}, context) ==
         InputIntent::cancel_pairing);
  assert(InputRouter::route({.short_press = true}, context) ==
         InputIntent::cancel_pairing);

  context.pairing_awaiting_confirmation = true;
  assert(InputRouter::route({.short_press = true}, context) ==
         InputIntent::confirm_pairing);
  assert(InputRouter::route({.short_press = true, .pairing_hold = true}, context) ==
         InputIntent::cancel_pairing);
}

void pairing_hold_only_starts_when_allowed() {
  InputContext context = normal_context();
  assert(InputRouter::route({.pairing_hold = true}, context) ==
         InputIntent::begin_pairing);

  context.pairing_start_allowed = false;
  assert(InputRouter::route({.pairing_hold = true}, context) == InputIntent::none);

  context = normal_context();
  context.pairing_available = false;
  assert(InputRouter::route({.pairing_hold = true}, context) == InputIntent::none);
}

void short_press_routes_to_primary_action() {
  const InputContext context = normal_context();
  assert(InputRouter::route({.short_press = true}, context) ==
         InputIntent::primary_action);
  assert(InputRouter::route({}, context) == InputIntent::none);
}

void primary_action_queue_is_bounded_and_single_consumer() {
  InputRouter router;
  assert(!router.primary_action_pending());
  assert(!router.queue_primary_action(InputIntent::begin_pairing));
  assert(router.queue_primary_action(InputIntent::primary_action));
  assert(router.primary_action_pending());
  assert(!router.queue_primary_action(InputIntent::primary_action));
  assert(router.consume_primary_action());
  assert(!router.primary_action_pending());
  assert(!router.consume_primary_action());
  assert(router.queue_primary_action(InputIntent::primary_action));
  assert(router.consume_primary_action());
}

} // namespace

int main() {
  destructive_confirmation_has_priority();
  active_pairing_owns_pairing_inputs();
  pairing_hold_only_starts_when_allowed();
  short_press_routes_to_primary_action();
  primary_action_queue_is_bounded_and_single_consumer();
  return 0;
}

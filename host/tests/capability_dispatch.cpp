#include "companion/capability_dispatch.hpp"

#include <cassert>

using namespace companion;

int main() {
  using protocol::ControlType;

  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.user_confirmation", "1", true) ==
         CapabilityDispatch::user_confirmation_call);
  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.user_confirmation", "2", true) ==
         CapabilityDispatch::unsupported_call);
  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.volume.set", "1", true) ==
         CapabilityDispatch::unsupported_call);
  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.user_confirmation", "1", false) ==
         CapabilityDispatch::unsupported_call);

  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.settings_v1", "1", true) ==
         CapabilityDispatch::settings_call);
  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.settings_v1", "2", true) ==
         CapabilityDispatch::unsupported_call);
  assert(select_capability_dispatch(
             ControlType::capability_call,
             "device.settings_v1", "1", true, false) ==
         CapabilityDispatch::unsupported_call);

  // Cancellation is correlation-scoped, not capability-name scoped. Once the
  // user-confirmation plane is enabled the single parser gives its active
  // request handler first ownership; otherwise a cancel is harmlessly consumed.
  assert(select_capability_dispatch(
             ControlType::capability_cancel, {}, {}, true) ==
         CapabilityDispatch::user_confirmation_cancel);
  assert(select_capability_dispatch(
             ControlType::capability_cancel, {}, {}, false) ==
         CapabilityDispatch::ignored_cancel);

  assert(select_capability_dispatch(
             ControlType::alarm_ack, {}, {}, true) ==
         CapabilityDispatch::not_capability);
  assert(select_capability_dispatch(
             ControlType::pairing_confirmation, {}, {}, true) ==
         CapabilityDispatch::not_capability);
  return 0;
}

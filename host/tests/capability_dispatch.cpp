#include "companion/capability_dispatch.hpp"

#include <cassert>

using namespace companion;

int main() {
  using protocol::ControlType;

  CapabilityRegistry registry{};
  assert(registry.add(CapabilityDefinition{
      .name = "device.test",
      .version = "1",
      .kind = "command",
      .handler = CapabilityHandler::settings,
      .cancelable = false,
  }));
  assert(!registry.add(CapabilityDefinition{
      .name = "device.test",
      .version = "1",
      .kind = "command",
      .handler = CapabilityHandler::settings,
      .cancelable = false,
  }));
  assert(registry.size() == 1);
  assert(registry.find("device.test", "1") != nullptr);
  assert(registry.find("device.test", "2") == nullptr);

  const auto enabled = make_capability_registry(true);
  assert(enabled.size() == 2);
  const auto* settings =
      enabled.find(kSettingsCapability, kSettingsCapabilityVersion);
  assert(settings != nullptr && !settings->cancelable &&
         settings->handler == CapabilityHandler::settings);
  const auto* confirmation = enabled.find(
      kUserConfirmationCapability, kUserConfirmationCapabilityVersion);
  assert(confirmation != nullptr && confirmation->cancelable &&
         confirmation->handler == CapabilityHandler::user_confirmation);

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

  const PendingCapabilityOperation pending{
      .active = true,
      .handler = CapabilityHandler::user_confirmation,
      .cancelable = true,
      .correlation_id = "cap-1",
      .turn_id = "turn-1",
      .generation_id = 7,
  };
  assert(select_capability_cancel(pending, "cap-1", "turn-1", 7) ==
         CapabilityDispatch::user_confirmation_cancel);
  assert(select_capability_cancel(pending, "cap-other", "turn-1", 7) ==
         CapabilityDispatch::ignored_cancel);
  assert(select_capability_cancel(pending, "cap-1", "turn-other", 7) ==
         CapabilityDispatch::ignored_cancel);
  assert(select_capability_cancel(pending, "cap-1", "turn-1", 8) ==
         CapabilityDispatch::ignored_cancel);

  auto not_cancelable = pending;
  not_cancelable.cancelable = false;
  assert(select_capability_cancel(not_cancelable, "cap-1", "turn-1", 7) ==
         CapabilityDispatch::ignored_cancel);

  assert(select_capability_dispatch(
             ControlType::capability_cancel, {}, {}, true) ==
         CapabilityDispatch::ignored_cancel);
  assert(select_capability_dispatch(
             ControlType::alarm_ack, {}, {}, true) ==
         CapabilityDispatch::not_capability);
  assert(select_capability_dispatch(
             ControlType::pairing_confirmation, {}, {}, true) ==
         CapabilityDispatch::not_capability);
  return 0;
}

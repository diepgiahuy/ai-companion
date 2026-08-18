#pragma once

#include "companion/wire_protocol.hpp"

#include <cstdint>
#include <string_view>

namespace companion {

inline constexpr std::string_view kUserConfirmationCapability =
    "device.user_confirmation";
inline constexpr std::string_view kUserConfirmationCapabilityVersion = "1";

inline constexpr std::string_view kSettingsCapability = "device.settings_v1";
inline constexpr std::string_view kSettingsCapabilityVersion = "1";

enum class CapabilityDispatch : uint8_t {
  not_capability,
  user_confirmation_call,
  user_confirmation_cancel,
  settings_call,
  unsupported_call,
  ignored_cancel,
};

// Pure policy used by the firmware transport parser. Transport framing owns one
// WebSocket receive/reassembly path; this selector only decides which local
// capability handler, if any, owns an already-parsed capability envelope.
constexpr CapabilityDispatch select_capability_dispatch(
    protocol::ControlType type, std::string_view name,
    std::string_view version, bool user_confirmation_enabled,
    bool settings_enabled = true) {
  if (type == protocol::ControlType::capability_call) {
    if (user_confirmation_enabled && name == kUserConfirmationCapability &&
        version == kUserConfirmationCapabilityVersion) {
      return CapabilityDispatch::user_confirmation_call;
    }
    if (settings_enabled && name == kSettingsCapability &&
        version == kSettingsCapabilityVersion) {
      return CapabilityDispatch::settings_call;
    }
    return CapabilityDispatch::unsupported_call;
  }
  if (type == protocol::ControlType::capability_cancel) {
    return user_confirmation_enabled
               ? CapabilityDispatch::user_confirmation_cancel
               : CapabilityDispatch::ignored_cancel;
  }
  return CapabilityDispatch::not_capability;
}

} // namespace companion

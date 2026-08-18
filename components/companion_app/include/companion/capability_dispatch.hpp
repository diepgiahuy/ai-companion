#pragma once

#include "companion/wire_protocol.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>

namespace companion {

inline constexpr std::string_view kUserConfirmationCapability =
    "device.user_confirmation";
inline constexpr std::string_view kUserConfirmationCapabilityVersion = "1";

inline constexpr std::string_view kSettingsCapability = "device.settings_v1";
inline constexpr std::string_view kSettingsCapabilityVersion = "1";

inline constexpr size_t kMaximumDeviceCapabilities = 8;

enum class CapabilityHandler : uint8_t {
  user_confirmation,
  settings,
};

struct CapabilityDefinition {
  std::string_view name{};
  std::string_view version{};
  std::string_view kind{"command"};
  CapabilityHandler handler{CapabilityHandler::settings};
  bool cancelable{};
};

class CapabilityRegistry {
public:
  constexpr bool add(CapabilityDefinition definition) {
    if (definition.name.empty() || definition.version.empty() ||
        definition.kind != "command" || size_ >= definitions_.size()) {
      return false;
    }
    if (find(definition.name, definition.version) != nullptr) return false;
    definitions_[size_++] = definition;
    return true;
  }

  constexpr const CapabilityDefinition* find(std::string_view name,
                                             std::string_view version) const {
    for (size_t index = 0; index < size_; ++index) {
      if (definitions_[index].name == name &&
          definitions_[index].version == version) {
        return &definitions_[index];
      }
    }
    return nullptr;
  }

  constexpr size_t size() const { return size_; }
  constexpr const CapabilityDefinition& operator[](size_t index) const {
    return definitions_[index];
  }

private:
  std::array<CapabilityDefinition, kMaximumDeviceCapabilities> definitions_{};
  size_t size_{};
};

constexpr CapabilityRegistry make_capability_registry(
    bool user_confirmation_enabled, bool settings_enabled = true) {
  CapabilityRegistry registry{};
  if (settings_enabled) {
    (void)registry.add(CapabilityDefinition{
        .name = kSettingsCapability,
        .version = kSettingsCapabilityVersion,
        .kind = "command",
        .handler = CapabilityHandler::settings,
        .cancelable = false,
    });
  }
  if (user_confirmation_enabled) {
    (void)registry.add(CapabilityDefinition{
        .name = kUserConfirmationCapability,
        .version = kUserConfirmationCapabilityVersion,
        .kind = "command",
        .handler = CapabilityHandler::user_confirmation,
        .cancelable = true,
    });
  }
  return registry;
}

enum class CapabilityDispatch : uint8_t {
  not_capability,
  user_confirmation_call,
  user_confirmation_cancel,
  settings_call,
  unsupported_call,
  ignored_cancel,
};

constexpr CapabilityDispatch select_capability_call(
    const CapabilityRegistry& registry, std::string_view name,
    std::string_view version) {
  const CapabilityDefinition* definition = registry.find(name, version);
  if (definition == nullptr) return CapabilityDispatch::unsupported_call;
  switch (definition->handler) {
  case CapabilityHandler::user_confirmation:
    return CapabilityDispatch::user_confirmation_call;
  case CapabilityHandler::settings:
    return CapabilityDispatch::settings_call;
  }
  return CapabilityDispatch::unsupported_call;
}

struct PendingCapabilityOperation {
  bool active{};
  CapabilityHandler handler{CapabilityHandler::settings};
  bool cancelable{};
  std::string_view correlation_id{};
  std::string_view turn_id{};
  uint64_t generation_id{};
};

constexpr CapabilityDispatch select_capability_cancel(
    const PendingCapabilityOperation& pending, std::string_view correlation_id,
    std::string_view turn_id, uint64_t generation_id) {
  if (!pending.active || !pending.cancelable || correlation_id.empty() ||
      turn_id.empty() || generation_id == 0 ||
      pending.correlation_id != correlation_id || pending.turn_id != turn_id ||
      pending.generation_id != generation_id) {
    return CapabilityDispatch::ignored_cancel;
  }
  switch (pending.handler) {
  case CapabilityHandler::user_confirmation:
    return CapabilityDispatch::user_confirmation_cancel;
  case CapabilityHandler::settings:
    return CapabilityDispatch::ignored_cancel;
  }
  return CapabilityDispatch::ignored_cancel;
}

// Pure call policy used by tests and transport code. Calls are resolved through
// the same bounded registry used for advertisement. Cancellation is deliberately
// separate because capability.cancel identifies an active operation by
// correlation/turn/generation rather than by capability name.
constexpr CapabilityDispatch select_capability_dispatch(
    protocol::ControlType type, std::string_view name,
    std::string_view version, bool user_confirmation_enabled,
    bool settings_enabled = true) {
  if (type == protocol::ControlType::capability_call) {
    return select_capability_call(
        make_capability_registry(user_confirmation_enabled, settings_enabled),
        name, version);
  }
  if (type == protocol::ControlType::capability_cancel) {
    return CapabilityDispatch::ignored_cancel;
  }
  return CapabilityDispatch::not_capability;
}

} // namespace companion

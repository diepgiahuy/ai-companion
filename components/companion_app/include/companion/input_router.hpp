#pragma once

#include <cstdint>

namespace companion {

enum class InputIntent : uint8_t {
  none,
  primary_action,
  confirm_destructive_action,
  begin_pairing,
  confirm_pairing,
  cancel_pairing,
};

struct InputSample {
  bool short_press{};
  bool pairing_hold{};
};

struct InputContext {
  bool confirmation_pending{};
  bool pairing_available{};
  bool pairing_active{};
  bool pairing_awaiting_confirmation{};
  bool pairing_start_allowed{};
};

// Renderer- and hardware-neutral routing for the single physical interaction
// surface. Gesture detection stays in the board adapter; this class decides
// which semantic owner receives the gesture and never performs a mutation.
class InputRouter final {
public:
  static constexpr InputIntent route(InputSample sample, InputContext context) {
    // A fresh destructive confirmation owns the short action exclusively. A
    // hold must never fall through into pairing while confirmation is active.
    if (context.confirmation_pending) {
      return sample.short_press ? InputIntent::confirm_destructive_action
                                : InputIntent::none;
    }

    if (context.pairing_active) {
      if (sample.pairing_hold) return InputIntent::cancel_pairing;
      if (sample.short_press) {
        return context.pairing_awaiting_confirmation
                   ? InputIntent::confirm_pairing
                   : InputIntent::cancel_pairing;
      }
      return InputIntent::none;
    }

    if (sample.pairing_hold && context.pairing_available &&
        context.pairing_start_allowed) {
      return InputIntent::begin_pairing;
    }

    if (sample.short_press) return InputIntent::primary_action;
    return InputIntent::none;
  }

  // CompanionApp still has a temporary Button compatibility seam during the
  // #228 cutover. Queue only the already-routed semantic primary action so the
  // app can interpret it after backend events, preserving the previous tick
  // ordering without giving it direct access to the physical button.
  bool queue_primary_action(InputIntent intent) {
    if (intent != InputIntent::primary_action || primary_action_pending_) {
      return false;
    }
    primary_action_pending_ = true;
    return true;
  }

  bool consume_primary_action() {
    if (!primary_action_pending_) return false;
    primary_action_pending_ = false;
    return true;
  }

  bool primary_action_pending() const { return primary_action_pending_; }

private:
  bool primary_action_pending_{};
};

} // namespace companion

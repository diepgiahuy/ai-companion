#pragma once

#include <cstdint>

namespace companion {

// Hardware-independent one-button gesture splitter. A short action is emitted
// only after release, which lets a longer hold be claimed without first leaking
// the same physical press into the normal PTT path.
class PressGesture final {
public:
  struct Config {
    uint32_t debounce_ms{30};
    uint32_t hold_ms{2'000};
  };

  constexpr PressGesture() = default;
  explicit constexpr PressGesture(Config config) : config_(config) {}

  void reset(bool raw_pressed, uint64_t now_ms) {
    raw_pressed_ = raw_pressed;
    stable_pressed_ = raw_pressed;
    changed_at_ms_ = now_ms;
    pressed_at_ms_ = raw_pressed ? now_ms : 0;
    short_ready_ = false;
    hold_ready_ = false;
    hold_claimed_ = false;
  }

  // Discard any action already in progress and, if currently pressed, claim
  // that press until release. Security-sensitive prompts use this so approval
  // must come from a fresh press that begins after the prompt is visible; the
  // discarded press also cannot later leak into PTT or pairing-hold behavior.
  void suppress_current_press(bool raw_pressed, uint64_t now_ms) {
    reset(raw_pressed, now_ms);
    hold_claimed_ = raw_pressed;
  }

  void sample(bool raw_pressed, uint64_t now_ms) {
    if (raw_pressed != raw_pressed_) {
      raw_pressed_ = raw_pressed;
      changed_at_ms_ = now_ms;
    }

    if (stable_pressed_ != raw_pressed_ &&
        now_ms - changed_at_ms_ >= config_.debounce_ms) {
      stable_pressed_ = raw_pressed_;
      if (stable_pressed_) {
        pressed_at_ms_ = now_ms;
        hold_claimed_ = false;
        hold_ready_ = false;
      } else {
        if (!hold_claimed_) short_ready_ = true;
        pressed_at_ms_ = 0;
      }
    }

    if (stable_pressed_ && !hold_claimed_ && config_.hold_ms > 0 &&
        now_ms - pressed_at_ms_ >= config_.hold_ms) {
      hold_claimed_ = true;
      hold_ready_ = true;
      short_ready_ = false;
    }
  }

  bool consume_short() {
    if (!short_ready_) return false;
    short_ready_ = false;
    return true;
  }

  bool consume_hold() {
    if (!hold_ready_) return false;
    hold_ready_ = false;
    return true;
  }

  bool pressed() const { return stable_pressed_; }
  bool hold_claimed() const { return hold_claimed_; }

private:
  Config config_{};
  bool raw_pressed_{};
  bool stable_pressed_{};
  bool short_ready_{};
  bool hold_ready_{};
  bool hold_claimed_{};
  uint64_t changed_at_ms_{};
  uint64_t pressed_at_ms_{};
};

} // namespace companion

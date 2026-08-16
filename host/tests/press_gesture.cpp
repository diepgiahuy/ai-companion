#include "companion/press_gesture.hpp"

#include <cassert>

using companion::PressGesture;

int main() {
  {
    PressGesture gesture({.debounce_ms = 30, .hold_ms = 2'000});
    gesture.reset(false, 0);
    gesture.sample(true, 10);
    gesture.sample(true, 40);
    assert(!gesture.consume_short());
    assert(!gesture.consume_hold());
    gesture.sample(false, 200);
    gesture.sample(false, 230);
    assert(gesture.consume_short());
    assert(!gesture.consume_short());
    assert(!gesture.consume_hold());
  }

  {
    PressGesture gesture({.debounce_ms = 30, .hold_ms = 2'000});
    gesture.reset(false, 0);
    gesture.sample(true, 10);
    gesture.sample(true, 40);
    gesture.sample(true, 2'039);
    assert(!gesture.consume_hold());
    gesture.sample(true, 2'040);
    assert(gesture.consume_hold());
    assert(!gesture.consume_hold());
    gesture.sample(false, 2'100);
    gesture.sample(false, 2'130);
    assert(!gesture.consume_short());
  }

  {
    // Bounce never becomes a short action unless the new level is stable for
    // the configured debounce interval.
    PressGesture gesture({.debounce_ms = 30, .hold_ms = 2'000});
    gesture.reset(false, 0);
    gesture.sample(true, 10);
    gesture.sample(false, 20);
    gesture.sample(true, 25);
    gesture.sample(false, 35);
    gesture.sample(false, 100);
    assert(!gesture.consume_short());
  }

  return 0;
}

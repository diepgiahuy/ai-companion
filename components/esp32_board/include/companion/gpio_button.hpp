#pragma once

#include "companion/app.hpp"

namespace companion {

class GpioButton final : public Button {
public:
  bool initialize();
  bool consume_press(uint64_t now_ms) override;
  bool is_pressed() const;

private:
  static constexpr uint32_t debounce_ms_{30};
  int raw_level_{1};
  int stable_level_{1};
  uint64_t changed_at_ms_{};
};

} // namespace companion

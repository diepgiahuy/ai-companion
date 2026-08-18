#pragma once

namespace companion {

class GpioButton final {
public:
  bool initialize();
  bool is_pressed() const;

private:
  int raw_level_{1};
  int stable_level_{1};
};

} // namespace companion

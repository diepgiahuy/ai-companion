#pragma once

#include "companion/app.hpp"

#include "driver/i2c_master.h"

#include <array>

namespace companion {

class Ssd1306Display final : public Display {
public:
  ~Ssd1306Display() override;
  bool initialize();
  void show(UiState state, std::string_view text) override;

private:
  static constexpr size_t width_{128};
  static constexpr size_t height_{32};
  i2c_master_bus_handle_t bus_{};
  i2c_master_dev_handle_t device_{};
  std::array<uint8_t, width_ * height_ / 8> pixels_{};

  bool command(std::span<const uint8_t> bytes);
  bool flush();
  void clear();
  void draw_text(size_t x, size_t y, std::string_view text);
  void draw_text_scaled(size_t x, size_t y, std::string_view text, size_t scale);
  void draw_char(size_t x, size_t y, char value);
  void draw_char_scaled(size_t x, size_t y, char value, size_t scale);
};

} // namespace companion

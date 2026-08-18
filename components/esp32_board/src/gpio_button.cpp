#include "companion/gpio_button.hpp"

#include "companion/board_pins.hpp"

#include "driver/gpio.h"

namespace companion {

bool GpioButton::initialize() {
  gpio_config_t config{};
  config.pin_bit_mask = 1ULL << board::kButton;
  config.mode = GPIO_MODE_INPUT;
  config.pull_up_en = GPIO_PULLUP_ENABLE;
  config.pull_down_en = GPIO_PULLDOWN_DISABLE;
  config.intr_type = GPIO_INTR_DISABLE;
  if (gpio_config(&config) != ESP_OK) return false;
  raw_level_ = stable_level_ = gpio_get_level(board::kButton);
  return true;
}

bool GpioButton::is_pressed() const {
  return gpio_get_level(board::kButton) == 0;
}

} // namespace companion

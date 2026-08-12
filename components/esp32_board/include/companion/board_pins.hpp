#pragma once

#include "driver/gpio.h"

namespace companion::board {

// Breadboard pin map. Keep this file as the single source of truth.
inline constexpr gpio_num_t kMicWs = GPIO_NUM_4;
inline constexpr gpio_num_t kMicBclk = GPIO_NUM_5;
inline constexpr gpio_num_t kMicData = GPIO_NUM_6;

inline constexpr gpio_num_t kAmpData = GPIO_NUM_7;
inline constexpr gpio_num_t kAmpBclk = GPIO_NUM_15;
inline constexpr gpio_num_t kAmpLrc = GPIO_NUM_16;

inline constexpr gpio_num_t kButton = GPIO_NUM_40;
inline constexpr gpio_num_t kOledSda = GPIO_NUM_41;
inline constexpr gpio_num_t kOledScl = GPIO_NUM_42;
inline constexpr uint8_t kOledAddress = 0x3C;

} // namespace companion::board

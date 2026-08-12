#include "companion/ssd1306_display.hpp"

#include "companion/board_pins.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cstdio>
#include <ctime>

namespace companion {
namespace {

// Five-column uppercase font: A-Z followed by 0-9.
constexpr std::array<std::array<uint8_t, 5>, 36> kFont{{
  {{0x7e,0x11,0x11,0x11,0x7e}}, {{0x7f,0x49,0x49,0x49,0x36}},
  {{0x3e,0x41,0x41,0x41,0x22}}, {{0x7f,0x41,0x41,0x22,0x1c}},
  {{0x7f,0x49,0x49,0x49,0x41}}, {{0x7f,0x09,0x09,0x09,0x01}},
  {{0x3e,0x41,0x49,0x49,0x7a}}, {{0x7f,0x08,0x08,0x08,0x7f}},
  {{0x00,0x41,0x7f,0x41,0x00}}, {{0x20,0x40,0x41,0x3f,0x01}},
  {{0x7f,0x08,0x14,0x22,0x41}}, {{0x7f,0x40,0x40,0x40,0x40}},
  {{0x7f,0x02,0x0c,0x02,0x7f}}, {{0x7f,0x04,0x08,0x10,0x7f}},
  {{0x3e,0x41,0x41,0x41,0x3e}}, {{0x7f,0x09,0x09,0x09,0x06}},
  {{0x3e,0x41,0x51,0x21,0x5e}}, {{0x7f,0x09,0x19,0x29,0x46}},
  {{0x46,0x49,0x49,0x49,0x31}}, {{0x01,0x01,0x7f,0x01,0x01}},
  {{0x3f,0x40,0x40,0x40,0x3f}}, {{0x1f,0x20,0x40,0x20,0x1f}},
  {{0x3f,0x40,0x38,0x40,0x3f}}, {{0x63,0x14,0x08,0x14,0x63}},
  {{0x07,0x08,0x70,0x08,0x07}}, {{0x61,0x51,0x49,0x45,0x43}},
  {{0x3e,0x51,0x49,0x45,0x3e}}, {{0x00,0x42,0x7f,0x40,0x00}},
  {{0x42,0x61,0x51,0x49,0x46}}, {{0x21,0x41,0x45,0x4b,0x31}},
  {{0x18,0x14,0x12,0x7f,0x10}}, {{0x27,0x45,0x45,0x45,0x39}},
  {{0x3c,0x4a,0x49,0x49,0x30}}, {{0x01,0x71,0x09,0x05,0x03}},
  {{0x36,0x49,0x49,0x49,0x36}}, {{0x06,0x49,0x49,0x29,0x1e}}
}};

std::span<const uint8_t> glyph(char value) {
  static constexpr std::array<uint8_t, 5> blank{};
  static constexpr std::array<uint8_t, 5> colon{{0x00,0x36,0x36,0x00,0x00}};
  static constexpr std::array<uint8_t, 5> dash{{0x08,0x08,0x08,0x08,0x08}};
  static constexpr std::array<uint8_t, 5> dot{{0x00,0x60,0x60,0x00,0x00}};
  if (value == ':') return colon;
  if (value == '-') return dash;
  if (value == '.') return dot;
  const unsigned char upper = static_cast<unsigned char>(
      std::toupper(static_cast<unsigned char>(value)));
  if (upper >= 'A' && upper <= 'Z') return kFont[upper - 'A'];
  if (upper >= '0' && upper <= '9') return kFont[26 + upper - '0'];
  return blank;
}
} // namespace

Ssd1306Display::~Ssd1306Display() {
  if (device_) i2c_master_bus_rm_device(device_);
  if (bus_) i2c_del_master_bus(bus_);
}

bool Ssd1306Display::initialize() {
  i2c_master_bus_config_t bus_config{};
  bus_config.i2c_port = I2C_NUM_0;
  bus_config.sda_io_num = board::kOledSda;
  bus_config.scl_io_num = board::kOledScl;
  bus_config.clk_source = I2C_CLK_SRC_DEFAULT;
  bus_config.glitch_ignore_cnt = 7;
  bus_config.flags.enable_internal_pullup = true;
  if (i2c_new_master_bus(&bus_config, &bus_) != ESP_OK) return false;

  i2c_device_config_t device_config{};
  device_config.dev_addr_length = I2C_ADDR_BIT_LEN_7;
  device_config.device_address = board::kOledAddress;
  device_config.scl_speed_hz = 400'000;
  if (i2c_master_bus_add_device(bus_, &device_config, &device_) != ESP_OK) return false;

  constexpr std::array<uint8_t, 24> init{
      0xAE, 0xD5, 0x80, 0xA8, 0x1F, 0xD3, 0x00, 0x40,
      0x8D, 0x14, 0x20, 0x00, 0xA1, 0xC8, 0xDA, 0x02,
      0x81, 0x8F, 0xD9, 0xF1, 0xDB, 0x40, 0xA4, 0xAF};
  clear();
  return command(init) && flush();
}

void Ssd1306Display::show(UiState state, std::string_view text) {
  clear();
  if (state == UiState::idle) {
    std::array<char, 6> clock{{'-','-',':','-','-','\0'}};
    const std::time_t now = std::time(nullptr);
    std::tm local{};
    if (now > 1'700'000'000 && localtime_r(&now, &local) != nullptr) {
      std::snprintf(clock.data(), clock.size(), "%02d:%02d", local.tm_hour, local.tm_min);
    }
    constexpr size_t clock_width = 5 * 12 - 2;
    draw_text_scaled((width_ - clock_width) / 2, 1, clock.data(), 2);
    const size_t visible = std::min<size_t>(text.size(), 21);
    const size_t x = (width_ - visible * 6) / 2;
    draw_text(x, 23, text.substr(0, visible));
  } else if (state == UiState::alarm) {
    draw_text((width_ - 5 * 6) / 2, 3, "ALARM");
    const size_t visible = std::min<size_t>(text.size(), 21);
    draw_text((width_ - visible * 6) / 2, 20, text.substr(0, visible));
  } else {
    const size_t visible = std::min<size_t>(text.size(), 21);
    const size_t x = (width_ - visible * 6) / 2;
    draw_text(x, 12, text.substr(0, visible));
  }
  flush();
}

bool Ssd1306Display::command(std::span<const uint8_t> bytes) {
  std::array<uint8_t, 32> packet{};
  if (bytes.size() + 1 > packet.size()) return false;
  packet[0] = 0x00;
  std::copy(bytes.begin(), bytes.end(), packet.begin() + 1);
  return i2c_master_transmit(device_, packet.data(), bytes.size() + 1, 100) == ESP_OK;
}

bool Ssd1306Display::flush() {
  constexpr std::array<uint8_t, 6> window{0x21, 0, 127, 0x22, 0, 3};
  if (!command(window)) return false;
  std::array<uint8_t, 17> packet{};
  packet[0] = 0x40;
  for (size_t offset = 0; offset < pixels_.size(); offset += 16) {
    std::copy_n(pixels_.data() + offset, 16, packet.data() + 1);
    if (i2c_master_transmit(device_, packet.data(), packet.size(), 100) != ESP_OK) {
      return false;
    }
  }
  return true;
}

void Ssd1306Display::clear() { pixels_.fill(0); }

void Ssd1306Display::draw_text(size_t x, size_t y, std::string_view text) {
  for (char value : text) {
    draw_char(x, y, value);
    x += 6;
    if (x + 5 >= width_) break;
  }
}

void Ssd1306Display::draw_text_scaled(size_t x, size_t y,
                                      std::string_view text, size_t scale) {
  if (scale == 0) return;
  for (char value : text) {
    draw_char_scaled(x, y, value, scale);
    x += 6 * scale;
    if (x + 5 * scale >= width_) break;
  }
}

void Ssd1306Display::draw_char(size_t x, size_t y, char value) {
  const auto columns = glyph(value);
  for (size_t column = 0; column < columns.size(); ++column) {
    for (size_t row = 0; row < 7; ++row) {
      if ((columns[column] & (1U << row)) == 0) continue;
      const size_t px = x + column;
      const size_t py = y + row;
      if (px >= width_ || py >= height_) continue;
      pixels_[px + (py / 8) * width_] |= static_cast<uint8_t>(1U << (py % 8));
    }
  }
}

void Ssd1306Display::draw_char_scaled(size_t x, size_t y, char value,
                                      size_t scale) {
  const auto columns = glyph(value);
  for (size_t column = 0; column < columns.size(); ++column) {
    for (size_t row = 0; row < 7; ++row) {
      if ((columns[column] & (1U << row)) == 0) continue;
      for (size_t dx = 0; dx < scale; ++dx) {
        for (size_t dy = 0; dy < scale; ++dy) {
          const size_t px = x + column * scale + dx;
          const size_t py = y + row * scale + dy;
          if (px >= width_ || py >= height_) continue;
          pixels_[px + (py / 8) * width_] |= static_cast<uint8_t>(1U << (py % 8));
        }
      }
    }
  }
}

} // namespace companion

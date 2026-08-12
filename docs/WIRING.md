# Breadboard wiring

This pin map matches the firmware in `components/esp32_board/include/companion/board_pins.hpp`.

## Power first

| Module | Power |
|---|---|
| ESP32-S3 | USB-C data cable |
| INMP441 | 3V3 only |
| SSD1306 | 3V3 |
| MAX98357A | 5V/VBUS |
| All modules | common GND |

Add a 470 uF electrolytic capacitor beside the amplifier: capacitor `+` to
5V/VBUS and capacitor `-` to GND. Never connect the MAX98357A `SPK-` terminal
to ground; the speaker connects only between `SPK+` and `SPK-`.

## INMP441 microphone

| INMP441 | ESP32-S3 |
|---|---|
| VDD | 3V3 |
| GND | GND |
| WS | GPIO4 |
| SCK | GPIO5 |
| SD | GPIO6 |
| L/R | GND |

Keep the three I2S signal wires short and keep the microphone away from the
speaker and amplifier power wires.

## MAX98357A amplifier

| MAX98357A | ESP32-S3 / speaker |
|---|---|
| VIN | 5V/VBUS |
| GND | GND |
| DIN | GPIO7 |
| BCLK | GPIO15 |
| LRC | GPIO16 |
| SD/EN | 3V3 |
| GAIN | leave open initially |
| SPK+ / SPK- | two speaker wires |

Use a 4 ohm 3 watt speaker for the target POC. An 8 ohm 2-3 watt speaker is
safe for testing but quieter.

## SSD1306 OLED 128x32

| OLED | ESP32-S3 |
|---|---|
| VCC | 3V3 |
| GND | GND |
| SDA | GPIO41 |
| SCL | GPIO42 |

The firmware uses I2C address `0x3C`. If the display stays black, run an I2C
scanner and check whether the purchased module uses `0x3D`.

## Push-to-talk button

Connect one side to GPIO40 and the other side to GND. The firmware enables the
internal pull-up. GPIO40 is deliberately used instead of boot-strapping GPIO0.

## Breadboard layout

Use two 400-hole breadboards side by side because the DevKitC-1 is wide. Use
pre-cut U-shaped 22 AWG solid-core jumpers for short links and Dupont wires only
where modules cannot sit directly on the breadboard.

## Power-on inspection

Before plugging in USB:

1. Confirm no 5V wire reaches INMP441 or OLED.
2. Confirm every ground rail is common.
3. Confirm capacitor polarity.
4. Confirm no loose copper strand bridges `SPK+` and `SPK-`.
5. Confirm the ESP32 antenna end is not covered by wires or metal.

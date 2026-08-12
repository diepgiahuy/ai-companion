# POC bill of materials

Buy modules with header pins already soldered. A bare IC or bare flex-panel is
not suitable for this breadboard proof of concept.

| Qty | Part | Exact requirement | Purpose |
|---:|---|---|---|
| 1 | ESP32-S3 DevKitC-1 | N16R8, PCB antenna, headers soldered | controller, Wi-Fi/BLE, PSRAM |
| 1 | INMP441 breakout | I2S, six pins soldered; not an I2C microphone | voice input |
| 1 | MAX98357A breakout | I2S, pins and speaker terminal soldered | digital audio amplifier |
| 1 | Speaker | 4 ohm, 3 watt; 40 mm is acceptable | audio output |
| 1 | SSD1306 OLED module | 128x32, I2C, four pins soldered, usually 0x3C | status display |
| 1 | Momentary pushbutton | normally open, breadboard type | push to talk |
| 1 | Electrolytic capacitor | 470 uF, at least 10 V | amplifier supply buffer |
| 2 | Breadboard | 400 tie points each | no-solder assembly |
| 1 set | Solid-core jumper kit | pre-cut 22 AWG U-shaped jumpers | short, neat links |
| 1 set | Dupont wires | male-male; short lengths preferred | off-board modules |
| 1 | USB-C cable | data-capable, not charge-only | power and flashing |

Do not buy a MicroSD module, battery, charger, camera, TTP223, motor or custom
PCB for this stage. They do not prove the current audio interaction loop and
would increase wiring and failure modes.

## Minimum tools

- Small wire cutter/stripper for 22-26 AWG wire.
- Multimeter with continuity and DC voltage modes.
- No soldering iron is required if every module is purchased pre-soldered.


# Companion prototype hardware plan

This is the authoritative BOM, interface, pin, and power plan for the **ESP-VoCat
v1.2 reversible prototype** selected by ADR-005. It supersedes no schematic:
the board's v1.2 schematic and silkscreen are authoritative for board-owned
nets. Nothing in this document authorizes purchasing, soldering, custom PCB
work, battery work, or eFuse changes.

## BOM

| Assembly / part candidate | Qty | Interface / supply | Prototype role | Sourcing / risk |
|---|---:|---|---|---|
| Espressif ESP-VoCat **v1.2** | 1 | USB-C 5 V or protected 3.7 V Li-ion | Selected integrated board: S3, 360x360 QSPI panel, dual mic array, audio codec/amplifier, IMU, touch, buttons | Exact v1.2 silkscreen is mandatory; board is a single-SKU supply risk |
| Known-good USB data cable | 1 | USB-C | Flash, serial diagnostics, 5 V bench power | Charging-only cables are unsuitable |
| 5 V, 2 A regulated bench/USB supply | 1 | USB-C 5 V | Safe bench baseline | Capacity is a test setup requirement, not a measured consumption claim |
| Optional protected 1-cell Li-ion battery | 1 | Board battery connector | Later portable test only | Human approval, protection and polarity check required |
| Optional DRV2605L breakout + compatible ERM/LRA | 1 | I2C, 2.0–5.2 V device supply | Follow-up tactile-feedback experiment | Do not buy/connect before #9 design review; module motor current and actuator data sheet must be recorded |
| Optional WS2812B/SK6812 single pixel + local decoupling | 1 | 5 V + one RMT/SPI data line | Follow-up notification-light experiment | Requires measured supply transient and level/power review |
| No discrete accelerometer | 0 | — | Product discovery uses onboard BMI270 first | Do not add a sensor merely for “bump” pairing; RF is never authorization |

The selected board integrates 16 MB flash, 16 MB PSRAM, a 360x360 QSPI LCD,
dual microphones, 3 W speaker path, BMI270, USB-C programming/power, and
boot/reset controls. Its display specification lists ST77916, 262K colors,
and 3.3 V maximum panel/logic supply; it also lists up to 60 mA LED forward
current. Those are part specifications, not measured system current.

References: [ESP-VoCat v1.2 guide](https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32s3/esp-vocat/user_guide_v1.2.html),
[display specification](https://dl.espressif.com/AE/esp-dev-kits/UE018HV-RB39-A002A%20%20V1.0%20SPEC.pdf), and
[DRV2605L data sheet](https://www.ti.com/lit/ds/symlink/drv2605l.pdf).

## Pin and resource ownership

### Selected integrated board: fixed-function map

| Resource | ESP-VoCat v1.2 assignment | Owner / rule | Conflict result |
|---|---|---|---|
| Color display | Board FPC: ST77916 360x360 QSPI; reset control GPIO47; backlight is board-owned | BSP/display adapter only | No application GPIO reuse |
| Audio input | Codec/ADC board path; v1.2 I2S_DI GPIO3; codec power GPIO48 | Audio adapter only | No display/LED use |
| Audio output | Board codec/NS4150B path; PA control GPIO15 | Audio adapter only | No display/LED use |
| Touch | GPIO6 and GPIO7 | Board input adapter only | No application GPIO reuse |
| Serial debug | U1RXD GPIO4; U1TXD GPIO5 | Flash/diagnostics only | No feature use |
| I2C expansion | HC-1.25-4PLT: VIN, SDA, SCL, GND | Optional haptic/LED-expander experiments only | Address/voltage check before attachment |
| Wi-Fi / BLE | ESP32-S3 radio; no GPIO reservation | Firmware coexistence test only | No claim without RF test |
| Boot/recovery | physical BOOT and RST buttons; USB-C download/debug | Keep accessible in enclosure | Not repurposed |
| Motion | board BMI270 | Read-only product experiment behind a feature flag | No pairing authorization |

The v1.2 assignments above are taken from Espressif's documented revision
changes. The QSPI bus signals are deliberately treated as board/BSP-owned:
the application must not reassign them from a guessed schematic net. This
avoids an unresolved display/audio/debug conflict while preserving the vendor
BSP mapping.

### Current breadboard retrofit: controlled comparison only

The existing code owns GPIO4/5/6 for INMP441, GPIO7/15/16 for MAX98357A,
GPIO40 for button, and GPIO41/42 for SSD1306 I2C. A retrofit comparison can
use this *provisional, non-production* map only after visually checking the
actual DevKitC/module variant:

| Function | Proposed GPIO | Shares / exclusion |
|---|---:|---|
| GC9A01 or ST7789 SCLK / MOSI / CS / DC / RST / BL | 17 / 18 / 8 / 9 / 10 / 11 | Dedicated SPI signals; does not overlap current audio/button/OLED |
| DRV2605L I2C | 41 / 42, address must be probed | Shares the existing I2C bus; do not retain a conflicting device address |
| WS2812B/SK6812 | 12 via RMT | Dedicated; validate 3.3 V logic margin and 5 V supply locally |
| Optional external accelerometer | Same I2C bus only after address audit | Not in baseline |

The retrofit map is not a selected product wiring plan and does not assert
availability of any uninspected DevKitC pins. It exists solely to make a
reversible GC9A01/ST7789 comparison possible.

## Power and measurement plan

| Domain | Known constraint | Decision / test |
|---|---|---|
| USB bench input | ESP-VoCat supports 5 V DC; comparable Espressif audio/display boards specify a 5 V/2 A adapter | Use a regulated 5 V/2 A minimum bench source and log supply/cable/model |
| Battery | Board accepts 3.7 V battery; onboard charge/power management exists | Battery test is deferred; use only a protected pack after approval |
| LCD | Panel specification: 3.3 V maximum VDD/IOVDD; backlight maximum 60 mA | Do not power panel/backlight externally; measure board input current |
| Haptic | DRV2605L operates from 2.0–5.2 V and drives ERM/LRA differentially | Record actuator part number, driver/module rail, and peak waveform current before attaching |
| Addressable LED | Component supports RMT or SPI backends | Treat LED current and its supply transient as unmeasured until a selected pixel and brightness cap are tested |

Record measured input-current p50/p95/peak during idle, full-white display,
animation + audio, animation + Wi-Fi/BLE, storage write, and recovery. Capture
measurement instrument, bandwidth/sample rate, supply voltage, board revision,
battery state (if any), and firmware SHA. Until then, all system current,
thermal, acoustic, and brownout claims are **pending**.

## Enclosure and purchasing gates

The integrated choice reduces breadboard/interconnect risk but fixes a 55 mm
touch assembly and board-specific housing. Before any enclosure design or
purchase:

1. Confirm ESP-VoCat v1.2 availability and capture seller/SKU/price as an
   observation, not an estimate.
2. Confirm the display, reset, BOOT, USB-C, speaker openings, thermal path, and
   expansion connector remain accessible.
3. Approve one bench unit before any second board, battery, haptic, or LED.
4. Run the benchmark in `benchmarks/display/` and attach raw output.

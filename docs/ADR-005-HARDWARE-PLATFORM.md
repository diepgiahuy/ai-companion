# ADR-005: Prototype on ESP-VoCat v1.2; adopt esp_lcd + esp_lvgl_port as the baseline

- Status: accepted for reversible prototype; physical-performance decision remains pending
- Date: 2026-08-13
- Owners: issue #8 lead
- Blocks: #9

## Context

The current ESP-IDF composition root instantiates `Ssd1306Display`; its single
source of truth for the breadboard pin map has separate I2S microphone, I2S
speaker, button, and SSD1306 I2C signals. The current target is ESP32-S3 with
16 MB flash/PSRAM enabled. Replacing only the OLED adds display, haptic, LED,
and enclosure work to an already audio- and radio-constrained breadboard.

Issue #8 requires a reversible selection, exact wiring/power ownership, and a
same-workload comparison of graphics stacks. No physical finalist board or
instrumented supply is available to this decision. Therefore this ADR makes no
FPS, power, acoustic, RF, tearing, or reliability claim.

The repository firmware build is already pinned to **ESP-IDF 6.0.2**. Hardware
benchmarks must run on that same toolchain unless a separate ADR demonstrates a
blocking BSP/component incompatibility and defines an explicit migration plan. A
display spike is not allowed to silently create a second firmware baseline.

ESP-IDF 6.0 already contains the LCD API migration that older drafts treated as a
future concern. Benchmark code must use the v6 field/API names (including the
`gpio_num_t`, `dma_burst_size`, `rgb_ele_order`, and `in_color_format` changes)
rather than carrying a second IDF 5 compatibility implementation.

## Options compared

| Criterion | A: retrofit current DevKit | B: ESP-VoCat v1.2 | C: ESP32-S3-Korvo-2 + LCD |
|---|---|---|---|
| Display | Add GC9A01 (240x240 SPI) or ST7789 (240x240 SPI) | Included ST77916 360x360 circular QSPI display | Optional 320x240 LCD/touch extension |
| Audio | Keep external INMP441 + MAX98357A wiring | Included dual microphone array, ES7210/ES8311, 3 W speaker path | Included dual microphones, ES7210/ES8311, 3 W speaker path |
| Input / motion | Existing GPIO button; add optional IMU only after product decision | Touch, physical controls, included BMI270 | Six buttons; display accessory adds touch |
| Memory / radio | Existing S3 configuration must be physically identified | ESP32-S3-WROOM-1-N16R16VA, 16 MB flash + 16 MB PSRAM, Wi-Fi + BLE | ESP32-S3, Wi-Fi + BLE; exact fitted module/revision must be recorded at purchase |
| Debug / recovery | Depends on current DevKitC variant and breadboard integrity | USB-C download/debug; reset and boot controls | USB-UART, explicit boot/reset controls |
| Enclosure / wiring | Highest: five new display wires plus LED/haptic/actuator and power routing | Lowest: display/audio/battery are integrated; expansion remains optional | Medium: main board, LCD FPC, stand-offs, cables, and external speaker |
| Supply / second source | Commodity display/modules are broad but module quality is variable | One kit/SKU: convenient but single-board supply risk | Main board and LCD accessory are distinct SKUs; retail accessory availability is a risk |

Espressif documents ESP-VoCat v1.2 with the 360x360 QSPI display, dual
microphone array, 3 W speaker, 16 MB flash/PSRAM, USB-C programming, battery
support, IMU, and boot/reset controls. Its board catalog explicitly positions
it for companion/interactive-toy products. Korvo-2 is a credible maintained
alternative with dual microphones, codec/ADC, 3 W amp, buttons, battery socket,
debug bridge, and an LCD accessory, but its multi-board physical assembly is a
poorer first companion prototype.

Primary sources: [ESP-VoCat v1.2 guide](https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32s3/esp-vocat/user_guide_v1.2.html),
[ESP-VoCat display specification](https://dl.espressif.com/AE/esp-dev-kits/UE018HV-RB39-A002A%20%20V1.0%20SPEC.pdf),
[Espressif board selection](https://docs.espressif.com/projects/esp-techpedia/en/latest/esp-friends/get-started/board-selection.html), and
[Korvo-2 guide](https://docs.espressif.com/projects/esp-adf/en/latest/design-guide/dev-boards/user-guide-esp32-s3-korvo-2-v3.0.html).

## Decision

1. Select **ESP-VoCat v1.2**, subject to human purchase approval, as the
   reversible prototype finalist. Do not remove the current SSD1306 adapter
   before the chosen board passes the benchmark and a basic boot/peripheral
   smoke test.
2. Use **ESP-IDF `esp_lcd` + `esp_lvgl_port` 2.8.0~1 + LVGL 9** as the one baseline
   UI stack for the first benchmark. On 2026-08-13 the official Espressif Component
   Registry reports `2.8.0~1` as the latest stable release; its 2.x implementation
   supports LVGL 9. Re-verify the registry immediately before the physical run and
   record the exact resolved artifact/hash rather than silently substituting a newer
   release.
3. Keep **LovyanGFX** only as the same-board benchmark challenger required by
   issue #8, not as a second product runtime. If it does not win the physical
   promotion rule, remove the benchmark dependency and keep only the vendor stack.
4. Do not select TFT_eSPI for new ESP-IDF work. It has no demonstrated
   repository-fit advantage over the two benchmark candidates.
5. No discrete haptic motor, WS2812, or accelerometer is approved for the
   first ESP-VoCat prototype. The board's built-in BMI270 is sufficient to
   decide whether a motion interaction is valuable. A haptic/LED add-on is
   an explicit follow-up purchase and wiring gate.

`esp_lcd` provides a common panel abstraction across SPI, I80, RGB, and
other interfaces, and exposes color-transfer completion callbacks. The
`esp_lvgl_port` registry component is actively developed, integrates
`esp_lcd`, supports LVGL 8 and 9, and defaults to the latest LVGL line. For
this repository the first benchmark deliberately uses LVGL 9 rather than
maintaining both compatibility modes. LovyanGFX advertises ESP-IDF/S3,
DMA-overlapped transfers, and sprite transforms, but those capabilities are
not a device-level result.

Sources: [ESP-IDF LCD API](https://docs.espressif.com/projects/esp-idf/en/latest/esp32s3/api-reference/peripherals/lcd/index.html),
[ESP-IDF 6.0 peripheral migration guide](https://docs.espressif.com/projects/esp-idf/en/latest/esp32s3/migration-guides/release-6.x/6.0/peripherals.html),
[esp_lvgl_port 2.8.0~1](https://components.espressif.com/components/espressif/esp_lvgl_port/versions/2.8.0~1/readme),
and [LovyanGFX](https://github.com/lovyan03/LovyanGFX).

## Version and dependency constraints

- **ESP-IDF: exactly `v6.0.2` for the first benchmark**, matching the repository
  firmware build. Any toolchain change requires a coordinated repository decision,
  not a display-only downgrade.
- `esp_lcd`: use only the API supplied by ESP-IDF 6.0.2. Do not add IDF 5 LCD
  compatibility shims; use the documented v6 configuration fields directly.
- `esp_lvgl_port`: **2.8.0~1 for the first benchmark, subject to the mandatory
  pre-run re-verification above**. The component manifest/lock must record the exact
  resolved revision/hash; do not leave a floating version as benchmark evidence.
- LVGL: **major version 9 only** for this prototype. Record the exact resolved
  version/hash from the component graph. Do not add an LVGL 8 compatibility build
  unless a new measured blocker and ADR justify replacing this decision.
- Board support: use the exact ESP-VoCat v1.2 BSP/board revision and resolved
  component hashes named in benchmark evidence.
- LovyanGFX: pin an explicit reviewed commit SHA only in the alternative benchmark
  build. A package is not added to production firmware unless it wins the physical
  gate.

## Measurable promotion rule

Run both stacks on the same ESP-VoCat v1.2, same ESP-IDF 6.0.2 configuration,
panel clock, 16-bit color format, asset set, audio playback, Wi-Fi transfer, and
BLE state. Each run must record at least 300 frames for full and partial updates:
p50/p95/max frame time, dropped frames, free/minimum/largest internal and PSRAM
heap, firmware binary size, visible defects during flash/storage activity, and
recovery result.

The initial experience target is p95 <= 33 ms (30 FPS). Choose LovyanGFX only
if it both passes every correctness/coexistence check and materially improves
p95 or memory versus the baseline without increasing binary size enough to
threaten the two 4 MiB OTA slots. Otherwise retain the vendor stack. This is
a decision rule, not a pre-measured result.

## Consequences and rollback

Issue #9 may create a board-specific display/input adapter, UI task, and
haptic/LED port only after revalidating board revision, the **ESP-IDF 6.0.2 +
esp_lvgl_port 2.8.0~1 + LVGL 9** component graph, pin plan, and this benchmark.
It must not change the hardware-independent `companion_app` contract or remove
SSD1306 until the gate is attached.

Rollback is to the existing SSD1306 build and breadboard map. No eFuses,
secure-boot changes, purchases, custom PCB, or irreversible enclosure change are
authorized by this ADR.

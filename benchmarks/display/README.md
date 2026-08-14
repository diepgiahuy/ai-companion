# Display benchmark harness

This directory makes a physical display comparison reproducible without turning a
simulator, host timing, or a handwritten spreadsheet into hardware evidence.

`summarize_results.py` accepts only a completed physical result, requires 300
or more frames for **each** full and partial run, calculates nearest-rank p50,
p95, and max frame time, records dropped frames, and refuses pending data. Its
unit tests use synthetic values only to test the validator/calculator; they are
not device measurements.

## Required test matrix

Run this matrix twice on the **same ESP-VoCat v1.2**, with the same firmware
commit apart from the renderer, the same ESP-IDF version/configuration, panel
clock, RGB565 asset set, test duration, Wi-Fi traffic source, BLE setting, and
audio clip.

| Variant | Stack |
|---|---|
| Baseline | ESP-IDF `esp_lcd` + the reviewed/pinned `esp_lvgl_port` + LVGL 9 graph from ADR-005 |
| Alternative | LovyanGFX at a recorded immutable revision |

For each variant:

1. Flash a dedicated benchmark build that renders the same animated
   circles/sprites, fonts, and image assets at the board's 360x360 resolution.
   Its log must emit every frame duration from `esp_timer_get_time()` and the
   corresponding dropped-frame count; this timer reports microseconds since boot.
2. Capture 300+ partial-update frames and 300+ full-screen frames. Keep
   I2S audio playing and active Wi-Fi traffic for both; exercise BLE when
   feasible. If BLE is not exercised, record the concrete reason instead of
   reporting a fake pass.
3. Record heap free/minimum/largest block for internal RAM and PSRAM using
   ESP-IDF heap-capability APIs, plus the `idf.py size` binary size.
4. Record a SHA-256 digest of the exact component lock/manifest used for the
   build. The stack description is not a substitute for an immutable dependency
   identity.
5. During a run, write/erase the intended storage medium and record visible
   tearing, corruption, watchdog/brownout, and whether the application
   recovers. A camera/video is useful supporting evidence but does not
   replace the log.
6. Measure board input current when instrumentation is available. For idle,
   full-white, animation+audio, animation+Wi-Fi/BLE, storage-write, and recovery,
   record p50/p95/peak mA together with supply voltage and instrument sample rate.
   If instrumentation is unavailable, record that explicitly; the resulting
   summary remains structurally valid but marks power measurement incomplete and
   cannot by itself close a physical power gate.
7. Save the raw serial frame log and one result JSON under a date- and
   board-revision-specific evidence location in the follow-up PR. Record the raw
   log SHA-256 in the result so the summary is bound to the reviewed artifact.
   Do not commit a result with `measurement_status: "pending"` as evidence.

The official ESP timer and heap APIs used by this harness are documented in
[ESP Timer](https://docs.espressif.com/projects/esp-idf/en/latest/esp32s3/api-reference/system/esp_timer.html)
and [Heap Memory Debugging](https://docs.espressif.com/projects/esp-idf/en/latest/esp32s3/api-reference/system/heap_debug.html).

## Result format and validation

Copy `result.pending.json` outside the repository evidence directory, replace
every placeholder with observed data, change the status to `"physical"`, then
run:

```sh
python3 benchmarks/display/summarize_results.py result.json --output summary.json
python3 -m unittest benchmarks/display/test_summarize_results.py
```

The validator deliberately has no `--allow-pending` option. Its success means
only that the observation record is structurally complete and internally
consistent. It does not prove that the named board produced the data, that the
firmware SHA was flashed, or that a current probe was calibrated. Raw artifacts,
trusted run provenance, and operator review remain required.

The initial decision target is p95 <= 33 ms per workload. ADR-005 defines the
selection rule; do not choose a library from average FPS alone.

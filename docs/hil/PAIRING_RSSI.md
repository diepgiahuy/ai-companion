# Pairing RSSI two-DUT qualification

This procedure is the physical evidence half of issue #100. Software/Tier-1 results are not substitutes for these measurements.

## Evidence rule

Use **two real target ESP32-S3 devices** running the exact candidate firmware. Keep the product BLE payload shape (`CP-` plus 16 Base32 symbols), record raw RSSI observations, and label the experiment outside the firmware. Never put a stable device identity or credential into BLE merely to make the benchmark easier.

The repository tooling does not select or promote a production threshold by itself. `scripts/pairing_rssi_analyze.py` only ranks candidates and emits `"promotion":"none"`.

Acceptance corpora are fail-closed on provenance. Every capture records the full 40-character firmware Git SHA, board revision, ESP-IDF version, SHA-256 of the effective firmware config, enclosure definition and the two physical DUT identifiers. A coverage-accepted corpus must use one candidate firmware/config/board/enclosure definition throughout; compare a changed candidate in a separate corpus instead of mixing it into the same score.

## Capture matrix

Use at least:

- 2 distinct physical DUTs, testing both directions when practical;
- 3 environments (for example open room, furnished room, and interference-heavy/obstructed room);
- 3 fixed distances spanning intended-near and intended-far behavior;
- 2 orientations per relevant distance;
- 3 or more labeled near captures and 3 or more labeled far captures overall;
- 20 or more raw observations per capture.

Use the same distance/orientation definitions for all environments so comparisons are meaningful. Record unusual RF conditions rather than silently deleting them.

Before starting, record the exact candidate values. The config fingerprint is the SHA-256 digest of the effective `sdkconfig` used by the capture build (for example `sha256sum sdkconfig` on Linux or `shasum -a 256 sdkconfig` on macOS).

## Firmware capture

`DiscoveryObservation.rssi` is the unfiltered NimBLE RSSI input used by this work. A trusted HIL capture build may call:

```cpp
#include "companion/pairing_rssi_hil.hpp"

companion::pairing::DiscoveryObservation observation{};
while (radio.poll(observation)) {
  companion::pairing::hil::log_observation(observation);
}
```

The helper emits lines such as:

```text
PAIRING_RSSI alias=CP-ABCDEFGHIJKLMNOP rssi=-52 seen_ms=123456
```

The helper is evidence instrumentation only. Production selection remains in the pairing policy layer.

## Label and save a capture

Pipe the serial/monitor log into the standard-library capture formatter. Use the same provenance values for every capture in one candidate corpus:

```bash
idf.py monitor | python3 scripts/pairing_rssi_capture.py \
  --capture-id office-20cm-front-a-to-b \
  --dut-tx DUT-A \
  --dut-rx DUT-B \
  --environment office \
  --distance-cm 20 \
  --orientation front-to-front \
  --expected-near true \
  --firmware-sha <FULL_40_CHAR_GIT_SHA> \
  --board-revision <EXACT_BOARD_REVISION> \
  --esp-idf-version v6.0.2 \
  --config-fingerprint <SDKCONFIG_SHA256> \
  --enclosure <EXACT_ENCLOSURE_ID> \
  --output evidence/hil/pairing-rssi/<DATE>/corpus.jsonl
```

Repeat with new `capture-id` values for the full matrix. `source` is written as `physical_hil`; the formatter never fabricates samples.

## Analyze without promoting

For exploratory work on an incomplete corpus:

```bash
python3 scripts/pairing_rssi_analyze.py \
  evidence/hil/pairing-rssi/<DATE>/corpus.jsonl \
  --output evidence/hil/pairing-rssi/<DATE>/analysis.json
```

For acceptance review use both provenance and coverage enforcement:

```bash
python3 scripts/pairing_rssi_analyze.py \
  evidence/hil/pairing-rssi/<DATE>/corpus.jsonl \
  --require-physical \
  --require-coverage \
  --output evidence/hil/pairing-rssi/<DATE>/analysis.json
```

`--require-coverage` is intentionally invalid without `--require-physical`. Coverage acceptance also rejects mixed firmware SHAs, board revisions, ESP-IDF versions, config fingerprints or enclosure definitions.

The sweep evaluates fixed RSSI thresholds from -95 to -30 dBm, rolling median windows of 1/3/5/7 observations, and 1/2/3 consecutive passing windows. Candidate ranking weights false-positive pairing twice as heavily as false negatives. That weighting is a review aid, not a product requirement.

Each retained top candidate includes aggregate metrics plus `by_environment` and `by_orientation` false-positive/false-negative breakdowns. Use those breakdowns rather than selecting from aggregate score alone.

## Acceptance review

Before a threshold/filter can become product policy, inspect at minimum:

- per-environment false-positive and false-negative behavior;
- stability under orientation changes;
- reconnect/restart repeatability;
- whether a candidate relies on one anomalous environment/capture;
- ambiguous multi-peer behavior (which must remain fail-closed);
- the exact firmware commit, config, board revision, enclosure and exact two physical DUTs used.

Only after the physical corpus and review exist should #100 select calibrated RSSI/ranking policy. Secure ranging remains a separate non-goal unless explicitly designed later.

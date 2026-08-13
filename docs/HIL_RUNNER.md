# ESP32-S3 HIL runner

This document describes the security and operating contract for the physical
Hardware-in-the-Loop runner. The repository is public, so the runner is not a
general pull-request executor.

## Security boundary

- Run `.github/workflows/firmware_hil.yml` only through manual `workflow_dispatch` by a maintainer with write access.
- Select a trusted branch, tag, or commit from this repository. Never copy a fork's code into a trusted ref to bypass this boundary.
- Do not add a `pull_request` trigger to the physical workflow.
- Keep repository permissions read-only.
- Use a dedicated macOS account/runner directory with no personal documents, cloud credentials, SSH keys, browser profiles, wallets, or unrelated source.
- Do not expose production credentials. HIL credentials are least-privilege, revocable test credentials stored in GitHub secrets.
- Restrict runner/network access to required build dependencies and the intended test backend.
- Take the runner offline when it is not needed or its integrity is uncertain.

GitHub-hosted or isolated ephemeral runners perform public pull-request build, unit,
integration and simulator checks.

## Runner and toolchain setup

1. Create a dedicated macOS account for the runner.
2. Register a repository runner with labels `self-hosted`, `macOS`, `ARM64`, and `esp32s3-hil`.
3. Use an Actions runner version compatible with the pinned Actions in the workflow.
4. Install the repository-supported ESP-IDF under `$HOME/esp/esp-idf` and verify `idf.py --version`.
5. Keep Python isolated from personal environments; the workflow installs the exact HIL requirements.
6. Configure the runner service only after an interactive smoke run succeeds.

Update ESP-IDF or pytest packages through a reviewed PR. Record compatibility and
rollback notes before changing pinned versions.

## Product-network HIL configuration

The firmware product composition has **no mock backend mode**. A physical smoke run
therefore uses the same Wi-Fi + WebSocket protocol-v2 path as the product build.
Configure these repository/environment secrets for the trusted HIL runner:

- `HIL_WIFI_SSID` — required test Wi-Fi SSID.
- `HIL_WIFI_PASSWORD` — test Wi-Fi password; may be empty only for an intentionally open isolated test network.
- `HIL_SERVER_URL` — required `ws://` or `wss://` endpoint reachable from the DUT.
- `HIL_DEVICE_TOKEN` — optional only when the isolated test backend intentionally allows unauthenticated device access; production-auth evidence needs a credentialed scenario.

The workflow generates a temporary `SDKCONFIG_DEFAULTS` overlay for these values,
builds the firmware, never uploads that overlay/build as an artifact, and deletes the
build/config after the physical run. Do not echo the secrets or add the generated
firmware binary to HIL artifacts.

The backend endpoint must be reachable from the ESP32's Wi-Fi network. A successful
boot smoke proves only that the product firmware initializes and reaches its
application loop with the configured network path; it does not by itself prove real
ASR/TTS/LLM quality or production authentication policy.

## DUT contract

The base smoke DUT is an ESP32-S3 with peripherals required by `main/app_main.cpp`,
currently display, button, I2S audio, Wi-Fi and WebSocket initialization.

Before a run:

1. Connect the DUT and required peripherals.
2. Identify the intended stable `/dev/cu.*` serial path and ensure no monitor owns it.
3. Confirm the board can enter download/reset mode and has a recovery path.
4. Confirm the configured HIL Wi-Fi and backend endpoint are reachable from the bench.
5. Do not enable Secure Boot, flash encryption, anti-rollback, or eFuses in this base smoke suite.

Pass the explicit serial path as the `device_port` workflow input. Do not rely on
auto-detection when multiple Espressif devices are connected.

Future multi-DUT suites must provide an explicit port-to-role mapping and a hardware
resource lock so concurrent jobs cannot claim the same board.

## Running the smoke test

1. Open **Actions -> Firmware HIL -> Run workflow**.
2. Select the trusted ref.
3. Enter the explicit serial path.
4. Enable flash erase only when required.
5. Start the workflow and inspect build, flash and serial output.

The workflow builds the canonical WebSocket product firmware, flashes it with
`pytest-embedded`, and waits for the real application-loop message emitted after
peripheral/network initialization.

A pass is evidence only for the named commit, board/peripheral setup, test network and
smoke scenario. It does not prove pairing, voice mail, display performance, audio
quality, OTA, security provisioning, provider quality or long-duration reliability.

## Required evidence

Retain or link:

- GitHub run ID and tested commit SHA;
- ref and workflow revision;
- ESP-IDF version;
- ESP32 target and board/peripheral revision;
- selected port or stable device serial identity;
- JUnit result and relevant redacted serial log;
- test operator notes for wiring/network/environment differences.

Do not put Wi-Fi passwords, device tokens, media URLs, or other secrets in artifacts
or serial logs.

## Negative validation

Before closing the HIL foundation issue:

1. Change the boot expectation on a disposable trusted branch to an impossible string and confirm failure.
2. Select an invalid/disconnected port and confirm failure before any pass claim.
3. Break the firmware build on a disposable trusted branch and confirm failure.
4. Remove a required HIL network secret and confirm the workflow fails closed before build.
5. Restore the trusted ref/configuration and rerun the smoke test.

These checks prove that build, network prerequisite and assertion failures are not
masked.

## Maintenance and recovery

- Apply runner/macOS security updates on a defined cadence.
- Review runner logs and registered instances; remove stale/offline registrations.
- Rotate HIL-specific credentials after suspected exposure.
- Clean work directories between test campaigns; the workflow also removes credential-bearing build/config files after each run.
- Stop the runner before investigation if a job/dependency behaves unexpectedly.
- Re-register/rebuild the dedicated account rather than trusting a contaminated environment.
- Keep a manual `idf.py flash monitor` recovery procedure and a known-good firmware artifact outside the HIL secret-bearing build directory.

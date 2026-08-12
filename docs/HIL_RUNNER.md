# ESP32-S3 HIL runner

This document describes the security and operating contract for the physical
Hardware-in-the-Loop runner. The repository is public, so the runner is not a
general pull-request executor.

## Security boundary

- Run `.github/workflows/firmware_hil.yml` only through manual
  `workflow_dispatch` by a maintainer with write access.
- Select a trusted branch, tag, or commit from this repository. Never copy a fork's
  code into a trusted ref only to bypass this boundary.
- Do not add a `pull_request` trigger to the physical workflow.
- Keep repository permissions read-only.
- Use a dedicated macOS account and runner directory with no personal documents,
  cloud credentials, SSH keys, browser profiles, wallet data, or unrelated source.
- Do not expose production secrets to the runner. HIL-specific test credentials must
  be least-privilege, revocable, and stored through GitHub's secret controls.
- Restrict network access to what build/test dependencies and the intended test
  backend require.
- Take the runner offline when it is not needed or when its integrity is uncertain.

GitHub-hosted or isolated ephemeral runners should perform public pull-request build,
unit, integration, and simulator checks.

## Runner and toolchain setup

1. Create a dedicated macOS account for the runner.
2. Register a repository runner using GitHub's generated one-time setup command.
3. Assign the labels `self-hosted`, `macOS`, and `ARM64`.
4. Install the repository-supported ESP-IDF toolchain under
   `$HOME/esp/esp-idf`.
5. Confirm `$HOME/esp/esp-idf/export.sh` exists and `idf.py --version` works in
   the runner account.
6. Keep system Python separate from personal environments. The workflow installs the
   compatible major/minor versions from `tests/firmware/requirements.txt` into the
   ESP-IDF-selected Python environment.
7. Configure the runner service only after an interactive smoke run succeeds.

Update ESP-IDF or pytest packages through a reviewed PR. Record compatibility and
rollback notes before changing the pinned range.

## DUT contract

The base smoke DUT is an ESP32-S3 with the peripherals required by
`main/app_main.cpp` and the active `sdkconfig`, currently including display,
button, and audio initialization.

Before a run:

1. Connect the DUT and required peripherals.
2. List serial devices and identify the intended stable `/dev/cu.*` path.
3. Ensure no serial monitor or other process owns the port.
4. Confirm the board can be put into download/reset mode and has a recovery path.
5. Do not enable Secure Boot, flash encryption, anti-rollback, or eFuses in this base
   smoke suite.

Pass the explicit serial path as the `device_port` workflow input. Do not rely on
auto-detection when multiple Espressif devices are connected.

For future multi-DUT suites, provide an explicit port-to-role mapping such as
`initiator` and `responder`. Add a hardware resource lock so concurrent jobs
cannot claim the same board.

## Running the smoke test

1. Open **Actions -> Firmware HIL -> Run workflow**.
2. Select the trusted ref to test.
3. Enter the explicit serial path.
4. Enable flash erase only when the scenario requires it.
5. Start the workflow and monitor build, flash, and serial output.

The workflow builds the repository root, flashes that build with
`pytest-embedded`, and waits for the actual application-loop message emitted after
peripheral initialization.

A successful run is evidence only for the named commit, board/peripheral setup, and
smoke scenario. It does not prove pairing, voice-mail, display performance, audio
quality, OTA, security provisioning, or long-duration reliability.

## Required evidence

Retain or link:

- GitHub run ID and tested commit SHA;
- ref and workflow revision;
- ESP-IDF version;
- ESP32 target and board/peripheral revision;
- selected port or stable device serial identity;
- JUnit result and relevant serial log;
- test operator notes for wiring or environment differences.

Do not put Wi-Fi passwords, device tokens, media URLs, or other secrets in artifacts
or serial logs.

## Negative validation

Before closing the HIL foundation issue:

1. Change the boot expectation on a disposable branch to an impossible string and
   confirm the job fails.
2. Select an invalid/disconnected port and confirm the job fails before claiming a
   pass.
3. Break the firmware build on a disposable branch and confirm the job fails.
4. Restore the trusted ref and rerun the real smoke test.

These checks prove that failure paths are not being masked.

## Maintenance and recovery

- Apply runner and macOS security updates on a defined cadence.
- Review runner logs and registered instances; remove stale/offline registrations.
- Rotate HIL-specific secrets after suspected exposure.
- Clean work directories between materially different test campaigns.
- If a job or dependency behaves unexpectedly, stop the runner before investigation.
- Re-register or rebuild the dedicated account rather than trusting a contaminated
  environment.
- Keep a manual `idf.py flash monitor` recovery procedure and known-good firmware
  artifact for bench recovery.

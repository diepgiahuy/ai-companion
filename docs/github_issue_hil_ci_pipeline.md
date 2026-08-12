# [Infrastructure] Establish Hardware-in-the-Loop (HIL) CI/CD Pipeline on Mac M1

## 1. Context & Motivation
Currently, firmware testing relies on manual verification. To scale the project to market standards (Xiaozhi-level quality), we must automate hardware verification. This issue tracks the creation of a **Physical HIL (Hardware-In-The-Loop) Pipeline** using a self-hosted runner. Any PR that touches firmware must be automatically flashed and tested on real hardware before merging. 

*Research Note (SotA 2026):* We will use Espressif's official `pytest-embedded` framework to avoid mocking ESP-IDF APIs and ensure true E2E behavioral testing over USB Serial.

## 2. Hardware / BOM State
*   **[ADDED]** Host Machine: Apple Mac M1 (acting as GitHub Actions Self-Hosted Runner).
*   **[ADDED]** DUT (Device Under Test): ESP32-S3 physically connected to the Mac M1 via USB cable.

## 3. Acceptance Criteria (AC)

### 3.1. Infrastructure Setup
*   **WHEN** code is pushed to `main` or a PR modifies `firmware/**`, **THEN** the `.github/workflows/firmware_hil.yml` workflow MUST trigger.
*   **WHEN** the workflow runs, **THEN** it MUST target the `[self-hosted, macOS, ARM64]` runner tags.
*   **WHEN** the runner executes, **THEN** it MUST build the firmware using the local `esp-idf` environment.

### 3.2. Pytest-Embedded Integration
*   **WHEN** the firmware is successfully built, **THEN** the pipeline MUST automatically flash the `.bin` to the connected ESP32-S3.
*   **WHEN** flashing completes, **THEN** `pytest-embedded` MUST execute the test suite in `tests/firmware/test_hil.py`.
*   **WHEN** a Python test script asserts `dut.expect('...', timeout=X)`, **THEN** the test MUST fail if the serial output does not match within the timeout period.

## 4. Architectural Pointers & File Modifications
The assigned AI/Dev should create/modify the following:
*   `[NEW] .github/workflows/firmware_hil.yml`: The CI/CD pipeline definition.
*   `[NEW] tests/firmware/test_hil.py`: Boilerplate Python scripts utilizing `pytest-embedded` fixtures (`dut: Dut`).
*   `[NEW] tests/firmware/pytest.ini`: Configuration for pytest-embedded specifying the target as `esp32s3`.

## 5. Architectural Policy Check
*   This setup adheres strictly to the E2E verification mandate in `COMMERCIAL_ARCHITECTURE.md`.
*   Mocking is strictly forbidden for integration flows. Tests must interact with the real hardware state.

## 6. Execution Steps for Assignee
1.  Verify the runner is online.
2.  Implement the `firmware_hil.yml` workflow.
3.  Write the base `test_hil.py` verifying a successful boot (`app_main`).
4.  Push the PR. The PR must pass on the local Mac M1 runner before being merged.

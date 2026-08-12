# Verification record

This file records what was actually executed for the current refactor. README is the feature/status source of truth; this file is the verification boundary.

## Current architecture gate

The refactor now includes:

- explicit `user_id`, `device_id`, `thread_id`, `session_id`, and turn identity boundaries;
- write-through bounded conversation cache keyed by `user_id + thread_id`;
- user-owned domain data with typed repository ports and CRUD;
- deterministic `ContextRouter` that selects tool packs/resources without another LLM round trip;
- application-controlled MCP-style resources; generic `resource.read/list` remain available internally but are hidden from normal LLM discovery;
- durable reminder delivery state `pending -> dispatching -> sent -> fired`, with ESP32 `alarm_ack` and retry/backoff;
- early UI presentation events emitted at tool completion, while the LLM still performs the final verbalization/TTS path;
- daily/weekly/monthly budget CRUD, timer pause/resume, and explicit current-thread conversation clear;
- session-nonce turn idempotency and user+device-scoped proactive push/ACK;
- compatibility migration that adopts legacy single-user data into owner `default` and preserves legacy device-separated conversations when that old column exists;
- Go toolchain pinned to **Go 1.26.5** in `backend/go.mod` and the test container.
- production-shaped control-plane boundaries: desired/reported device twin, globally monotonic resolved config generation, feature catalog/flags, entitlements, privacy, credential-backed user/tenant/plan claims, and OTA metadata registry;
- temporal memory plus rebuildable vector secondary index, live market provider/cache/watch layer, transactional outbox, tool schema/policy enforcement, and LLM usage/quota hooks.

## Executed in this sandbox

The host C++/Opus regression and memory/partition budget gate are executable here:

```bash
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py
```

Expected/current result for this delivery: **2/2 host tests pass** (`companion_tests`, `opus_probe`), two 4 MiB OTA slots remain within the 16 MiB partition table, and the internal-SRAM design cap remains 160.5 KiB.

Pure-Go tests that do not need unavailable external modules also pass locally (`domain`, `capability`, `conversation`, `contextengine`). An explicit `compilecheck` build tag supplies compile-only local shims for the SQLite driver and Opus factory. With temporary, non-shipped WebSocket/Opus API stubs, **all backend packages and all `_test.go` files compile** (`go test -run '^$' -tags compilecheck ./...`). This is a type/signature gate only; it does not pretend that SQLite/WebSocket/Opus runtime behavior was exercised.

## Full Go 1.26.5 software E2E gate

The acceptance gate is:

```bash
make e2e-container
```

or, in an environment that already has Go 1.26.5 + libopus:

```bash
./scripts/e2e.sh
```

`scripts/e2e.sh` refuses to run the backend suite unless `go env GOVERSION` reports Go 1.26.5. It then executes the full Go suite with the race detector, including WebSocket/Opus, Qwen fake endpoint/tool loop, SQLite repositories/migrations, expense+budget UI/TTS flow, conversation persistence, CRUD, and scheduler alarm ACK/retry tests.

**Release-toolchain boundary:** this sandbox has Go 1.23.2 and blocks outbound TCP/DNS, so it cannot fetch the Go 1.26.5 binary or upstream modules and has no Docker daemon. Functional runtime E2E is now covered by `make e2e-offline`; the exact Go 1.26.5 + pinned upstream-module release gate remains `make e2e-container` on Docker Desktop/CI.

## ESP-IDF / hardware gate

ESP-IDF/Xtensa and the physical ESP32-S3 audio/display hardware are not available here. Target acceptance remains:

```bash
idf.py set-target esp32s3
idf.py build
idf.py size
```

followed by hardware-in-the-loop checks for I2S mic/speaker, SSD1306 rendering, SNTP/timezone behavior, Wi-Fi reconnect, real reminder ACK delivery, Smart VAD tuning, and future ESP-SR AEC/WakeNet behavior.

## Network-isolated full functional E2E

Run:

```bash
make e2e-offline
```

This gate is designed for sandboxes with no Docker and no outbound module downloads. It runs the C++ host FSM/Opus tests, partition/SRAM checks, then `go test -race -count=1 ./...` through `backend/go.offline.mod`. In this sandbox the functional suite executed with Go 1.23.2 because the exact Go 1.26.5 binary cannot be downloaded; production `backend/go.mod` remains pinned to Go 1.26.5. The compatibility modules use the host `libsqlite3` and `libopus.so.0` and a minimal RFC6455 test transport. Production `go.mod` is untouched.

**Observed in this delivery sandbox (final 2026-08-11 run): PASS for all backend packages with the race detector.** Coverage includes `TestExpenseBudgetFullE2EThroughQwenRegistryAndUI`, `TestReminderSchedulerPushesAlarmToTargetDevice`, Qwen structured tool-loop/idempotency, tool JSON-schema rejection, routing-eval corpus, temporal/vector memory, transactional outbox, config-generation/device twin behavior, atomic market threshold alerts, OTA manifest compatibility, Feature Catalog rollback rejection, trusted device user/tenant/plan credential claims, and voice-memo privacy denial before any WAV file is written. C++ host tests remain **2/2 PASS**; partition/SRAM checks remain **PASS**.

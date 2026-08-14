# Acceptance test plan

## Test levels

| Level | What it proves | Current result |
|---|---|---|
| Host unit/integration tests | FSM, 20 ms capture pump, playback, timeout, barge-in, Smart VAD, idle, alarm/beep | **PASS in this delivery** |
| Go race/E2E tests | WebSocket lifecycle, Opus, Qwen tools, persistence, scheduler/alarm | Go 1.26.6 production-module upgrade gate pending hosted verification |
| Budget/partition check | N16 flash layout and two OTA slots | **PASS in this delivery** |
| Wokwi | boot, OLED, GPIO40 button, state transitions | project supplied; audio is not acoustically simulated |
| ESP-IDF target compile | real ESP32-S3 API compatibility and binary size | pending in this sandbox; use supplied IDF container |
| Physical breadboard | mic samples, VAD threshold, speaker, clock sync, AEC prerequisites, brownout | pending hardware |

## Host test

```bash
cmake -S host -B build-host
cmake --build build-host -j2
ctest --test-dir build-host --output-on-failure
```

Current expected line from `companion_tests`:

```text
PASS: streaming + timeout + silence + barge-in + smart VAD + idle/alarm
```

## Backend test

```bash
cd backend
go test -tags nolibopusfile -race ./...
```

or from the root for a reproducible toolchain:

```bash
docker compose run --build --rm test
```

The backend suite includes coverage for batch/idempotent expenses, deterministic
relative timers, reminder claim/recover/sent/ACK/fire, legacy timestamp UTC normalization,
voice-memo WAV metadata, Qwen tool calls, capability registry discovery/execution,
native MCP-style resource reads (`expenses://today`, `timers://active`), timer/reminder
kind separation, and targeted alarm WebSocket delivery.

## Context/CRUD acceptance

1. Warm `conversation.Service.Recent`, append a message, and confirm the next read stays in the write-through cache without another durable read.
2. Use two thread IDs for one user and confirm history never leaks across threads.
3. Ask an expense question and confirm ContextRouter exposes only expense/budget packs and preloads the matching resources.
4. Ask to clear conversation history explicitly and confirm only the `context` pack is exposed; the prior thread is deleted and cache invalidated.
5. Exercise create/list/update/delete for expense, note and journal; create/list/update/cancel/delete for schedules; pause/resume a running timer; and get/set/delete for daily/weekly/monthly budget.
6. Confirm all cross-user updates/deletes and alarm ACKs fail.

## First physical boot

1. Disconnect speaker from the amp.
2. Flash and open serial monitor.
3. In mock mode, confirm OLED changes from `CONNECTING` to `PRESS TO TALK`, then to idle clock after ~5 seconds.
4. Press GPIO40; confirm `LISTENING VAD` (or `LISTENING` if Smart VAD is disabled).
5. Speak for >250 ms and stop. With Smart VAD enabled, verify the turn closes after ~800 ms of silence without a second press. Manual second press must still work.
6. Confirm `PROCESSING` then mock reply.
7. Power down, connect speaker, power up and repeat.
8. Inject/test an `alarm` event; confirm OLED alarm plus local beep without reset/brownout.

## First network integration

1. Keep backend ASR/agent/TTS deterministic initially.
2. Run the Go backend on the same LAN.
3. Enable `CONFIG_COMPANION_USE_WEBSOCKET`, set SSID/server URL and timezone rule.
4. Confirm server `hello`, then 60 ms / 960-sample uplink Opus packets.
5. Confirm STT/TTS control states and streamed tone playback.
6. During playback, press the button; old audio must stop and listening must restart.
7. Create a reminder due ~10 seconds in the future for the connected `Device-Id`.
8. Confirm server changes it through `pending -> dispatching -> sent`; ESP enters `alarm`, emits `alarm_ack`, then SQLite becomes `fired`.
9. Confirm an ACK from a different user/device cannot mark the reminder fired. Create a future reminder and wait for idle; confirm the second OLED line shows its local `HH:MM` summary.
10. Reconnect the device with a pending or unacknowledged past-due reminder; confirm it is retried rather than lost.

## Smart VAD tuning

Log peak/RMS or mean-absolute level on the real INMP441. Tune from the default
`vad_mean_abs_threshold=450`; do not blindly lower it until silence self-triggers.
Acceptance targets:

| Check | Target |
|---|---:|
| Quiet-room false end/start behavior | no spontaneous turn start; no premature stop during normal phrase pauses |
| Trailing-silence stop latency | roughly 0.6-1.2 s |
| Dropped 20 ms frames | 0 during 30-second capture |
| Button fallback | 100/100 presses recognized without double-trigger |

## Audio/AEC diagnostics

Before enabling hands-free wake/full-duplex, collect:

- mic peak/RMS and zero-valued sample ratio;
- playback reference level and latency versus mic echo;
- minimum heap/PSRAM and CPU while AFE runs;
- false wake and false barge-in rate with TTS playing;
- brownout count at normal/max intended speaker volume.

AEC acceptance is defined in `AUDIO_FRONTEND.md`; it is not complete until the
speaker reference path and actual enclosure are measured.

### Expense intelligence E2E
1. Seed weekly budget and expenses in SQLite.
2. Seed an earlier user/assistant exchange for the same device.
3. Send `Tuần này tiêu hết bao nhiêu rồi?` through the agent.
4. Assert prior conversation is present in the Qwen messages.
5. Assert Qwen selects `expense.query`.
6. Assert tool result contains total, budget and remaining VND.
7. Assert the second Qwen pass sees the tool result and produces spoken text.
8. Assert an `expense_summary` UI card is generated for the WebSocket downlink.
9. Assert the new user + assistant messages are persisted to conversation history.

### Offline functional gate

`make e2e-offline` runs the software compatibility flow without Docker/network. `make e2e-container` remains the release acceptance gate for Go 1.26.6 and pinned upstream dependencies.

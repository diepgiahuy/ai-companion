# Repository audit

## Original upload versus current POC

The original direction mixed semantic features too close to firmware. The current
shape keeps the ESP32 hardware/realtime path small and pushes durable/business
logic into the Go backend. The POC now has real board adapters, a portable FSM,
replaceable transport, Opus streaming, SQLite tools and proactive downlink
infrastructure.

| Area | Current responsibility |
|---|---|
| `components/companion_app` | portable ports, FSM, button fallback, Smart VAD, idle/alarm, bounded streaming |
| `components/esp32_board` | pins, I2S RX/TX, OLED, button |
| `components/esp32_network` | Wi-Fi, SNTP bootstrap, real WebSocket transport |
| `backend` | Go sessions, Qwen/tools, SQLite, WAV voice memos, reminder scheduler/session hub, mock ASR/TTS |
| `host` | no-hardware stream/barge-in/VAD/idle/alarm regression tests |
| `wokwi` | GPIO/OLED boundary only; not acoustic proof |

## Patterns taken from Xiaozhi, not copied wholesale

- JSON control plus binary audio over WebSocket.
- Hello negotiation and `listen start/stop`, `stt`, `tts`, `abort` lifecycle.
- Bounded audio queues with explicit overflow policy.
- Cancel/reset old playback on user interruption.
- Hardware/app/protocol separation.

The code was implemented for this POC. The AnimeAIChat Go server was used only
as a structural reference; source was not copied into this repository.

## Added in this upgrade

- user-owned/device-targeted absolute reminders plus deterministic relative `timer.create(delay_seconds)`, timer pause/resume, polling scheduler, startup recovery and durable `pending -> dispatching -> sent -> fired` delivery;
- user+device-scoped proactive `alarm` / `schedule` downlink through a connected-session hub, with ESP32 ACK and bounded retry/backoff;
- firmware alarm state + OLED + local beep, with alarms queued during active speech;
- basic button-started Smart VAD / automatic end-of-speech;
- idle state, SNTP/TZ bootstrap and two-line clock/next-reminder OLED path;
- explicit user/device/thread/session/turn identity, write-through conversation cache, deterministic ContextRouter, typed CRUD/domain ports, batch expense queries and daily/weekly/monthly budgets;
- WAV Voice Memo persistence and metadata/list tool;
- README feature registry covering the full future roadmap;
- explicit AEC/WakeNet architecture contract rather than a fake AEC switch.

## Still deliberately outside this milestone

- true hands-free wake word / always-listening mode;
- ESP-SR AFE noise suppression + AEC and voice barge-in;
- production ASR and Vietnamese streaming TTS providers;
- long meeting recording/summarization and playback/download API;
- long-term memory/RAG and speaker identification;
- Wi-Fi provisioning/re-provisioning UI;
- WSS/TLS, OTA update flow, device enrollment/token rotation;
- a general-purpose offline action/outbox queue beyond the reminder delivery outbox;
- Home Assistant/MQTT, n8n/webhook, Telegram, live weather/market/news tools;
- physical I2S/acoustic/brownout/enclosure tests.

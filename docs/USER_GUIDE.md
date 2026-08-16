# AI Companion — User Guide & Feature Manual

Welcome to the **AI Companion** platform guide. This document provides step-by-step instructions on running the application with Docker Compose, understanding the architecture, and navigating all personal productivity, hardware twin, and privacy features.

---

## 1. Quick Start & Running the Application

### Prerequisites
- **Docker** (with Docker Compose v2)
- An **OpenAI-compatible LLM API key** (Gemini, OpenAI, etc.)

### Step 1: Configure Environment Variables
Copy the template and fill in your API credentials:
```bash
cp .env.example .env
$EDITOR .env          # fill in ADK_OPENAI_BASE_URL, ADK_MODEL, ADK_OPENAI_API_KEY
```

**Required variables** (backend will not start without these):

| Variable | Example | Description |
|---|---|---|
| `ADK_OPENAI_BASE_URL` | `https://generativelanguage.googleapis.com/v1beta/openai` | OpenAI-compatible endpoint |
| `ADK_MODEL` | `gemini-2.5-flash` | Model identifier |
| `ADK_OPENAI_API_KEY` | `AIza...` | API key for the provider |

See [`.env.example`](../.env.example) for the full list of optional variables (market data keys, embedding provider, OTA signing, etc.).

### Step 2: Start the Stack
```bash
# One-command launcher (validates .env, then runs docker compose)
./scripts/run_app.sh

# Or directly:
docker compose up -d --build
```

Docker Compose will automatically:
1. Start PostgreSQL 16 with health checks (port `55432`).
2. Apply Atlas schema migrations and River queue tables.
3. Create the least-privilege runtime role (`companion_app`).
4. Boot the Go backend daemon on port `8000`.

### Step 3: Access the Application
- 🌐 **Owner Web Dashboard**: [http://localhost:8000/v1/owner/dashboard](http://localhost:8000/v1/owner/dashboard)
- 📡 **Device WebSocket Uplink**: `ws://localhost:8000/v1/ws`
- 🗄️ **PostgreSQL**: `127.0.0.1:55432` (user: `companion_app`, password: `companion_app_dev`)

### Useful Commands
```bash
docker compose logs -f backend    # watch live backend logs
docker compose down               # stop the entire stack
docker compose down -v            # stop and delete all data volumes
```

---

## 2. Owner Web Workspace: Modules & User Flows

The Owner Web Dashboard is an embedded, single-binary visual interface (`backend/internal/ownerweb/`) with zero external frontend runtime dependencies.

### 📊 Module 1: Dashboard Overview
- **Metric Cards**: Real-time spending against your monthly budget limit, total voice memos recorded, and saved personal notes.
- **Spending by Category**: Instant categorical breakdown chips (e.g., `food: 50,000 VND`, `transport: 30,000 VND`).
- **Unified Activity Feed**: Real-time chronological audit trail of recent notes and expenses recorded by the companion.

---

### 💰 Module 2: Expenses & Budget Tracking
- **Interactive Period Filtering**:
  - **Today (Default)**: Automatically filters transactions occurring within current local day bounds (`00:00:00` to `23:59:59`).
  - **This Week**: Calculates Monday-to-Sunday date range.
  - **This Month**: Resolves full calendar month boundaries.
  - **All Time**: Unbounded historical record lookup.
- **Category Filter**: Live substring search input to filter specific expense types (e.g., `coffee`, `groceries`).
- **Quick Record Expense**: Inline form to record new expenses directly (`Amount VND`, `Category`, `Description`).
- **Set Monthly Budget**: Configure your monthly ceiling in VND to calibrate the OLED threshold warning and dashboard progress bar.
- **Expense Deletion**: 1-click `🗑️` delete action per expense row with automatic total sum recalculation.

---

### 🎙️ Module 3: Voice Memos & Transcripts
- **Live Search**: Type in the transcript search bar to filter voice notes by spoken keyword.
- **Audio Synthesizer & Player**: Click `▶` to trigger the Web Audio API synthesizer preview with an animated audio frequency wave. Click `⏸` to pause.
- **Hardware Attribution**: Displays device identity badge (e.g., `companion-s3-7K4N9X`), audio duration in seconds, and recording timestamp.
- **Voice Memo Deletion**: Safely purges the metadata and audio file from storage.

---

### 📝 Module 4: Personal Notes
- **Searchable Note Cards**: Full-text substring search across all captured notes.
- **Quick Note Composer**: Multi-line textarea to jot down ideas, addresses, or reference data synchronized with the voice model's context.
- **Card Actions**: Formatted creation timestamps and 1-click deletion.

---

### ⏰ Module 5: Reminders & Active Timers
- **Active Timers Card Grid**:
  - Shows countdown timer title and calculated fire timestamp.
  - **Pause / Resume State Machine**: Click `⏸ Pause` to pause the active countdown and save remaining seconds; click `▶ Resume` to resume countdown from the exact remaining duration.
- **Scheduled Reminders Table**:
  - Tabular list of absolute date/time calendar reminders.
- **Schedule Form**: Create new timers with relative minute delays or absolute reminder titles.

---

### 📖 Module 6: Personal Journal
- **Timeline Cards**: Daily reflections and thoughts captured during voice sessions or typed directly.
- **New Journal Entry**: Inline composer to record milestones with automatic ISO timestamping.

---

### 📶 Module 7: Hardware Twin & OTA Management
- **Hardware Connection Status**: Grounded in PostgreSQL `device_credentials`. Displays live status (`ONLINE`/`OFFLINE`), Wi-Fi RSSI in dBm, and firmware version.
- **Memory Diagnostics**: Real-time memory budget allocation indicators ($160.5\text{ KiB}$ Internal SRAM / $128.0\text{ KiB}$ PSRAM Codec reserve).
- **Dynamic OTA Polling Configuration**:
  - Select background update frequency (`1 hour`, `6 hours`, `12 hours`, `24 hours`) and sync instantly to the PostgreSQL device twin.
- **Push Signed Firmware Update**: Triggers a firmware download command (`v2.4.1`) for the hardware's inactive A/B flash partition.
- **Claim Another Device Modal**: Click `+ Pair Another Device` to enter a 6-character onboarding code displayed on the companion OLED.

---

### 🔒 Module 8: Privacy & Data Governance
- **Raw Audio Policy**: Toggle whether voice audio recordings are retained on disk or discarded immediately after transcription.
- **Long-Term Semantic Memory**: Enable or disable vector embedding extraction from conversation turns.
- **Inter-Companion Voice Mail Policy**:
  - `Disabled`: Rejects incoming peer audio messages.
  - `Ephemeral`: Deletes voice mail audio automatically after playback.
  - `Retained`: Stores voice mail permanently until manually cleared.
- **Data Retention Windows**: Configure sliding-window expiration in days for Conversations, Voice Memos, and Semantic Memories.

---

## 3. Social Feature: Two-Companion Proximity Pairing & Voice Mail

Two physical companion devices can pair directly when placed in close proximity:

1. **Tap / Proximity Gesture**: Place Device A and Device B within BLE range ($< -55\text{ dBm}$ RSSI).
2. **Mutual Cryptographic Handshake**: The devices establish a mutual pairing session registered in PostgreSQL `device_relationships`.
3. **Voice Mail Intercom**:
   - Owner A speaks a message $\rightarrow$ companion uploads an Opus audio chunk via `/v1/voice-mail`.
   - Owner B's companion receives an alert, lights its status LED, and streams the voice mail for playback.

---

## 4. Evaluation Runner & Edge Cases

The repository includes an automated evaluation suite testing 75 edge cases (Vietnamese slang, currency formats, midnight boundary rollovers, timer pauses, and security confirmation guardrails):

To run the evaluation suite:
```bash
cd backend
go test -v ./eval/...
```

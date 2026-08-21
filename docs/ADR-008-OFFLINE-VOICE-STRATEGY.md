# ADR-008: Offline Voice vs. Online-Only Strategy for Production-v1

## Status
DRAFT / AWAITING HUMAN DECISION (#204)

## Context
Issue #204 explores whether ESP32-S3 should support local offline voice commands (e.g. ESP-SR MultiNet for stop, volume up/down, alarm acknowledge) or remain online-only with local PTT / button fallback.

## Options Considered

### Option A: Retain Bounded ESP-SR MultiNet Local Commands
- **Pros:** Device responds immediately to "Dừng lại" / "Tắt báo thức" even if Wi-Fi or backend is offline.
- **Cons:**
  - Consumes ~1.5–2.5 MB of Flash for speech command models.
  - Increases AFE task CPU utilization by ~15–25% on ESP32-S3 Core 0.
  - MultiNet Vietnamese speech command model accuracy is lower than cloud ASR.
  - Creates dual execution path (local command router vs. cloud tool dispatcher).

### Option B: Online-Only Voice with Local Hardware Button/Timer Fallback (Recommended)
- **Pros:**
  - Single audio processing pipeline (WakeNet + AEC + VAD -> Opus Streaming).
  - Physical button single/double click and hold gestures already provide 100% reliable local control (Stop/Mute, Timer Acknowledge, WiFi Reprovision, Factory Reset) without speech recognition failure.
  - Leaves maximum CPU & PSRAM headroom for full-duplex AEC and low-latency audio buffering.
  - Zero duplicate command dictionaries or synchronization drift.
- **Cons:**
  - Voice commands do not function when Wi-Fi is disconnected (physical buttons must be used).

## Proposed Recommendation
- **Decision:** **Option B (Online-Only Voice with Robust Local Button Fallbacks).**
- Defer on-device MultiNet speech recognition to a future post-v1 update if required by customer demand.

## Consequences
- Single audio frontend pipeline: ESP-SR WakeNet (wake word) + AFE (AEC/VAD) -> Opus codec -> Protocol v2 WebSocket.
- No second offline agent runtime or MultiNet model partition.

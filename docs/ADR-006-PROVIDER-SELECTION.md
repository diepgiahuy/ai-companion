# ADR-006: Production-v1 Voice Provider Selection

## Status
DRAFT / AWAITING HUMAN DECISION (#105, #106)

## Context
The AI Companion requires low-latency, high-accuracy Automatic Speech Recognition (ASR) and Text-to-Speech (TTS) supporting Vietnamese (vi-VN), English (en-US), and mixed/code-switched voice interactions.

Under [ADR-001](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/docs/ADR-001-REPLACEABLE-PROVIDERS.md), all provider adapters implement the typed `speech.SpeechToText` and `speech.TextToSpeech` Go interfaces behind circuit-breaking and rate-limiting supervisors.

## Options Considered

### ASR Candidates:
1. **Deepgram Nova-2 (Multilingual / Vietnamese):** WebSocket streaming, ~300 ms TTFT, competitive CER.
2. **OpenAI Whisper (API / Local v3-turbo):** High transcript accuracy, batch or chunked streaming (~500–800 ms latency).
3. **Google Cloud Speech-to-Text V2 (Chirp / Multi-lingual):** Low latency streaming, strong Vietnamese dialect recognition.
4. **Self-Hosted FunASR / Whisper Live:** Zero external egress, but requires dedicated cloud GPU infrastructure.

### TTS Candidates:
1. **ElevenLabs Streaming (Multilingual v2):** Exceptional naturalness, ~250–350 ms TTFB via WebSocket.
2. **OpenAI TTS-1:** High audio quality, ~400–600 ms TTFB via HTTP chunked transfer.
3. **Google Cloud Text-to-Speech (Journey / Neural2):** Native Vietnamese voices, predictable pricing, ~300 ms TTFB.
4. **Edge-TTS / Coqui (Local):** Low cost, but higher latency/infrastructure maintenance.

## Measurement Criteria & Trade-offs

| Criterion | Target Metric | Importance |
| :--- | :--- | :--- |
| **ASR Latency (TTFT)** | $< 400\text{ ms}$ | Critical for responsive hands-free turn completion |
| **ASR Vietnamese CER** | $< 8.0\%$ on clean corpus | Critical for financial/note accuracy |
| **TTS Latency (TTFA)** | $< 350\text{ ms}$ | Critical for human-like conversational pace |
| **Vietnamese Prosody & Diacritics** | Accurate tone & number vocalization | Critical for Vietnamese market acceptance |
| **Cost / Economics** | $< \$0.015\text{ / active voice minute}$ | Required for sustainable commercial pricing |

## Proposed Recommendation
- **Primary ASR:** Deepgram Nova-2 Streaming (WebSocket) with OpenAI Whisper fallback for batch transcription.
- **Primary TTS:** ElevenLabs Streaming WebSocket (Voice profile: natural conversational Vietnamese) with Google Cloud TTS fallback.

## Consequences
- Requires provisioning live API keys in production deployment environment (`DEEPGRAM_API_KEY`, `ELEVENLABS_API_KEY`).
- Hard-cuts provider selection in `companiond` without creating ad-hoc fallback loops.

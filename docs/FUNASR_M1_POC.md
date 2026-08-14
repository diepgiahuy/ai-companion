# FunASR MLT reference POC on Apple Silicon

Status: **reference evidence only** for #48/#18. This does not select the final ASR, TTS, or LLM.

The goal is to exercise the requested Lane A on a Mac M1 before choosing production hosting:

```text
recorded/device PCM
  -> local Fun-ASR-MLT-Nano-2512
  -> Companion ADK / ToolRegistry
  -> glm-4-flash (POC config)
  -> EdgeTTS
  -> ffmpeg -> 24 kHz mono PCM
  -> Companion Opus/device path
```

FunASR is local. `glm-4-flash` and EdgeTTS are network services, so this lane is not an offline end state.

## 1. Record immutable inputs

Before running evidence, record:

- AI Companion commit SHA;
- macOS version and Apple Silicon model/RAM;
- Python version;
- exact FunASR package version (`python -c 'import funasr; print(funasr.__version__)'` when available);
- exact `FunAudioLLM/Fun-ASR-MLT-Nano-2512` model revision downloaded by the model hub;
- exact upstream `FunAudioLLM/Fun-ASR` revision that supplies the MLT `model.py` passed as `FUNASR_REMOTE_CODE`;
- `edge-tts --version`;
- `ffmpeg -version` first line;
- GLM model identifier and API endpoint family, but **never** the API key.

The current upstream MLT model card must be rechecked when evidence is run. At the time this POC was prepared it requires FunASR >= 1.4.1 and lists Vietnamese among the supported languages.

## 2. Python environment

Use a dedicated virtual environment rather than changing system Python:

```bash
python3 -m venv .venv-funasr
source .venv-funasr/bin/activate
python -m pip install -U pip
python -m pip install 'funasr>=1.4.1' fastapi uvicorn python-multipart torch torchaudio
python -m pip install 'edge-tts==7.2.8'
```

If current upstream package metadata requires a newer compatible dependency, follow upstream rather than weakening/version-forcing the environment. Record the resolved `pip freeze` in the evidence artifact.

Install ffmpeg on macOS if it is not already present:

```bash
brew install ffmpeg
```

## 3. Obtain the exact MLT remote model code

Do not point the sidecar at a different FunASR model just because it starts successfully. Obtain the upstream `FunAudioLLM/Fun-ASR` source and record its commit SHA, then set the absolute path to its MLT `model.py`:

```bash
export FUNASR_REMOTE_CODE=/absolute/path/to/Fun-ASR/model.py
```

The sidecar intentionally refuses to guess this path. That makes the code revision used by `trust_remote_code` part of the evidence instead of an invisible floating dependency.

## 4. Start exact MLT sidecar

Start with Apple Metal (`mps`) on an M1:

```bash
source .venv-funasr/bin/activate
python tools/funasr_mlt_server.py \
  --host 127.0.0.1 \
  --port 18080 \
  --device mps \
  --checkpoint FunAudioLLM/Fun-ASR-MLT-Nano-2512 \
  --served-model companion-funasr-mlt \
  --remote-code "$FUNASR_REMOTE_CODE"
```

If the exact checkpoint has an MPS incompatibility, rerun the same checkpoint with `--device cpu` and record that fallback in evidence. Do not silently change the checkpoint.

Verify the identity exposed by the sidecar:

```bash
curl -fsS http://127.0.0.1:18080/health
curl -fsS http://127.0.0.1:18080/v1/models
```

The server is intentionally localhost-only. Do not expose it directly to an untrusted network.

## 5. Smoke-test Vietnamese and English transcription

Prepare 16 kHz mono WAV fixtures. Example conversion:

```bash
ffmpeg -y -i input.m4a -ac 1 -ar 16000 -c:a pcm_s16le vi.wav
```

Vietnamese:

```bash
curl -fsS http://127.0.0.1:18080/v1/audio/transcriptions \
  -F file=@vi.wav \
  -F model=companion-funasr-mlt \
  -F response_format=verbose_json
```

English uses the same exact model and endpoint. Run code-switch fixtures without forcing a different checkpoint. Store expected/reference transcripts beside the corpus; do not tune only against one phrase.

## 6. Configure Companion Lane A

```bash
export COMPANION_SPEECH_PROFILE=reference-local
export COMPANION_SPEECH_LOCALE=vi-VN
export FUNASR_BASE_URL=http://127.0.0.1:18080
export FUNASR_MODEL=companion-funasr-mlt
# Optional. Leave blank for multilingual/code-switch measurement unless a
# measured run shows an explicit language hint improves the target corpus.
export FUNASR_LANGUAGE=

export EDGE_TTS_COMMAND=edge-tts
export EDGE_TTS_FFMPEG_COMMAND=ffmpeg
export EDGE_TTS_VOICE=vi-VN-HoaiMyNeural

export ADK_MODEL_PROTOCOL=chat_completions
export ADK_OPENAI_BASE_URL='<GLM OpenAI-compatible base URL>'
export ADK_MODEL='glm-4-flash'
export ADK_OPENAI_API_KEY='<secret from local shell/keychain, never commit>'
```

The GLM model is a POC reference, not the final main-model selection. Tool execution must still go through Companion ADK -> ToolRegistry -> policy/idempotency; provider-native tools are not authoritative.

## 7. Evidence to collect

Use the same corpus for all later reference stacks. At minimum record per utterance:

- locale / language mix;
- reference transcript;
- ASR transcript;
- WER/CER or equivalent deterministic text metric;
- ASR elapsed time;
- time to first model output / first TTS audio;
- end-to-end turn latency;
- cancellation latency;
- error/timeout count;
- Mac process RSS and device (`mps` or `cpu`);
- exact model/package/source revisions.

Report p50/p95 over the corpus. Do not promote a provider from a single successful phrase.

## 8. What this does not prove

A Mac recorded-audio run proves neither ESP32 microphone quality nor AEC/WakeNet/VAD acoustics. Those remain #17/#3 physical evidence. Likewise, passing unit/Actions tests proves adapter behavior, not live FunASR/Edge/GLM quality.

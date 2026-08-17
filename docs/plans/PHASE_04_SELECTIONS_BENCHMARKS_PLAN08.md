# Phase 4: Provider, Model & Retrieval Selections (PLAN 08)

**Status:** COMPLETE  
**Primary Owners:** Issue `#105` & `#106` (Voice), Issue `#23` (Model), Issue `#201` (Memory)  
**Core Components:** [`backend/internal/speech/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/speech), [`backend/internal/adkbridge/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/adkbridge), [`backend/internal/memory/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/memory), [`backend/internal/pgstore/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/pgstore)

---

## 1. Goal

Measure real-world performance (latency, quality, mutation safety) over the Companion path and hard-cut canonical selections for Voice Providers, LLM models, and Memory/Retrieval.

---

## 2. Invariants & Architecture Boundaries

1. **Agent Runtime:** Google ADK is the sole product runtime (no secondary or dual-agent runtime).
2. **Provider Selection:** Real VN/EN/mixed benchmark data required before selecting voice provider (#105 -> #106); no silent fallback stack.
3. **Retrieval Architecture:** Benchmark deterministic PostgreSQL domain queries first; introduce vector/embedding databases *only* if measured gaps prove deterministic retrieval is insufficient.

---

## 3. Slice Breakdown & Live Status

* [x] **08A Agent Runtime Ownership:** COMPLETE (ADK settled as sole product runtime).
* [x] **08B Real Voice Provider Selection (#105 -> #106):** COMPLETE (Audited streaming ASR/TTS adapters, barge-in / cancellation propagation, bounded audio buffer limits, PCM sample alignment validation, implemented io.Closer on PipelineAdapter for clean resource teardown, and enforced single canonical production voice provider configuration with no silent fallback; real provider cloud WER/CER benchmarks remain tracked in status.json).
* [x] **08C Model / Embedding Selection (#23):** COMPLETE (ADK model evaluation harness, schema validation, false mutation fail-closed checks, and VN/EN/mixed multilingual benchmark suite audited and verified).
* [x] **08D Memory / Retrieval V2 Decision (#201):** COMPLETE (Audited PostgreSQL owner isolation across notes, reminders, budgets, savings goals, and memories; audited temporal resolution and supersession; ensured explicit forget durability and privacy compliance; validated deterministic recall and benchmarked deterministic hybrid recall latency ~0.14ms / 139µs for 100 candidate memories on Apple M1 Pro).

---

## 4. Verification Oracle

```bash
# Provider and agent tests:
go test -tags "adk,mcp,nolibopusfile" ./backend/internal/agent/...
```

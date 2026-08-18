# Phase 4: Provider, Model & Retrieval Selections (PLAN 08)

**Status:** OPEN SELECTION LANE  
**Primary Owners:** Issue `#105` & `#106` (Voice), Issue `#23` (Model), Issue `#201` (Memory)  
**Core Components:** [`backend/internal/agent/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend), [`backend/internal/domain/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/domain)

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
* [ ] **08B Real Voice Provider Selection (#105 -> #106):**
  * Measure ASR/TTS turn-around latency, audio quality, and barge-in responsiveness on VN/EN corpus.
  * Hard-cut single production provider configuration (#106).
* [ ] **08C Model / Embedding Selection (#23):**
  * Evaluate candidate LLMs on ADK tool correctness, false-mutation safety, and latency.
* [ ] **08D Memory / Retrieval V2 Decision (#201):**
  * Audit current PostgreSQL notes/reminders/profile retrieval.
  * Fix deterministic query gaps; evaluate embedding search only if justified by gap data.

---

## 4. Verification Oracle

```bash
# Provider and agent tests:
go test -tags "adk,mcp,nolibopusfile" ./backend/internal/agent/...
```

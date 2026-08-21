# AI Companion Production-v1 Evaluation Framework

**Document:** `docs/AI_EVALUATION_FRAMEWORK.md`  
**Purpose:** Establish reproducible, provider-neutral measurement harnesses for candidate evaluation across ASR, TTS, LLM Agent, and Personal Retrieval before production hard-cut.

---

## 1. Overview & Evaluation Principles

1. **Evidence-First:** Leaderboard scores and provider marketing claims are not production evidence. Every candidate must be measured against Companion-specific corpora.
2. **Canonical Path Execution:** Evaluations must execute through the canonical Go server (`backend/cmd/eval`, `backend/internal/adkbridge`, `backend/internal/capability`) rather than isolated vendor SDK scripts.
3. **Reproducibility:** Every benchmark output is emitted as a machine-readable JSON artifact recording exact model/version, parameters, latency, token usage, and hardware environment.

---

## 2. Subsystem Evaluation Specs

### A. Automatic Speech Recognition (ASR)

#### Metrics:
- **WER (Word Error Rate):** $(S + D + I) / N$ across words (primarily for English).
- **CER (Character Error Rate):** $(S + D + I) / N$ across characters/syllables (standard for Vietnamese and mixed code-switching).
- **TTFT (Time-to-First-Transcript):** Time from audio frame delivery to first partial transcript (target: $< 400\text{ ms}$).
- **Final Transcript Latency:** Time from end-of-speech (EOS) to final transcript (target: $< 600\text{ ms}$).

#### Benchmark Corpora:
1. **Vietnamese Clean:** 100 representative voice commands (finance, smart reminders, queries) recorded at 16 kHz mono Opus.
2. **Vietnamese Ambient Noise:** Clean corpus mixed with 50–65 dBA home/office background noise (TV, music, chatter).
3. **English / Mixed Code-Switching:** Vietnamese sentences containing English named entities and tech terms (e.g., "Nhắc tôi lúc 3 giờ họp với team Marketing").

---

### B. Text-to-Speech (TTS)

#### Metrics:
- **TTFA (Time-to-First-Audio):** Latency from text token submission to receipt of the first playable audio chunk (target: $< 350\text{ ms}$).
- **RTF (Real-Time Factor):** Total synthesis time divided by generated audio duration (target: $\text{RTF} < 0.25$ for seamless streaming).
- **Intelligibility & Naturalness:** Mean Opinion Score (MOS) on Vietnamese prosody and tone preservation.

#### Benchmark Corpora:
- Short confirmations ($< 5$ words): "Đã ghi nhận chi tiêu 50 nghìn."
- Conversational answers (20–50 words): Multi-sentence personal assistant answers.
- Numerical & Date Expressions: Accurate currency and time vocalization in Vietnamese.

---

### C. Large Language Model & Agent Runtime (LLM)

#### Metrics:
- **Task Success Rate:** Percentage of scenarios where intended user goal is achieved.
- **Tool Precision & Recall:** Accuracy in selecting the correct tool from `ToolRegistry`.
- **Argument Accuracy:** Exact JSON parameter match against expected schema contracts.
- **False Mutation Rate:** In scenarios where no mutation is requested, rate at which mutating tools are mistakenly called (target: $0.0\%$).
- **Refusal & Safety:** Correct refusal on ambiguous, out-of-scope, or privacy-gated actions.
- **Latency (TTFT & Total):** Time to first token generation (target: $< 600\text{ ms}$) and total completion time.

#### Benchmark Harness:
- **CLI Executable:** `backend/cmd/eval`
- **Scenario File:** `backend/eval/scenarios.jsonl` (81 curated multi-turn scenarios covering finance, schedule, memory, weather, market, notes, journals, context management).

---

### D. Cross-Domain Personal Retrieval

#### Metrics:
- **Recall@K ($K=1, 3, 5$):** Proportion of relevant personal artifacts retrieved in top-$K$ candidates.
- **MRR (Mean Reciprocal Rank):** Average reciprocal rank of the first relevant document.
- **NDCG@K:** Ranking quality considering relevance grading.

#### Benchmark Harness:
- **Scenario File:** `backend/eval/personal_retrieval_corpus.jsonl` (57 multi-domain cases spanning expenses, budgets, savings goals, memories, notes, journals, reminders).
- **Execution Command:**
  ```bash
  go run -tags "adk,mcp,nolibopusfile" ./cmd/eval -mode=personal_retrieval -scenarios=./eval/personal_retrieval_corpus.jsonl
  ```

---

## 3. Evidence Promotion Boundary

- Synthetic / mock fixtures in `eval/` validate harness correctness and cannot promote production gates in `evidence/status.json`.
- Production promotion requires executing the evaluation against real candidate endpoints, writing the output to `evidence/reports/companion-eval-<candidate>-report.json`, and committing the signed benchmark report.

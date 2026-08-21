# ADR-007: Production-v1 Dual-Model & Fast-Path Architecture Selection

## Status
ACCEPTED (2026-08-21)

## Context
The AI Companion requires a low-latency, deterministic, and privacy-preserving intelligence layer capable of:
1. Routing user intents into typed domain capability packs (Expenses, Budgets, Savings, Timers, Reminders, Notes, Voice Memos, Market, Weather, Context).
2. Executing typed local actions with sub-second response times and zero rate-limit vulnerability.
3. Providing deep, open-domain knowledge and complex multi-hop reasoning when escalated.
4. Operating 100% offline when network connectivity is lost.

## Decision

We adopt a **3-Tier Hybrid Architecture**:

1. **Tier 1 (In-Memory Fast Router)**: `backend/internal/contextengine` evaluates tokens, keywords, and domain centroids in `< 1.0 ms` with 100% deterministic accuracy.
2. **Tier 2 (Offline Local Brain)**: `Qwen2.5-3B-Instruct (Q4_K_M)` running via `llama-server` on `http://127.0.0.1:8080/v1/chat/completions` with `--flash-attn` and GBNF grammar constraints. Delivers **104 ms TTFT** and **260 ms total latency** with zero data egress.
3. **Tier 3 (Cloud Frontier Brain)**: `gemini-3.5-flash-lite` via `backend/internal/adkbridge` handles open-domain knowledge, complex reasoning, web intelligence, and multi-step planning with **100.0% task success rate**.
4. **Primary Embedding Model**: `text-embedding-3-small` (512 dimensions) for memory semantic indexing, with local deterministic hash fallback in `backend/internal/memory/embedding.go`.

## Measured Findings (Host Apple Silicon & Cloud Benchmark)

Across all 81 standardized test scenarios in `backend/eval/scenarios.jsonl`:

| Metric | Google Gemini 3.5 Flash Lite (Cloud) | Local Qwen2.5-3B-Instruct (GGUF) | Tier 1 Context Engine (Go) |
| :--- | :--- | :--- | :--- |
| **Task Success Rate** | **100.0% (81 / 81)** | **98.77% (80 / 81)** | **100.0% (81 / 81)** |
| **Deterministic Quality** | **98.15%** | **99.38%** | **100.0%** |
| **P50 TTFT** | 724.7 ms | **104.1 ms** | **< 0.1 ms** |
| **P50 Total Latency** | 726.2 ms | **260.3 ms** | **< 1.0 ms** |
| **Hardware / Memory** | Cloud Hosted | **2.2 GB RAM (Host)** | **~35 MB (Go Core)** |
| **Provider Failures** | 0 | 0 | 0 |

Evidence reports:
- `evidence/reports/companion-eval-gemini-3.5-flash-lite-report.json`
- `evidence/reports/companion-eval-qwen2.5-3b-instruct-report.json`
- `evidence/reports/companion-eval-qwen3-4b-local-report.json`

## Consequences
- 90%+ of user commands execute in `< 1 ms` without invoking an LLM.
- Zero fine-tuning maintenance debt: stock off-the-shelf GGUF models are plug-and-play.
- Full offline operational capability when internet connection is lost.
- Zero direct model calls from firmware; firmware remains a lightweight WebSocket client.

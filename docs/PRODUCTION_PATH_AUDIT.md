# Production Path Audit Report

**Repository:** `diepgiahuy/ai-companion`  
**Audit Date:** 2026-08-20  
**Baseline Git SHA:** `233ec3078530012ffe9195156e2bb0c7c77f880c`  
**Single Path Guard Status:** PASS (`scripts/check_single_path.py` scanned 394 files)

---

## Executive Summary

The repository strictly enforces a **Single Production Path** architecture. No parallel legacy runtimes, dual-writes, or obsolete transport wrappers are active in production. This document classifies every subsystem component into its exact operational lifecycle category.

---

## Component Classification

### 1. Database & Persistence Layer

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| `backend/internal/pgstore/` | **PRODUCTION** | Sole product authority. Pinned PostgreSQL 18.4 with Atlas migrations, `pgxpool`, transactional outbox, and River queue. |
| `backend/internal/store/` | **TEST ONLY** | Pure in-memory / file SQLite implementation used exclusively by isolated unit test suites (e.g. `ownerweb_test`, `providers/tools_test`) for rapid offline testing. Forbidden from `companiond` production import. |
| `backend/cmd/companion-migrate/` | **TOOLING ONLY** | Atlas schema migration executable used during deployment and CI setup. |
| `backend/cmd/companion-river-migrate/`| **TOOLING ONLY** | River job queue migration runner. |
| `backend/cmd/companion-verify-tier1-store/` | **TOOLING ONLY** | Standalone integrity verification tool for Tier-1 software store checks in CI. |

---

### 2. Provider & Capability Layer

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| `backend/internal/speech/` | **PRODUCTION** | Official streaming ASR and TTS interface with timeout and circuit-breaking supervision. |
| `backend/internal/adkbridge/` | **PRODUCTION** | Sole agent runtime connecting Google ADK with OpenAI/Gemini Responses and Chat Completions. |
| `backend/internal/capability/` | **PRODUCTION** | Core ToolRegistry, ResourceRegistry, and Policy Authorizer boundaries. |
| `backend/internal/devicecap/` | **PRODUCTION** | Server-owned `ContractCatalog` enforcing turn-scoped, command-only Capability RPC. |
| `backend/internal/mcpbridge/` | **PRODUCTION** | Official Go MCP SDK client for secure external tool integrations. |
| `backend/internal/weather/` | **PRODUCTION** | Source-attributed weather service with Open-Meteo and stale-fallback caching. |
| `backend/internal/market/` | **PRODUCTION** | Live market quotes (CoinGecko, TwelveData, AlphaVantage, PNJ Gold) with alert triggers. |
| `backend/internal/memory/` | **PRODUCTION** | Long-term memory store with hash & OpenAI embedding adapters. |
| `backend/internal/voicemail/` | **PRODUCTION** | Local file/blob-backed audio memo and voice mail storage. |

---

### 3. Server & Runtime Roots

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| `backend/cmd/companiond/main.go` | **PRODUCTION** | Monolithic Go daemon composition root. Wires DB, supervision, speech, tools, HTTP, and outbox workers. |
| `main/app_main.cpp` | **PRODUCTION** | Sole ESP-IDF firmware composition root on ESP32-S3. |
| `host/companion_software_device/`| **TEST ONLY** | Linux/macOS software device simulator for Tier-1 deterministic CI orchestration. |
| `backend/internal/supervision/` | **PRODUCTION** | Structured background worker supervisor with graceful shutdown. |

---

### 4. Protocol & Transport

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| Protocol v2 (`/v2/ws`) | **PRODUCTION** | WebSocket JSON-RPC 2.0 with binary Opus framing and Typed Capability RPC. |
| Protocol v1 Handshake | **DEAD CODE / REJECTED** | v1 client requests are deterministically rejected with `4400 protocol_rejected`. |
| WebRTC Opus Bridge | **DEAD CODE / PURGED** | Purged in Phase 0 reset; 0 occurrences in codebase. |

---

### 5. Provisioning & Onboarding

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| Zero-Typing Owner Approval | **PRODUCTION** | Proximity-confirmed BLE onboarding with owner web approval (#250, #252). |
| Manual 6-Digit Claim Code | **TEST ONLY / FALLBACK** | Kept as secondary manual fallback if Bluetooth LE is unavailable on client. |
| Build-time Hardcoded Secrets | **DEAD CODE / PURGED** | Checked by `scripts/check_single_path.py`. Secrets only in NVS. |

---

### 6. Owner Experience & Display

| Component / Path | Classification | Rationale & Evidence |
| :--- | :--- | :--- |
| Owner Hub (`backend/internal/ownerweb/`) | **PRODUCTION** | Zero-framework HTML5/JS responsive web app for settings, twin sync, and voice memo playback. |
| SSD1306 Display Controller | **PRODUCTION** | Direct I2C 128x64 display rendering with priority view reduction. |

---

## Deletion and Retention Verdict

1. **Retain `backend/internal/store/` as TEST ONLY:** Deleting SQLite entirely would break fast isolated unit testing across 12 packages or force all unit tests to require a live PostgreSQL daemon. Keeping it scoped to unit tests complies with AGENTS.md.
2. **Retain `companion-migrate` & `companion-river-migrate` as TOOLING ONLY:** Required for DB deployment.
3. **No Dead Code Found:** All historical WebRTC, dual-write, and obsolete runtime selector code has already been purged and is permanently blocked by `check_single_path.py`.

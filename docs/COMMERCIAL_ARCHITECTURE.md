# Commercial architecture — production-shaped POC

This document is the architecture contract for evolving the current single-node POC into a commercial multi-user/multi-device product without rewriting the domain core.

## 1. Three planes

```text
ESP32 fleet
   |
   +---- DATA PLANE -------- WebSocket/WebRTC Opus gateway -> ASR -> Semantic Router -> LLM -> MCP Bridge -> TTS/UI
   |
   +---- CONTROL PLANE ----- device identity/twin, config, feature catalog/flags, entitlements, privacy, OTA
                                  |
Domain state (SQLite POC / Postgres prod) -- transactional outbox -- EVENT PLANE -> alerts/integrations/audit/workers
```

The POC intentionally remains a modular Go monolith. Process boundaries may be split later; Go package/port boundaries are already the migration seams.

## 2. Configuration is not feature flags, authorization or entitlement

The four concerns remain separate:

- **Remote config:** typed parameters such as VAD thresholds, locale, timezone and voice key.
- **Feature flag:** rollout/kill-switch/experiment decision.
- **Entitlement:** product/plan right owned by the user/tenant.
- **Policy:** whether a specific actor may execute a specific tool/action now.

Config resolution order is: built-in defaults -> global -> tenant -> plan -> user -> device. Device-specific desired state remains the final override. Every resolved device snapshot carries a globally monotonic `config_version`; ESP32 reports the version it actually applied. Stale device reports/config pushes never advance state.

## 3. Device twin

`desired` and `reported` config are stored independently. The device keeps last-known-good local values, rejects invalid/stale versions and reports applied/rejected state. Offline devices resolve the newest snapshot on reconnect. This follows the desired/reported/version pattern used by commercial IoT device shadows/twins without depending on an AWS/Azure runtime.

## 4. Feature lifecycle

The control plane includes a versioned Feature Catalog plus flags:

```text
draft -> internal -> beta -> released -> deprecated -> removed
```

A FeatureModule manifest describes tools, resources, UI cards, config keys, locales, device capabilities, minimum protocol and implementation adapter. It is metadata only: manifests never load arbitrary executable code into the Go process. Native capability code is deployed normally; optional external capability execution must cross the **Official MCP Go SDK Bridge** boundary with strict SSRF-safe endpoint defaults.

### 4.1. Evidence-Based Truth Gate
Code deployment is strictly governed by `evidence/status.json`. The architecture intentionally separates **implemented code** from **production-proven evidence**. Mock/fake evidence cannot promote code to a `PASS` state. It remains `UNPROVEN` until real-provider/network/HIL evidence exists.

## 5. LLM runtime

The LLM is a reasoning/composition component, never the authoritative database.

```text
ASR
 -> deterministic ContextRouter / Semantic Router
 -> authoritative domain resources
    + current temporal memory
    + hybrid semantic/lexical memory
    + live external data
 -> selected model
 -> native structured/parallel tool calls
 -> host JSON-schema validation + policy
 -> MCP Bridge / ToolRegistry
 -> authoritative mutation
 -> early UI
 -> final verbalization/TTS
```

Fast/reasoning model selection is replaceable. Every model request can record model, prompt version and token usage; an optional monthly quota guard blocks runaway spend. Real-model promotion must pass the versioned eval corpus plus provider-side quality/latency/cost tests.

### 5.1. HITL Governance Gates & Destructive Auth
While the LLM is restricted by JSON-schema validation, **High-Risk Mutations** (e.g., identity rotation, payments, memory purges) must implement a strict Governance Gate. Destructive authorization is based on **owner + exact tool + canonical args hash + expiry confirmation scope**, not just utterance substrings. The LLM may draft the execution plan, but the ToolRegistry MUST pause execution and await explicit human cryptographic approval before committing the mutation.

## 6. Memory

Memory has two layers:

- Authoritative temporal facts with `valid_from`, `valid_to`, source and confidence.
- Replaceable vector index used only as a secondary semantic projection.

Superseded facts remain temporal history but are not returned as current facts. A vector index can be rebuilt from the authoritative memory store. Expenses, budgets, schedules and other domain state never move into vector memory.

## 7. Live market data

Live market data is external context, not memory. Normalized quotes carry source and `as_of`; retail bid/ask quotes also carry `bid`, `ask`, `price_type` and unit. Current adapters cover CoinGecko, Twelve Data, Alpha Vantage gold, and an opt-in PNJ retail-gold adapter. Provider licensing/redistribution rights are a commercial deployment requirement.

Price watches are deterministic workers. Threshold transition + reminder creation is atomic in SQLite so retries/concurrent workers do not double-notify a crossing. No LLM polling is used.

## 8. Events

The source of truth is traditional transactional state. The POC deliberately does **not** use full Event Sourcing. State mutations create durable outbox events in the same database transaction. Consumers must be idempotent because at-least-once retries are possible.

This gives audit/integration/retry hooks without forcing every query or startup to replay an event stream.

## 9. OTA

Firmware metadata is separate from firmware bytes. The registry enforces board/channel/protocol/security-version compatibility, SHA-256, monotonically versioned metadata and expiry. Ed25519 manifest verification can be required by configuration. The contract is TUF-inspired; it is **not** claimed as a complete TUF/Uptane repository implementation.

On-device ESP-IDF binary download, image verification, pending-verify self-test, rollback, Secure Boot v2, flash encryption/eFuse security version and physical HIL are separate release gates.

### 9.1. Tiered CI/CD Release Pipeline
To safely deploy AI-assisted firmware, the OTA pipeline strictly follows a tiered validation model:
*   **Tier 1 (Virtual):** Fast feedback via static analysis, MISRA compliance, and QEMU/Wokwi emulation.
*   **Tier 2 (Physical):** Hardware-in-the-Loop (HIL) via Mac M1 self-hosted runners using `pytest-embedded`.
Firmware NEVER reaches the OTA registry without passing both tiers.

## 10. Voice and language

Locale uses BCP-47-style tags (`vi-VN`, `en-US`), timezone is IANA (`Asia/Ho_Chi_Minh`), and users select a stable logical `voice_key`, not a provider-specific voice ID. Locale/timezone/voice are carried in `TurnContext`, so ASR/LLM/TTS adapters may resolve them per turn. The Qwen system instruction responds in the preferred locale rather than being permanently Vietnamese.

The current deterministic ASR/TTS remain POC adapters; production streaming ASR/TTS and provider voice catalogs are still provider-dependent gates.

## 11. Privacy

Per-user privacy policy separates `save_voice_audio`, long-term-memory enablement, and conversation/voice/memory retention periods. Long-term-memory tools are denied when the user disables memory. A deterministic retention worker prunes expired database rows; voice files returned by retention cleanup are removed by the worker.

Speaker identification remains opt-in/future work and must never silently identify guests.

## 12. Security / identity

The server supports per-device credentials with only SHA-256 token hashes stored, constant-time comparison, revoke/rotate enrollment and credential-backed ownership. Enrollment also binds trusted `user_id`, `tenant_id` and `plan` claims; database-authenticated sessions use those claims for config/feature resolution instead of trusting transport headers. Legacy global-token/header identity remains for bench compatibility only. Commercial hardware must move credential material into ESP32 secure provisioning/storage and enable TLS verification, Secure Boot and flash encryption.

### 12.1. AI Security Debt & Pipeline Hardening
Due to the speed of AI-generated code, the CI/CD pipeline acts as the primary security perimeter:
*   **Automated Security Gates:** All AI-generated PRs are automatically scanned for Common Weakness Enumerations (CWEs) (e.g., injection flaws) and Supply Chain attacks (hallucinated dependencies).
*   **Code Lineage:** AI-authored code is tracked distinctly in version control, triggering stricter automated RBAC and signing policies before merging into `main`.

## 13. Commercial Production (CP) Milestone Roadmap

The evolution from POC to a commercial production system strictly follows this sequential milestone roadmap. Work must not skip ahead of unproven dependencies.

1. **CP-AUDIT0 (The Foundation):** Evidence model + README truth + production profile (`evidence/status.json`).
2. **CP-VOICE1 (Speech Layer):** Real ASR/TTS provider boundaries.
3. **CP-AI1 (Brain Layer):** Prompt bundles + real LLM eval.
4. **CP-SAFE1 (Governance):** Confirmation tokens + hard idempotency (Destructive Auth).
5. **CP-DATA1 (Persistence):** Postgres + Atlas + Ent + River (replaces SQLite).
6. **CP-RT1 (Concurrency):** Supervised concurrency + protocol v2.
7. **CP-MCP1 (Integration):** Device/external MCP.
8. **CP-OBS1 (Telemetry):** OpenTelemetry + SLO/fuzz/load/fault injection.
9. **CP-FW (Hardware):** AEC/VAD/wake word + secure device.
10. **CP-RC (Release Candidate):** Real-device full system + soak testing.

## 14. Explicit non-goals for the current verified POC

Do not mark these production-ready until their real CP milestone passes. The architecture separates implemented code from production-proven evidence. Fake/mock evidence cannot promote a feature to `PASS`.

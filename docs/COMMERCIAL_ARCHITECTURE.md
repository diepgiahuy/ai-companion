# Commercial architecture — production-shaped POC

This document is the architecture contract for evolving the current single-node POC into a commercial multi-user/multi-device product without rewriting the domain core.

## 1. Three planes

```text
ESP32 fleet
   |
   +---- DATA PLANE -------- WebSocket gateway -> ASR -> Context Compiler -> LLM -> ToolRegistry -> TTS/UI
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

A FeatureModule manifest describes tools, resources, UI cards, config keys, locales, device capabilities, minimum protocol and implementation adapter. It is metadata only: manifests never load arbitrary executable code into the Go process. Native capability code is deployed normally; optional external capability execution must cross an allow-listed adapter/MCP boundary with policy checks.

## 5. LLM runtime

The LLM is a reasoning/composition component, never the authoritative database.

```text
ASR
 -> deterministic ContextRouter
 -> authoritative domain resources
    + current temporal memory
    + hybrid semantic/lexical memory
    + live external data
 -> selected model
 -> native structured/parallel tool calls
 -> host JSON-schema validation + policy
 -> ToolRegistry
 -> authoritative mutation
 -> early UI
 -> final verbalization/TTS
```

Fast/reasoning model selection is replaceable. Every model request can record model, prompt version and token usage; an optional monthly quota guard blocks runaway spend. Real-model promotion must pass the versioned eval corpus plus provider-side quality/latency/cost tests.

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

## 10. Voice and language

Locale uses BCP-47-style tags (`vi-VN`, `en-US`), timezone is IANA (`Asia/Ho_Chi_Minh`), and users select a stable logical `voice_key`, not a provider-specific voice ID. Locale/timezone/voice are carried in `TurnContext`, so ASR/LLM/TTS adapters may resolve them per turn. The Qwen system instruction responds in the preferred locale rather than being permanently Vietnamese.

The current deterministic ASR/TTS remain POC adapters; production streaming ASR/TTS and provider voice catalogs are still provider-dependent gates.

## 11. Privacy

Per-user privacy policy separates `save_voice_audio`, long-term-memory enablement, and conversation/voice/memory retention periods. Long-term-memory tools are denied when the user disables memory. A deterministic retention worker prunes expired database rows; voice files returned by retention cleanup are removed by the worker.

Speaker identification remains opt-in/future work and must never silently identify guests.

## 12. Security / identity

The server supports per-device credentials with only SHA-256 token hashes stored, constant-time comparison, revoke/rotate enrollment and credential-backed ownership. Enrollment also binds trusted `user_id`, `tenant_id` and `plan` claims; database-authenticated sessions use those claims for config/feature resolution instead of trusting transport headers. Legacy global-token/header identity remains for bench compatibility only. Commercial hardware must move credential material into ESP32 secure provisioning/storage and enable TLS verification, Secure Boot and flash encryption.

## 13. POC -> production replacements

| POC | Production replacement | Core rewrite? |
|---|---|---:|
| SQLite | Postgres | No — repository adapters |
| in-process vector scan | pgvector/Qdrant | No — VectorStore |
| memory conversation cache | Redis | No — Cache |
| local flags | OpenFeature provider/flagd/Unleash/LaunchDarkly | No — FeatureProvider |
| single Go process | gateway/agent/worker/control-plane processes | No — package ports become RPC boundaries |
| local files | object storage | No — recording/firmware storage adapter |
| JSON logs | OpenTelemetry exporter | No — telemetry boundary |

## 14. Explicit non-goals for the current verified POC

Do not mark these production-ready until their real gate passes: ESP-SR AEC/WakeNet, full hands-free barge-in, physical I2S/OLED validation, firmware OTA downloader/rollback HIL, Secure Boot/eFuse provisioning, production streaming ASR/TTS, production vector service, external MCP transport, speaker identification, billing/payment integration, and commercial data-provider contracts.

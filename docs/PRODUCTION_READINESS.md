# Production readiness matrix

Legend: ✅ software path implemented/tested · 🟡 production-shaped boundary implemented but external/HIL gate remains · 🔴 not implemented.

| Area | Status | Verified boundary |
|---|---:|---|
| Domain source of truth | ✅ | SQLite transactions + typed repositories; domain state not reconstructed from chat/vector memory. |
| Transactional outbox | ✅ | SQLite triggers/rows are atomic with state; retry/recovery worker. |
| Device twin/config | ✅ | desired/reported, last-known-good client behavior, global monotonic snapshot version, config hierarchy. |
| Feature catalog/flags | ✅ | versioned catalog, lifecycle, deterministic rollout, admin APIs. |
| Entitlement boundary | ✅ | separate durable user entitlement store/admin API; not used as authentication. |
| Tool policy | ✅ | feature/privacy/entitlement hooks, destructive-intent check, host schema validation. |
| Device credentials | 🟡 | per-device server credentials/revoke/constant-time auth plus trusted enrolled user/tenant/plan claims implemented; secure hardware provisioning/TLS client identity pending. |
| Temporal memory | ✅ | supersede/current validity/provenance/confidence. |
| Vector retrieval | 🟡 | secondary VectorStore + rebuildable SQLite projection/OpenAI-compatible embedding adapter; production vector engine pending. |
| LLM tool calling | ✅ | progressive packs, native structured tool calls, host validation, parallel-tool request support, fake-model E2E. |
| Real-model eval | 🟡 | versioned routing corpus exists; live Qwen A/B quality/latency/cost run requires model endpoint. |
| Usage/cost | ✅ | token metering + optional monthly guard. Dynamic billing-plan quota lookup can replace fixed env limit. |
| Live market | 🟡 | real HTTP adapters/cache/provenance/watches implemented; commercial provider licenses and Internet runtime remain external. |
| Market alert idempotency | ✅ | atomic false->true threshold transition + reminder creation. |
| Privacy/retention | ✅ | per-user policy, memory opt-out, voice-audio pre-write denial, retention worker. Regulatory review remains deployment-specific. |
| Voice locale/voice config | 🟡 | locale/timezone/voice key propagated per turn; production ASR/TTS catalogs/providers pending. |
| OTA server/control plane | 🟡 | signed/versioned/expiring compatible manifest registry + device/admin endpoints; on-device download/rollback/HIL pending. |
| Backpressure | ✅ | bounded separate control/audio queues; control traffic prioritized. |
| Observability | 🟡 | per-turn stage latency + usage logs; OpenTelemetry exporter not yet wired. |
| Exact Go 1.26.5 release CI | 🟡 | workflow/devcontainer added; this sandbox cannot execute Docker/GitHub-hosted run. |
| ESP-IDF target/HIL | 🔴 ⚠️ | no ESP-IDF toolchain/physical board in this sandbox. |

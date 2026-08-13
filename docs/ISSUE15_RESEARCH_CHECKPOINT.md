# Issue #15 research checkpoint — 2026-08-13

This checkpoint records the research and decisions used to finish the single-path cleanup on top of the merged protocol-v2 and evidence-ladder baseline. The first section preserves the **pre-migration findings**; the completion section records what the replacement branch implemented.

## Pre-migration verified repository state

- Protocol v2 and the Tier-1 software-device evidence ladder were already merged on `main`.
- The speculative WebRTC bridge, secondary offline Go module/shims, and `e2e-offline` target were already absent from `main`; they must not be reintroduced or re-ported from stale PR history.
- Firmware still had a product-selectable `MockVoiceBackend` through `CONFIG_COMPANION_USE_WEBSOCKET`.
- Backend product composition still selected `legacy|adk|mock`, defaulted to `legacy`, and could fall back to `MockAgent` when the legacy model endpoint was absent.
- The ADK bridge exposed only four representative tools and created an in-memory ADK runner/session service.
- Device auth still had a legacy shared-token path beside database-backed enrolled device credentials.

## External cross-checks

- Google ADK Go v2 exposes session storage as a replaceable service boundary. Companion durable conversation/domain state remains authoritative; ADK in-memory session state must not become product durability.
- ADK FunctionTool supports explicit input JSON Schema. Companion ToolRegistry remains the authoritative validation/authorization/execution boundary; ADK tools are adapters, not duplicated domain implementations.
- The ADK OpenAI-compatible model adapter uses the Responses API, so deterministic Tier-1 fixtures must exercise Responses streaming/function-call semantics rather than the legacy Chat Completions fixture.
- GitHub Actions secrets are only available when explicitly injected. HIL/provider workflows must remain trusted/manual and must not restore a shared device token as a product fallback.

## Migration order used

1. Hard-cut firmware mock product backend.
2. Expand ADK adapter to the complete current ToolRegistry without bypassing ToolRegistry policy/schema/idempotency/presentation behavior.
3. Keep Companion conversation storage authoritative and treat ADK in-memory sessions as a rehydratable execution cache rather than a second product database.
4. Make ADK the only product agent runtime; remove `COMPANION_AGENT_RUNTIME`, custom Qwen composition, semantic legacy router and production mock-agent fallback.
5. Make database-enrolled per-device credentials the sole product device-auth path; migrate software-device tests to explicit enrollment, wrong-credential and revocation checks.
6. Replace the Chat Completions fake model with a deterministic Responses API fixture and run Tier-1 parity through the real ADK bridge.
7. Delete the legacy code/fixtures/config rather than retaining rollback selectors; rollback remains Git/data restore.

## Implemented result on PR #34

- Firmware product composition is Wi-Fi + WebSocket protocol v2 only. Test doubles remain test-only.
- Google ADK v2 is the sole product agent runtime. Missing `ADK_OPENAI_BASE_URL`/`ADK_MODEL` fails startup.
- Every public `ToolRegistry.Definitions()` entry is exposed generically to ADK; hidden/internal tools are not exported.
- Companion conversation storage persists user/assistant history and rehydrates recreated ADK sessions. Domain state remains authoritative in repositories.
- The custom Qwen runtime, model selector/semantic router and legacy Chat Completions fixture were deleted.
- `/v2/device` requires a database-enrolled per-device credential and fails closed without a valid authenticator/credential. Tier-1 covers wrong credentials and revoked credentials.
- Tier-1 uses the production C++ `CompanionApp`, real Go backend, real ADK Responses adapter and real SQLite state with deterministic ASR/TTS/model fixtures. Evidence remains `orchestration_only`; it does not promote provider or physical gates.
- Representative parity is being gated for expense, budget, note, journal, reminder, timer and memory, with barge-in/cancellation and tool-loop coverage in the same Tier-1 harness.

## Non-goals

- No ADK version upgrade in this cleanup.
- No new database source of truth or ADK-owned parallel product database.
- No provider-quality, physical-HIL, Wokwi, or real-audio promotion from hosted tests.
- No compatibility selectors retained for rollback; rollback is Git/data restore.

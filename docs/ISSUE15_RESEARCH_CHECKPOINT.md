# Issue #15 research checkpoint — 2026-08-13

This checkpoint records the decisions used to finish the single-path cleanup on top of the merged protocol-v2 and evidence-ladder baseline.

## Verified repository state

- Protocol v2 and the Tier-1 software-device evidence ladder are merged on `main`.
- The speculative WebRTC bridge, secondary offline Go module/shims, and `e2e-offline` target are already absent from current `main`; they must not be reintroduced or re-ported from stale PR history.
- Firmware still had a product-selectable `MockVoiceBackend` through `CONFIG_COMPANION_USE_WEBSOCKET`; this cleanup hard-cuts the product composition to Wi-Fi + WebSocket v2. Test-only mocks remain valid.
- Backend product composition still selects `legacy|adk|mock`, defaults to `legacy`, and may fall back to `MockAgent` when the legacy model endpoint is absent.
- The ADK bridge currently exposes only four representative tools and creates an in-memory ADK runner/session service.
- Device auth still has a legacy shared-token path beside database-backed enrolled device credentials.

## External cross-checks

- Google ADK Go v2 exposes `session.Service` as a replaceable storage boundary. Companion durable conversation/session state remains authoritative; ADK in-memory session state must not become product durability.
- ADK FunctionTool supports explicit input JSON Schema. Companion ToolRegistry remains the authoritative validation/authorization/execution boundary; ADK tools are adapters, not duplicated domain implementations.
- GitHub Actions secrets are only available when explicitly injected. HIL/provider workflows must remain trusted/manual and must not restore a shared device token as a product fallback.

## Migration order

1. Hard-cut firmware mock product backend.
2. Expand ADK adapter to the complete current ToolRegistry without bypassing ToolRegistry policy/schema/idempotency/presentation behavior.
3. Replace hard-coded ADK in-memory session ownership with a Companion-owned session adapter or an explicitly non-authoritative invocation/session layer proven not to hold durable product state.
4. Make ADK the only product agent runtime; remove `COMPANION_AGENT_RUNTIME`, custom Qwen composition, and production mock-agent fallback.
5. Make database-enrolled per-device credentials the sole product device-auth path; migrate software-device/HIL test fixtures to explicit enrolled credentials.
6. Run Tier-1 parity, auth reconnect/revocation/failure tests, exact Go/ESP-IDF CI, CodeQL, evidence truth, and static duplicate-path scan before merge.

## Non-goals

- No ADK version upgrade in this cleanup.
- No new database source of truth or ADK-owned parallel product database.
- No provider-quality, physical-HIL, Wokwi, or real-audio promotion from hosted tests.
- No compatibility selectors retained for rollback; rollback is Git/data restore.

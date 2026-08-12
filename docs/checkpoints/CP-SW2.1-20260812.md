# CP-SW2.1 — Reversible Google ADK v2 integration seam

Date: 2026-08-12
Status: PARTIAL — source/offline regression passed; exact production ADK compile/dependency-lock gate blocked by the current sandbox

## Goal

Introduce Google ADK v2 without a big-bang rewrite: keep the current runtime as a rollback path, preserve Companion-owned product/safety boundaries, and make the new path independently testable before it can become default.

## Changed

- Added `backend/internal/adkbridge` as the sole ADK anti-corruption boundary.
- Added build-tag split:
  - without `adk`: `New` returns `ErrNotBuilt` (fail closed);
  - with `adk`: official ADK OpenAI/Responses model + `llmagent` + runner are wired.
- Added explicit `COMPANION_AGENT_RUNTIME=legacy|adk|mock`; default remains `legacy`.
- Added typed representative FunctionTools for:
  - `expense.log`
  - `budget.get`
  - `timer.create`
  - `memory.recall`
- Tool wrappers reuse the existing registry's JSON schemas and execute through `ToolRegistry`, retaining validation, authorization, feature/privacy policy, presentation emission and product logic.
- Idempotency keys now include ADK `FunctionCallID` plus Companion turn/session identity through canonical SHA-256 tuple hashing.
- ADK SSE text events feed the provider-neutral `StreamingAgent`; partial chunks are forwarded according to ADK `Partial` semantics and final snapshots are deduplicated.
- Preserved destructive-intent policy on the ADK path.
- Added ADK model wrapper for existing monthly quota checks and persisted token-usage metering.
- Added production dependency pins in `backend/go.mod`:
  - `google.golang.org/adk/v2 v2.2.0`
  - `github.com/google/jsonschema-go v0.4.3`
  - `google.golang.org/genai v1.66.0`
- Production E2E now compiles the `adk` path; `make backend-adk-gate` requires exactly Go 1.26.5.
- Updated backend runtime/environment documentation.
- Added `scripts/checkpoint_snapshot.sh` so future checkpoints atomically require a clean tagged HEAD, generate a source ZIP + full Git bundle + SHA256 files + manifest, and verify both artifacts before handoff.

## Tests executed

Passed in this environment:

```text
GOTOOLCHAIN=local go test -race -count=1 -modfile=go.offline.mod ./internal/adkbridge ./internal/capability ./internal/policy ./cmd/companiond
GOTOOLCHAIN=local go vet -modfile=go.offline.mod ./...
bash scripts/e2e_offline.sh
git diff --check
ADK boundary grep (no ADK imports outside internal/adkbridge)
```

Production gate was executed and correctly remained blocked rather than being
misreported as green:

```text
GOTOOLCHAIN=local make backend-adk-gate
ADK gate requires go1.26.5; got go1.23.2
```

Coverage added:

- ADK-disabled build fails closed.
- FunctionTool bridge delegates to authoritative host registry.
- Registry JSON-schema validation still rejects bad arguments before handler execution.
- Registry authorization still rejects denied tools before handler execution.
- idempotency is deterministic, reconnect-safe and delimiter-collision-safe.
- representative rollout list cannot be mutated by callers.
- repeated streaming suffix chunks are not dropped.
- a later full final response does not duplicate already streamed speech.
- tagged production tests instantiate the ADK agent with a fake `model.LLM` and test usage quota/meter behavior; these tests are present but cannot be executed in this sandbox until the production dependency graph is available.

## Measured

From the final offline E2E gate for this checkpoint:

- host C++ tests: `2/2 PASS`;
- backend offline functional/race suite: PASS;
- partition end: `0xFF0000 / 0x1000000` (99.6% address-space layout used);
- OTA slots: 4 MiB each;
- SRAM design cap: 160.5 KiB;
- PSRAM codec reserve: 128 KiB.

No ADK latency/CPU/memory number is claimed yet because the tagged runtime could not be built here.

## Problems found

1. The previous Git bundle ended at CP4.1 even though a newer CP-SW1 ZIP existed.
2. The first stream normalizer used content heuristics and could drop a legitimate repeated delta chunk.
3. Delimiter-joined idempotency/session keys admitted theoretical tuple collisions.
4. A naive ADK integration would have bypassed the legacy runtime's quota/meter and destructive-intent context.
5. This sandbox has Go 1.23.2 and outbound DNS is blocked; Go cannot auto-fetch toolchain 1.26.5 or ADK modules.

## Root cause

- Checkpoint artifacts had been created independently instead of updating one authoritative Git history.
- Streaming logic was guessing whether text was cumulative vs delta instead of consuming ADK's explicit `Partial` contract.
- Human-readable delimiter keys were favored over canonical tuple encoding.
- Cross-cutting policy/cost controls lived around the legacy model adapter and therefore had to be deliberately reintroduced at the new framework boundary.
- Environment restriction is external to the repository.

## Solution

- Imported exact CP-SW1 tree into Git and tagged `CP-SW1-20260812` before starting this work.
- Automated checkpoint artifact creation/verification so ZIP state and Git history cannot silently drift again.
- Use ADK event semantics; only deduplicate the non-partial final snapshot.
- Hash canonical JSON tuples for bounded collision-resistant keys.
- Keep tools and quota/policy enforcement Companion-owned; ADK orchestrates but does not become the source of product authority.
- Separate the offline regression gate from the exact production dependency/compile gate and never mark the latter PASS without evidence.

## Trade-offs accepted

- ADK is opt-in and behind a build tag, adding two build modes during migration; this is deliberate rollback insurance.
- Only four representative tools are exposed in CP-SW2.1; full tool parity is deferred until measured in CP-SW4.
- ADK's in-memory session runner is temporary and cannot be promoted to the default production runtime.
- The official ADK OpenAI adapter needs the Responses API. A Chat-Completions-only local server remains on the legacy path.
- If a provider's final text materially rewrites already streamed partial text, the voice adapter suppresses that conflicting final snapshot because already-spoken audio cannot be safely rewritten; provider parity tests must surface this behavior.

## Rollback

After commit/tag creation:

```text
git checkout CP-SW2.1-20260812   # exact checkpoint
git checkout CP-SW1-20260812     # previous known-good software checkpoint
```

Runtime-only rollback does not require a code revert:

```text
COMPANION_AGENT_RUNTIME=legacy
```

A ZIP + SHA256 and a Git bundle containing all checkpoint tags are saved with this checkpoint.

## Next

Finish CP-SW2 in an exact Go 1.26.5 environment with dependency access:

1. `go mod tidy` + `go mod verify`; commit the resulting `go.sum`.
2. `make backend-adk-gate`.
3. Run a local OpenAI Responses-compatible endpoint contract test.
4. Add tagged streaming cancellation/tool-call parity E2E.
5. Keep legacy default until those gates are green; durable ADK sessions/full tool migration remain CP-SW4.

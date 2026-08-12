# CP-SW2.2 — ADK tool-loop/error-semantics hardening

Date: 2026-08-12
Status: PARTIAL — hardening and offline regression pass; CP-SW2 exact Go 1.26.5 + tagged ADK compile/provider contract gate remains blocked by this sandbox

## Goal

Harden the reversible ADK seam after an external review highlighted tool-only turns, malformed host-tool JSON, synchronous usage metering, and streamed-text deduplication. Apply fixes only where they preserve stronger production invariants; do not adopt speculative optimizations that weaken accounting or lifecycle safety.

## Changed

- Added a Companion-owned invocation outcome tracker that correlates ADK function-call IDs with host-tool outcomes without leaking ADK types outside `internal/adkbridge`.
- Voice completion now distinguishes:
  - normal final speakable text;
  - successful mutation with no post-tool model text;
  - read/failure/malformed tool result with no final text;
  - function call with no host result;
  - completely silent invocation.
- A deterministic `OK.` fallback is emitted only when every unacknowledged tool result is a successfully committed `write`/`destructive` mutation. This prevents the user from repeating a mutation that already committed while refusing to hide read/failure states.
- Pre-tool model speech no longer counts as acknowledgement of a side effect that happens later.
- Tool-call IDs are deduplicated so streamed/duplicate function-call events cannot overcount execution state.
- `HostToolExecutor` now converts malformed/non-object/missing-`ok: bool` host results into a safe structured tool response:
  - `ok=false`
  - `error_code=invalid_tool_result`
  - `execution_status=unknown`
  - `retryable=false`
- Raw malformed host output is never copied into the LLM-visible error payload.
- `ToolRegistry.Execute` now contains panics from tool/authorization execution and returns a generic structured failure instead of allowing a tool panic to tear down the process/session or leak the panic payload.
- Kept synchronous usage metering unchanged. The proposed fire-and-forget goroutine optimization was rejected because there is no measured latency evidence yet and it would weaken shutdown/accounting/quota guarantees without a bounded durable queue.
- Added `docs/STATIC_REVIEW_GATE.md` and made independent static review a mandatory checkpoint gate.
- `scripts/checkpoint_snapshot.sh` now refuses to snapshot a tag unless both the checkpoint note and `docs/reviews/<tag>-static-review.md` record a passing independent static review.
- Updated the README checkpoint template so future checkpoint notes record review dimensions and post-review retest evidence.
- Fixed a realtime wire-ordering bug discovered by the independent review: the priority control queue could emit `tts sentence_end`/`tts stop` before audio frames that were already accepted into the FIFO audio queue.
- Added an ordered turn-scoped media lane for TTS lifecycle events and audio frames. Causally-related `tts start/sentence_start/audio/sentence_end/stop` items share one FIFO lane; lifecycle events wait only for bounded lane capacity, while urgent abort/config/control retains its separate priority lane.
- Non-streaming TTS/UI control writes in the touched path now check enqueue failures instead of silently continuing with an inconsistent client state.

## Tests executed

Post-review gates run in the available sandbox:

```text
GOTOOLCHAIN=local go test -race -count=1 -modfile=go.offline.mod ./...
GOTOOLCHAIN=local go vet -modfile=go.offline.mod ./...
bash scripts/e2e_offline.sh
bash -n scripts/checkpoint_snapshot.sh
git diff --check
ADK import-boundary grep
```

Results:

- backend offline race suite: PASS;
- backend offline `go vet`: PASS;
- host C++ tests: `2/2 PASS`;
- offline functional E2E: PASS;
- ADK boundary: PASS — no ADK Go imports outside `internal/adkbridge`;
- snapshot script syntax: PASS;
- diff whitespace check: PASS.
- ADK host/outcome/capability focused suite: PASS in 20 consecutive `-race` repetitions;
- final static review report: PASS — no unresolved release-blocking correctness/security finding.
- previously flaky expense/budget websocket E2E: PASS in 50 consecutive final `-race` repetitions after the ordered-media-lane fix;
- focused media-ordering/backpressure + streaming overlap tests: PASS in 30 consecutive `-race` repetitions, including a transiently-full media lane.

The first post-review full race run intentionally **did not close the checkpoint**: it exposed an intermittent `frames=1` result before `tts stop`. Repetition reproduced the failure on the pre-ordering-fix code, root cause was cross-lane priority ordering without a media causality contract, and the gate was rerun only after the fix.

Production ADK gate was rerun and remains correctly blocked rather than falsely marked green:

```text
GOTOOLCHAIN=local make backend-adk-gate
ADK gate requires go1.26.5; got go1.23.2
```

## Independent static review

Static review status: PASS

Review method: inspect the complete implementation diff after the first implementation pass, treat it as third-party code, then rerun focused and full tests after every accepted fix.

### Findings

| ID | Severity | Finding | Disposition |
|---|---|---|---|
| SR-01 | HIGH | `sentText` could treat speech emitted before a tool as successful final acknowledgement after the tool. | FIXED — track speakable text after host-tool result sequence. |
| SR-02 | HIGH | A successful mutation followed by a failed/read tool could be incorrectly masked by a generic mutation fallback. | FIXED — any unacknowledged non-mutation/failure/malformed result blocks fallback. |
| SR-03 | HIGH | Malformed host result returned as a Go error would break the agent continuation path, while echoing raw content to the model would leak internals and create prompt-injection surface. | FIXED — safe structured `invalid_tool_result`, no raw content, retry disabled because side-effect status may be ambiguous. |
| SR-04 | MEDIUM | JSON that parsed but lacked a boolean `ok` envelope could be treated as a valid tool response. | FIXED — require `ok: bool`; otherwise convert to invalid result. |
| SR-05 | HIGH | A tool panic occurs before JSON decoding and could escape the registry boundary. | FIXED centrally in `ToolRegistry.Execute`; generic failure does not expose panic value. |
| SR-06 | MEDIUM | Streamed/duplicate function-call events could overcount calls and cause false incomplete-result decisions. | FIXED — correlate/dedupe by ADK function-call ID; anonymous calls are only recorded on non-partial events. |
| SR-09 | HIGH | Priority control writes could overtake previously queued audio, so `tts sentence_end`/`tts stop` could reach the device before the final audio frame. | FIXED — ordered turn-scoped media lane for TTS lifecycle events and audio frames; reproduced flaky E2E before fix and stress-tested after fix. |
| SR-10 | MEDIUM | The first barrier-based fix would both serialize streaming at sentence boundaries and could falsely fail when the media queue was transiently full after audio had already been accepted. | FIXED — replaced the barrier with ordered media-lane enqueue; causally-required lifecycle events use bounded capacity wait while ordinary audio production remains non-blocking/bounded. |
| SR-11 | HIGH | Non-streaming TTS/UI control enqueue errors in the touched path were ignored, allowing audio/client state to diverge under control-queue backpressure. | FIXED — check errors, fail the turn, and stop producing further state/audio. |
| SR-12 | MEDIUM | Streaming terminal media enqueue could return `context.Canceled` after the last `isCurrent` check but be treated as success, producing a false completed-turn log/state. | FIXED — propagate cancellation to the outer turn handler; canceled turns are not logged as completed. |
| SR-13 | HIGH | ADK host bridge published tool presentation before validating the result envelope, so malformed/failed output could show success UI. | FIXED — validate `ok: bool` first and publish presentation only for valid `ok=true` results; regression test verifies malformed presentation suppression. |
| SR-07 | MEDIUM | `go meter.RecordUsage(...)` per invocation would create unbounded fire-and-forget lifecycle and possible usage/quota loss at shutdown without a bounded durable handoff. | ACCEPTED AS NO-CHANGE — retain synchronous metering until tracing/benchmark proves a latency problem; if changed later, use bounded queue + drain/backpressure/accounting semantics. Owner: CP-SW8 observability/performance hardening. |
| SR-08 | LOW | Generic `OK.` fallback is intentionally language-neutral but less expressive than a localized model response. | ACCEPTED — only used for the narrow committed-mutation/no-final-text failure mode; model-generated post-tool text remains preferred. Revisit with locale-aware host acknowledgement only if measurements/user testing justify it. |

### Review dimensions

- Correctness/state machine: PASS after SR-01/SR-02/SR-04/SR-06/SR-09/SR-11/SR-12/SR-13 fixes.
- Concurrency/cancellation/lifecycle: PASS for modified code; outcome tracker is mutex-protected, fallback is suppressed after context cancellation, and TTS media is turn-scoped and FIFO on one lane; stale generations are still dropped by the single writer, and lifecycle enqueue waits are timeout-bounded.
- Error/retry/idempotency: PASS for current scope; ambiguous malformed side effects are non-retryable and existing host idempotency remains authoritative.
- Security/privacy/trust: PASS for current scope; raw malformed output and panic payloads are not exposed to the LLM.
- Performance/resource bounds: PASS; no new long-lived goroutine or unbounded queue was introduced. The ordered media lane reuses the existing bounded queue and adds no per-sentence acknowledgement goroutine/channel; lifecycle enqueue waits have a 5s bound.
- Architecture/maintainability: PASS; ADK types remain isolated and registry/domain semantics are not duplicated.
- Tests rerun after review: PASS — expense websocket E2E 50/50 under `-race`; media/stream ordering suite 30/30 under `-race`; full backend race/vet and offline host/backend E2E all green.

## Measured

From the final offline E2E gate:

- host tests: `2/2 PASS`;
- partition end: `0xFF0000 / 0x1000000` (99.6% address-space layout used);
- OTA slots: 4 MiB each;
- SRAM design cap: 160.5 KiB;
- PSRAM codec reserve: 128 KiB.

No claim is made that usage metering is a voice-path bottleneck; no latency optimization is accepted without measurement.

## Problems found / root cause

1. A boolean `sentText` represented the whole invocation but did not model ordering relative to tool side effects.
2. The bridge assumed every internal tool returned its normal JSON envelope and treated decode errors as framework errors.
3. Error handling focused on return values but not panics at the extension boundary.
4. The first fallback design tracked only the most recent successful mutation, which was insufficient for mixed multi-tool turns.
5. The initial performance suggestion optimized an unmeasured synchronous write by weakening lifecycle guarantees.
6. Realtime output used independent priority control/audio queues without a causal ordering contract, so terminal TTS control could overtake accepted audio.
7. The first barrier-based repair added avoidable per-sentence synchronization and still needed explicit transient-capacity semantics.
8. Several non-streaming control sends in the touched path discarded backpressure errors.

## Solution

- Model terminal correctness explicitly with function-call/result correlation and post-tool speech ordering.
- Treat malformed tool results as data-level tool failures, not agent-runtime transport failures.
- Fail closed when execution status is ambiguous and prevent automatic retry.
- Contain extension panics centrally.
- Prefer bounded, measurable optimizations over per-request fire-and-forget work.
- Enforce an independent static-review gate in checkpoint tooling, not only in human convention.
- Put causally-related TTS lifecycle events and audio frames on one ordered media lane rather than relying on scheduler timing across independent queues.
- Use bounded wait only for required media lifecycle enqueue; keep ordinary audio enqueue bounded/non-blocking.
- Propagate control enqueue failures instead of silently advancing the turn state.

## Trade-offs accepted

- A successful mutation can receive host fallback `OK.` if ADK/provider terminates without post-tool text. This favors idempotency/user safety over failing a turn after a committed side effect.
- Reads and failed/ambiguous tool executions still fail the invocation if the model produces no final speakable explanation; they are not silently converted to success.
- Tool panic diagnostics are intentionally generic at the model boundary. Structured internal panic telemetry is deferred to CP-SW8 rather than leaking panic values.
- Usage metering remains synchronous until measured. If future traces show meaningful tail latency, the replacement must be bounded and gracefully drained rather than fire-and-forget.

## Rollback

After tag creation:

```text
git checkout CP-SW2.2-20260812   # exact hardened checkpoint
git checkout CP-SW2.1-20260812   # previous checkpoint
```

Runtime-only rollback remains:

```text
COMPANION_AGENT_RUNTIME=legacy
```

## Next

CP-SW2 remains partial until an exact Go 1.26.5 environment with dependency access can run:

1. `go mod tidy` and `go mod verify` against the production graph;
2. `make backend-adk-gate` with `-tags=adk` actually compiling/running;
3. fake-model ADK tool-loop tests covering tool call -> host result -> summarization;
4. Responses-API-compatible local provider contract test;
5. streaming cancellation and parallel tool-call parity tests.

Do not promote ADK to default before these gates pass.

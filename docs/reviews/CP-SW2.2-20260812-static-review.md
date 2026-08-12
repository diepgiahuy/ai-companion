# Independent static review — CP-SW2.2-20260812

Static review status: PASS

Review scope: complete diff from `CP-SW2.1-20260812` to the proposed CP-SW2.2 state. The review is performed after the implementation pass and treats the diff as third-party code rather than validating implementation intent.

## Review dimensions

- Correctness/state machines: tool-call/result/final-text ordering; mixed multi-tool outcomes; TTS audio/control causal ordering.
- Concurrency/lifecycle: mutex ownership, cancellation, stale generations, websocket single-writer behavior, queue bounds, media-lane ordering and bounded capacity waits.
- Error/retry/idempotency: ambiguous side effects, malformed envelopes, panic containment, duplicate function-call events, retry safety.
- Security/privacy/trust: raw tool/panic data exposure, authorization path preservation, framework/domain boundaries.
- Performance/resource bounds: realtime blocking, bounded queues/timeouts, speculative goroutines, accounting integrity.
- Maintainability/architecture: ADK anti-corruption layer, one authoritative ToolRegistry, testability, rollback behavior.
- Test adequacy: focused regression tests, repeated race stress, full race/vet/offline E2E after fixes.

## Findings

| ID | Severity | Finding | Disposition |
|---|---|---|---|
| SR-01 | HIGH | Pre-tool text could be mistaken for a final acknowledgement after a side effect. | FIXED |
| SR-02 | HIGH | A successful mutation could mask a later read/failure in generic fallback logic. | FIXED |
| SR-03 | HIGH | Malformed host results could break the agent continuation path; echoing raw content would leak internals/untrusted text. | FIXED |
| SR-04 | MEDIUM | Parsed JSON without a boolean `ok` envelope could be accepted. | FIXED |
| SR-05 | HIGH | Tool/authorizer panic could escape the common registry boundary. | FIXED |
| SR-06 | MEDIUM | Duplicate streamed function-call events could corrupt incomplete-call accounting. | FIXED |
| SR-07 | MEDIUM | Fire-and-forget usage accounting would weaken shutdown/quota guarantees without measured benefit. | ACCEPTED AS NO-CHANGE; re-evaluate in CP-SW8 only with traces/benchmark. |
| SR-08 | LOW | Host fallback `OK.` is less expressive/localized than model-generated final speech. | ACCEPTED; narrow mutation-only failure fallback. |
| SR-09 | HIGH | Priority control queue could emit TTS terminal control before already accepted audio frames. | FIXED with one ordered turn-scoped FIFO media lane for TTS lifecycle + audio. |
| SR-10 | MEDIUM | The first barrier-based repair added avoidable per-sentence synchronization and could falsely fail on transient media-lane saturation. | FIXED by replacing the barrier with ordered media-lane enqueue; lifecycle events use bounded capacity wait. |
| SR-11 | HIGH | Non-streaming TTS/UI control enqueue errors were ignored in the touched path. | FIXED; fail turn on backpressure/error. |
| SR-12 | MEDIUM | Cancellation during final streaming media enqueue could be swallowed as success. | FIXED; propagate cancellation to outer turn lifecycle. |
| SR-13 | HIGH | Presentation could be emitted before ADK host-result validation. | FIXED; only valid `ok=true` results may publish presentation. |

## Independent review conclusions

The ADK integration remains behind a narrow anti-corruption layer and does not duplicate domain/tool semantics. Tool failures are represented as safe data when continuation is possible, while ambiguous side effects are non-retryable. Panics are contained at the common capability boundary. The realtime writer retains urgent control priority, while causally-related TTS lifecycle events and audio now share one ordered media FIFO. This removes timing assumptions across lanes without forcing a per-sentence writer-drain barrier.

No per-request fire-and-forget goroutine was introduced for usage accounting. The current checkpoint keeps accounting semantics intact until latency is measured and a bounded/drained design is justified.

## Required post-review evidence

Before this review may be changed to PASS and the checkpoint tagged:

- focused tool/outcome/panic tests with `-race`;
- repeated websocket E2E under `-race` to exercise audio/control ordering;
- full backend `go test -race ./...` on the offline-compatible graph;
- `go vet ./...`;
- host firmware simulation + offline E2E;
- boundary/leak/static script checks;
- exact ADK production gate attempted and recorded as PASS or explicitly BLOCKED by environment (never falsely green).

## Final post-review evidence

- Expense/budget websocket E2E: `50/50` PASS under `-race` after ordered media-lane fix.
- Media ordering/backpressure + streaming overlap focused suite: `30/30` PASS under `-race`.
- ADK host/outcome/capability suite: `20/20` PASS under `-race`.
- Full backend `go test -race -count=1 -modfile=go.offline.mod ./...`: PASS.
- Full backend `go vet -modfile=go.offline.mod ./...`: PASS.
- `scripts/e2e_offline.sh`: PASS; host C++ `2/2`, backend functional/race PASS.
- ADK import-boundary, malformed-result leak guard, snapshot-script syntax, and `git diff --check`: PASS.
- Exact production ADK gate: BLOCKED as expected by sandbox toolchain (`go1.23.2`; gate requires `go1.26.5`). This is not recorded as a production PASS.

No CRITICAL/HIGH correctness or security finding remains unresolved in this checkpoint diff.

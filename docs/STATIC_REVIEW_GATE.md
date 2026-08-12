# Independent static review gate

Every implementation checkpoint must receive a static review **after the implementation diff is complete and before the checkpoint is tagged or snapshotted**. The review is intentionally separate from implementation reasoning to reduce confirmation bias.

A checkpoint may be tagged only when its checkpoint note contains `## Independent static review` and `Static review status: PASS`, and a matching independent review report at `docs/reviews/<checkpoint-tag>-static-review.md` also records `Static review status: PASS`.

The reviewer inspects the complete diff and records findings across these dimensions:

1. Correctness and state-machine invariants: terminal states, ordering, partial failures, duplicate/replayed events, malformed inputs, boundary conditions.
2. Concurrency and lifecycle: data races, goroutine ownership, cancellation, shutdown, queue bounds, deadlocks, stale work, context propagation.
3. Error semantics and recovery: fail-open/fail-closed choice, retry safety, idempotency, ambiguous side effects, error classification, rollback behavior.
4. Security and privacy: authorization, capability boundaries, prompt/tool injection, secret/raw-internal-data leakage, untrusted data handling, least privilege.
5. Performance and resource bounds: avoid speculative asynchronous work, unbounded goroutines/queues/allocations, blocking on latency-critical paths, measurable evidence before optimization.
6. Maintainability and architecture: duplicated business rules, framework leakage, replaceable provider boundaries, API contracts, naming, comments, testability, dependency pins.
7. Test adequacy: each fixed finding gets a regression test where practical; race/static/E2E gates are rerun after review changes.

Review findings use severity `CRITICAL | HIGH | MEDIUM | LOW` and disposition `FIXED | ACCEPTED | DEFERRED`. `DEFERRED` findings must name an owner/checkpoint and cannot include an unresolved release-blocking correctness or security issue.

The checkpoint snapshot script enforces the presence of the review section/status so a source ZIP or Git bundle cannot be produced from an unreviewed tagged state.

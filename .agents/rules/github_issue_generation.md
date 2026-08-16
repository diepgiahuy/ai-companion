# GitHub issue generation for agent delegation

Use this guidance when an issue will be implemented, measured, qualified, or owned by a
human or coding agent.

## Research before specification

1. Inspect the current repository, live GitHub state, and recent relevant changes before
   naming files, symbols, commands, dependencies, architecture patterns, or blockers.
2. Research time-sensitive technology and security choices using current primary
   sources for the exact version/provider/hardware family involved.
3. Compare options by repository fit, maintenance, resource cost, migration cost,
   security, and measurable product value.
4. Treat an unmeasured framework, algorithm, provider, hardware, or common-practice
   choice as a hypothesis. Do not turn "latest", a vendor claim, or an example into a
   mandatory requirement.
5. Record why a decision is already fixed. Otherwise leave it as an open decision with
   a benchmark/spike exit criterion.

## Issue types are different contracts

Do not force every open issue into one implementation template.

- **Implementation issue** — executable change with a concrete output, observable
  acceptance criteria, scenarios, and required evidence. This is the normal unit a
  coding agent may claim when `status:ready`.
- **Spike / measurement / selection issue** — answers one explicit decision question
  with reproducible evidence and a bounded decision output. Experimental code that is
  not selected is removed rather than becoming a hidden product path.
- **Evidence / HIL / manufacturing issue** — proves an already-defined property at the
  required evidence tier. It must not silently redesign the owning feature during
  qualification; failures reopen/create a focused implementation issue.
- **Feature parent / epic** — owns durable product outcome, invariants, child ownership,
  and closure conditions. It is not a coding branch or live status dashboard.
- **Release gate** — freezes a required set, proves integrated behavior, and blocks
  promotion on failed evidence; it is not a feature bucket.

## Strict executable-issue contract

A Ready implementation issue should be usable as the implementation contract without a
separate hand-copied plan document. It should contain, when relevant:

### Output

- **Product output:** user/system-visible behavior that will exist when complete.
- **System guarantees:** durable/protocol/state guarantees that must become true.
- **Failure guarantees:** required behavior under invalid input, retry, restart,
  partial failure, concurrency, revocation, timeout, or deletion as applicable.

### Current state

Record only the relevant facts believed true when the issue is written. Make the
staleness boundary explicit: the implementer must revalidate these claims against exact
current `main` and live GitHub state before relying on them.

### Invariants

List architecture, security, privacy, data, protocol, compatibility, and evidence rules
that the change must preserve. Prefer durable behavior over brittle symbol-by-symbol
instructions.

### Scope and non-goals

State the owned outcome and explicit exclusions. Do not hide adjacent cleanup or product
expansion inside a feature implementation.

### Fixed decisions and unknowns

Separate already-decided product/architecture contracts from hypotheses. A material
open product, security, persistence, concurrency, migration, provider, or hardware
question means the implementation issue is not Ready; use a Spike/decision issue.

### Acceptance criteria

Use observable criteria with stable IDs such as `A1`, `A2`, ... when the issue is
non-trivial. Acceptance should describe what must be true, not merely which function or
file should exist.

### Required scenarios

For non-trivial issues, define realistic scenario IDs such as `S1`, `S2`, ... with:

- setup/input;
- expected behavior;
- owning oracle/test layer;
- required evidence tier where relevant.

Include relevant negative and recovery behavior. Do not write only "add unit and
integration tests".

### Security / privacy / compatibility

Name actual trust boundaries, actor/owner scope, replay/idempotency, data lifecycle,
secret handling, compatibility/migration, and fail-closed behavior that matter to the
change.

### Dependencies and live status

Use native GitHub blocker/sub-issue/project state as the live dependency source. Prose
may explain intent but must not be treated as authoritative current status after the
issue ages.

### Rollback / recovery

State how the behavior can be disabled/reverted/recovered without introducing a
permanent parallel product architecture.

### Escalation

The implementer must stop and report a focused `ISSUE_STALE`, `NEEDS_DECISION`, or
`INSUFFICIENT_EVIDENCE` condition instead of guessing when:

- current `main` materially contradicts the issue's required outcome/invariants;
- product intent or acceptance criteria conflict;
- implementation requires material scope expansion;
- a material correctness/security/data/migration/concurrency fact remains unresolved;
- the required oracle/evidence cannot actually be executed.

## Verification ladder for unknown facts

When an issue or implementation depends on a fact that is not already proven:

1. verify current project facts from current repository/GitHub state;
2. verify external/version-sensitive facts from current primary/official sources;
3. use the established current repository pattern for project-specific design when it
   already solves the same problem;
4. use common industry practice only as a candidate design if the above do not decide
   the question;
5. if a material uncertainty remains, run the smallest focused spike that can resolve
   it or request a decision.

Never convert common practice into repository truth without evidence.

## Testing guidance

Use the cheapest oracle that can actually fail when the required behavior fails:

- Pure logic and state machines: deterministic unit tests.
- Hardware/provider boundaries: fakes or mocks for errors and edge cases.
- Persistence and protocol: integration tests against the implemented adapter.
- Simulation: Wokwi or another simulator when it represents the relevant behavior.
- Physical behavior: trusted-ref HIL for RF, timing, peripherals, audio, display,
  power, OTA, and other hardware-dependent claims.

A test must prove the relevant acceptance/scenario property, not merely exist. Never
mask build or test errors with `|| true`, `|| echo`, ignored exit codes, fictional test
commands, or a different commit's result.

## Final review contract

Before a non-trivial software issue is considered done, review the final integrated
diff from a fresh/independent context where practical. The reviewer should evaluate:

1. requested Product/System/Failure outputs;
2. each acceptance criterion;
3. whether each required scenario is actually proven by its oracle;
4. preserved invariants and non-goals;
5. adversarial correctness, security/privacy, data integrity, concurrency/lifecycle,
   compatibility/rollback, and evidence gaps.

Review findings should block only for correctness, stated requirements, security/privacy,
data integrity, required compatibility/recovery, or missing/invalid required evidence.
Optional style suggestions must not create an endless remediation loop.

## Delegation and PRs

Use one accountable lead per issue. Split work when it spans independent domains or
cannot be reviewed safely as one coherent change. Parallel agents require a stable
shared contract, explicit branch base, non-overlapping ownership, and an integrator for
shared files. Stacked PRs are useful for real dependencies, not mandatory ceremony.

Do not include generic instructions such as "do not hallucinate", "self-heal", "use
state of the art", or arbitrary line-count limits. Give the assignee concrete output,
boundaries, scenarios, evidence, and success criteria instead.

GitHub issues are the tracker source of truth for requested outcomes. Do not maintain a
hand-copied issue body under `docs/`; link an ADR or design document only when it contains
durable technical reasoning beyond the ticket.

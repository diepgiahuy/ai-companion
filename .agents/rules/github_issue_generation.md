# GitHub issue generation for agent delegation

Use this guidance when an issue will be implemented by a human or coding agent.

## Research before specification

1. Inspect the current repository and recent changes before naming files, symbols,
   commands, dependencies, or architecture patterns.
2. Research time-sensitive technology and security choices using primary sources.
3. Compare options by repository fit, maintenance, adoption, resource cost,
   migration cost, security, and measurable performance.
4. Treat an unmeasured framework or algorithm choice as a hypothesis. Do not turn
   "latest", a vendor claim, or an example into a mandatory requirement.
5. Record why a decision is already fixed. Otherwise leave it as an open decision
   with a benchmark or spike exit criterion.

## Ready-issue contract

Every implementation issue should contain:

- **Outcome:** user or system behavior that will exist when complete.
- **Current state:** verified files, symbols, behavior, and limitations.
- **Scope:** changes owned by this issue.
- **Non-goals:** adjacent work intentionally excluded.
- **Decisions and open questions:** fixed contracts versus hypotheses to evaluate.
- **Acceptance criteria:** observable outcomes, including failure behavior.
- **Test plan:** unit/host, integration, simulator, HIL, and manual layers that are
  relevant. Do not require every layer for every change.
- **Security and privacy:** trust boundaries, authorization, data lifecycle,
  retention, deletion, replay, abuse, and secrets where applicable.
- **Dependencies and ownership:** blocking issues, merge order, and likely shared
  files.
- **Rollout and rollback:** how to enable, observe, disable, or revert the change.

Use actual repository paths. A new path must be marked as new and justified.

## Testing guidance

Use a test pyramid rather than a blanket "no mocks" rule:

- Pure logic and state machines: deterministic unit tests.
- Hardware/provider boundaries: fakes or mocks for errors and edge cases.
- Persistence and protocol: integration tests against the implemented adapter.
- Simulation: Wokwi or another simulator when it represents the relevant behavior.
- Physical behavior: trusted-ref HIL for RF, timing, peripherals, audio, display,
  power, OTA, and other hardware-dependent claims.

A test must fail when the behavior fails. Never mask build or test errors with
`|| true`, `|| echo`, ignored exit codes, or fictional test commands.

## Delegation and PRs

Use one accountable lead per issue. Split work when it spans independent domains or
cannot be reviewed safely as one change. Parallel agents require a stable shared
contract, explicit branch base, non-overlapping ownership, and an integrator for
shared files. Stacked PRs are useful for real dependencies, not mandatory ceremony.

Do not include generic instructions such as "do not hallucinate", "self-heal", "use
2026 state of the art", or arbitrary line-count limits. Give the assignee concrete
context, boundaries, evidence, and success criteria instead.

GitHub issues are the tracker source of truth. Do not maintain a hand-copied issue
body under `docs/`; link an ADR or design document only when it contains durable
technical reasoning beyond the ticket.

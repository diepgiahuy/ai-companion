# AI-assisted development workflow

This workflow keeps human ownership while allowing coding agents to execute bounded
work independently. It applies to humans and agents equally.

## 1. Intake and repository check

Before implementation:

1. Restate the requested outcome.
2. Inspect the current implementation, tests, configuration, and recent changes.
3. Identify security, privacy, hardware, migration, and external-write risks.
4. Classify statements as current fact, fixed requirement, or hypothesis.
5. Create or refine an issue when the work is large, delegated, or has dependencies.

Small, local, low-risk fixes do not require a new issue.

## 2. Issue readiness

An issue is ready when it defines:

- the outcome and observable acceptance criteria;
- verified current-state references;
- scope and non-goals;
- fixed decisions and open questions;
- relevant test layers;
- security/privacy/data-lifecycle requirements;
- dependencies, ownership, and merge order;
- rollout and rollback expectations.

An issue is not a binding architecture document. If implementation discovery
invalidates an assumption, update the issue and explain the decision.

## 3. Decomposition and coordination

Use one accountable lead per issue. Split an issue when it combines separately
reviewable domains such as schema, backend lifecycle, firmware behavior, and
hardware selection.

Parallel work is appropriate only when:

- contracts and ownership are stable;
- agents use separate branches or worktrees;
- shared files and migrations have one integrator;
- each branch declares its base and dependency;
- results can be validated independently.

Do not force all work into architectural layers or stacked PRs. Use a stack only
when one change genuinely depends on another.

## 4. Implementation loop

1. Make the smallest coherent change that advances an acceptance criterion.
2. Run the narrowest relevant check.
3. Read actual output and fix root causes. Do not mask failures.
4. Add broader integration, simulation, or HIL coverage when the behavior crosses
   those boundaries.
5. Review the diff for unrelated changes, unsafe defaults, missing failure handling,
   privacy regressions, and rollback gaps.
6. Record commands actually run and any validation that could not run.

Safe local inspection, editing, and tests do not require repeated approval.
Production writes, destructive actions, external side effects, purchases,
irreversible hardware operations, and material scope expansion do.

## 5. Validation tiers

These tiers describe evidence types, not CI jobs that are guaranteed to exist.

- **L0 Static:** formatting, compilation, schema checks, lint, dependency review.
- **L1 Unit/host:** pure logic, state machines, error paths, fakes at ports.
- **L2 Integration:** implemented database, filesystem/blob, protocol, and provider
  contracts.
- **L3 Simulation:** Wokwi or equivalent where simulated peripherals are meaningful.
- **L4 Physical HIL:** trusted firmware on real boards and peripherals.
- **L5 Release:** security provisioning, OTA rollback, soak, fault, backup/restore,
  and artifact provenance as applicable.

Use the lowest tier that proves the claim. A physical test does not replace missing
deterministic tests, and a mock does not prove physical behavior.

## 6. CI and HIL safety

The repository is public. Pull requests must not automatically execute arbitrary
code on a personal self-hosted runner.

- Pull-request checks use GitHub-hosted or otherwise isolated ephemeral runners.
- Physical HIL uses manual authorization and a trusted repository ref.
- The checked-out commit SHA is recorded with the result.
- Build and test failures fail the job.
- HIL reports identify the board/port, firmware artifact, test set, and toolchain.
- Feature-specific HIL is added only after the firmware exposes real behavior or a
  documented test-control interface.

## 7. Review and merge

A PR should explain outcome, scope, tests, risk, and rollback. Human review is
required for architecture changes, privacy/security policy, migrations, hardware
changes, production rollout, or other high-impact work. Branch protection and
repository permissions, not prose alone, enforce merge policy.

An agent may prepare a PR and address feedback. Merge only when the repository's
configured checks and required approvals are satisfied.

## 8. Evidence vocabulary

Use precise status terms:

- **Implemented:** code exists.
- **Tested:** named checks ran and passed in a stated environment.
- **HIL-tested:** named physical scenario ran on identified hardware.
- **Production-proven:** defined release/SLO criteria passed in production-like use.
- **Planned:** accepted direction with no implementation claim.

Do not create a global evidence file unless a concrete release process consumes and
validates it.

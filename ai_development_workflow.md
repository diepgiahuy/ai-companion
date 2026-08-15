# AI development workflow

This is the single human-readable implementation lifecycle for AI-assisted changes in
this repository. It optimizes for small coherent batches, short feedback loops, and
proof another person can reproduce. Product test capabilities remain documented in
`docs/TEST_EVIDENCE_LADDER.md`.

## One truth per concern

| Concern | Canonical source |
|---|---|
| Portfolio priority / NOW-NEXT-LATER | GitHub Project |
| Requirement / acceptance | GitHub Issue |
| Implementation under review | PR diff at HEAD |
| Execution explanation | PR description |
| Hosted automated proof | GitHub Checks / artifacts |
| Promoted evidence claim | `evidence/status.json` |
| Merged product truth | `main` code/schema/config |
| Durable architecture / decision | architecture docs / ADR |
| Product introduction / quick start | README |

Do not copy live branch, open-PR, or execution-queue status into README or architecture
docs. An agent report is never proof.

## Core rule

Use the cheapest objective oracle that can catch the failure. Broaden verification only
when the change crosses a real boundary or reaches final review. More commands, agents,
artifacts, or narration are not stronger evidence by themselves.

## Execution flow

```text
GitHub Issue
  -> resolve live dependencies and current main
  -> load narrow context
  -> classify actual risk
  -> one accountable lead by default
  -> implement + nearest oracle
  -> final integrated diff review
  -> exact-head hosted CI
  -> merge
  -> human-only / higher-tier evidence separately when required
```

### Ready to implement

Before changing code, establish:

- intended outcome and explicit non-goals;
- live blockers/dependencies;
- current implementation seam and owned files/contracts;
- acceptance items that are software-testable versus human-only;
- nearest direct oracle;
- risk level from the table below.

Stop discovery when those facts are known. If the work still contains an unresolved
technology/product choice, create or finish a Spike rather than pretending the
implementation issue is ready.

### Narrow implementation loop

Repeat only:

1. Add/update one observable oracle.
2. Make the smallest coherent code change.
3. Run touched-file formatting/static checks and the nearest package/host/integration
   test that owns the behavior.
4. Fix the first useful failure. Do not rerun unrelated green suites.

After the same unexplained failure twice, stop retrying and inspect the root cause.

### Final local verification

Run the risk-matched final local gate once against the final local diff. Record command,
result, and tested commit SHA in the PR. Rerun only when later edits can affect that
oracle.

Local proof is diagnostic evidence. Required GitHub Checks are the exact-head hosted
merge oracle.

### Review

Review the complete final diff once, prioritizing correctness, security/privacy,
concurrency/lifecycle, rollback, dead fallback paths, and missing acceptance tests.
Fix findings and rerun only affected oracles. A second independent review is required
only for L3/release-critical changes or when the first review exposes systemic risk.

### Hosted proof and merge

Required GitHub Checks at current PR HEAD are authoritative hosted proof. Do not copy
hosted check lists or a synthetic `hosted-proven` status into the PR body.

After merge, delete/supersede the implementation branch unless it still contains unique
reviewed work. Do not accumulate `-v2`, `-v3`, `-final-v2` branches as project state.

### Human-only completion

Hardware, credentials, purchases, production access, irreversible operations, and
subjective product decisions are separate gates. Software work can merge while the
parent feature remains open for a named human/evidence child issue.

If an issue itself still requires the human gate, use `Refs #N`, not `Closes #N`.

## Risk-matched gates

| Risk | Typical change | Local loop | Final local gate | Extra proof |
|---|---|---|---|---|
| L0 | docs/comments/metadata | syntax/link/contract check | relevant validator | normal PR CI |
| L1 | pure logic/isolated UI/tooling | nearest unit/host test | touched package/tool tests | normal PR CI |
| L2 | API/protocol/auth/persistence/firmware FSM | unit + focused integration | affected packages + one boundary E2E | exact-head CI + final diff review |
| L3 | migration/destructive data/concurrency runtime/credential/security/release | focused failure/recovery tests | full relevant race/integration/recovery gate | independent review + immutable evidence only where it proves a distinct property |

Risk can rise during implementation. Do not mark an entire issue L3 merely because the
repository contains L3 code.

## Bounded delegation

Default accountable implementation leads per task: **1**.

```text
                  TASK
                   |
          Can one lead do it safely
           in one coherent lane?
             /          \
           YES           NO
            |             |
       don't spawn     Can split cleanly?
                     /                \
                   NO                  YES
                    |                   |
                sequential          parallel
                                 only useful lanes
```

A split is clean only when all are true:

- the shared contract is already stable;
- primary file/path ownership does not overlap materially;
- each lane has an independent pass/fail oracle;
- no lane depends on another lane's unmerged output.

Use parallel implementation only when its expected value is higher than coordination
cost. The practical repository target is no more than about two active product-code
implementation lanes at once; research, read-only triage, or independent verification
do not count as product-code lanes.

Good delegated work: read-only repository/issue triage, isolated stable-interface
packages, named test execution returning SHA/command/exit/artifact, or independent L3
final-diff review.

The lead owns integration, current-main reconciliation, final proof, and the PR record.
Workers do not create side-channel status files, duplicate GitHub comments, or vendor
workflow frameworks.

## PR execution record

The PR description is the canonical execution explanation for the change. It should be
compact and current, not a raw activity log.

Required core:

- linked issue;
- risk L0-L3;
- local tested head SHA or `not-run`;
- human gate;
- summary and scope/non-goals;
- verification table with direct oracles;
- evidence boundary: what the PR proves and does not prove;
- risk/rollback;
- remaining PR blockers, issue work, and human action;
- `Refs` versus `Closes` semantics consistent with remaining work.

Optional only when useful: decision rationale, review map, or execution summary. Do not
copy every hosted CI check, raw logs, or per-agent activity into the PR body.

`scripts/check_pr_contract.py` enforces mechanical structure only. It must never decide
whether architecture is good, tests are sufficient, or an issue is semantically done.

## Test and evidence rules

- Prefer deterministic assertions over logs and prose.
- Test behavior at the lowest layer that owns it, then one boundary test for wiring.
- Mocks prove orchestration/failure handling, not provider/RF/acoustic/hardware quality.
- Follow `docs/TEST_EVIDENCE_LADDER.md` for promotion language.
- `evidence/status.json` changes only when a real evidence claim is promoted or
  invalidated; it is not a changelog.
- Artifacts/digests are required only when artifact identity matters (migration,
  restore, firmware image, benchmark corpus, physical evidence, etc.).
- Never mark PASS from source inspection, expected output, a different SHA, or an
  uninspectable agent report.

## No-duplicate rule

Before an expensive gate ask:

1. What specific failure can it catch?
2. Was that property already tested on the same code?
3. Did code/environment change since that proof?
4. Will the result change the merge decision?

Skip a redundant gate when it has no new decision value. Unknown/high-risk changes fail
safe by broadening verification; normal changes should not repeat release-grade proof.

## Definition of done

A software child issue is done when:

- requested behavior exists;
- the direct positive and relevant negative oracle pass;
- the final diff has no unresolved release-blocking finding;
- no unrelated fallback/parallel product path was introduced;
- required exact-head GitHub Checks pass;
- rollback is understood;
- the PR states any remaining unproven higher-tier claim;
- code is merged to `main` and the implementation branch is retired.

Parent features may remain open for separate provider/HIL/human qualification without
keeping completed software implementation issues artificially open.

## After 5-10 PRs

Measure review findings, CI reruns, stale-evidence incidents, merge conflicts, duplicate
expensive tests, incorrect issue closures, README/status drift, PR lead time, and average
agents per task. Convert a repeated deterministic failure into a linter. Create a Skill,
nested `AGENTS.md`, or additional framework only after repeated evidence demonstrates a
real need.

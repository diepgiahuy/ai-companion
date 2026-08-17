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
docs. An agent report is never proof. Issue prose may age; live blockers and current
implementation facts must be refreshed before implementation.

## Core rule

Use the cheapest objective oracle that can catch the failure. Broaden verification only
when the change crosses a real boundary or reaches promotion/final review. More commands,
agents, artifacts, or narration are not stronger evidence by themselves.

Do not make a material assumption silently. Verify project facts from current repo/GitHub
state, external/version-sensitive facts from current primary sources, and use common
practice only as a candidate design when those sources do not decide the question. A
material unresolved correctness/security/data/migration/concurrency/product uncertainty
requires a focused spike or decision before implementation continues.

## Execution flow

```text
GitHub Issue
  -> refresh exact main + live blockers
  -> revalidate issue current-state claims and external facts
  -> classify drift/risk
  -> one accountable lead by default
  -> implement + nearest oracle
  -> refresh/reconcile main if it moved materially
  -> fresh final integrated diff review
  -> exact-head targeted PR Gate
  -> merge
  -> broad Promotion Gate on exact main SHA
  -> human-only / higher-tier evidence separately when required
```

### Mandatory preflight / issue revalidation

Before changing code:

1. Fetch exact current `main`, the owning issue, and live blockers/dependencies.
2. Verify only the relevant current implementation seams, files/contracts, schema,
   configuration, and tests named or implied by the issue.
3. Re-check time-sensitive external APIs/frameworks/providers/hardware from current
   primary sources when the implementation depends on them.
4. Classify the issue against current truth:
   - **VALID** — current facts and required output remain aligned;
   - **NON_MATERIAL_DRIFT** — implementation details changed but output/acceptance are
     still valid; adapt the implementation approach and continue;
   - **MATERIAL_DRIFT / ISSUE_STALE** — required output/invariants or architecture
     assumptions conflict materially; re-plan/update the issue before coding;
   - **NEEDS_DECISION / UNKNOWN_MATERIAL_FACT** — a material product, security, data,
     migration, concurrency, provider, or hardware fact remains unresolved; spike or
     request the decision rather than guessing.
5. Establish intended Product/System/Failure output, explicit non-goals, acceptance
   criteria, required scenarios/evidence, nearest oracle, and actual risk level.

Do not edit acceptance criteria merely to fit the current implementation. `main` answers
what exists now; the issue answers what outcome is still required. If those truths truly
conflict, escalate instead of choosing one silently.

Stop discovery when the material facts are known. If the work still contains an
unresolved technology/product choice, create or finish a Spike rather than pretending
the implementation issue is Ready.

### Narrow implementation loop

Repeat only:

1. Add/update one observable oracle tied to an acceptance criterion or required
   scenario.
2. Make the smallest coherent code change.
3. Run touched-file formatting/static checks and the nearest package/host/integration
   test that owns the behavior.
4. Fix the first useful failure. Do not rerun unrelated green suites.

After the same unexplained failure twice, stop retrying and inspect the root cause.
Do not expand scope merely because an adjacent cleanup is convenient.

### Reconcile current main before final review

If `main` moved while the issue was being implemented, inspect the intervening changes
that overlap the owned surfaces or assumptions. Mechanical/unrelated drift does not
force a re-plan. Semantic drift that changes the required implementation or invalidates
an oracle returns the issue to revalidation before final review.

### Final local verification

Run the risk-matched final local gate once against the final local diff. Record command,
result, and tested commit SHA in the PR when local proof actually ran. Rerun only when
later edits can affect that oracle.

Local proof is diagnostic evidence. The ruleset-required `PR Gate` is the exact-head
hosted merge oracle; promotion/release claims require their stronger gate separately.

### Fresh final review

Review the complete final integrated diff from a fresh/independent context where
practical. The reviewer should not rely on the implementer's reasoning as proof; the
review inputs are the issue contract, relevant current code, exact PR diff/HEAD, and
actual evidence.

Review in this order:

1. **Output:** does the Product/System/Failure output actually exist?
2. **Acceptance:** evaluate every non-trivial acceptance criterion; use stable `A*`
   mappings when the issue defines them.
3. **Scenarios/evidence:** do the required `S*` scenarios have an oracle that would fail
   if the behavior regressed, and did the required tier actually run?
4. **Invariants/scope:** did the change preserve architecture/security/data boundaries
   and avoid non-goals or parallel fallback paths?
5. **Adversarial review:** look for correctness, auth/privacy, data-integrity,
   concurrency/lifecycle, retry/restart/partial-failure, compatibility/rollback, dead
   fallback, and missing-evidence counterexamples not already covered by the issue.

A review verdict should distinguish:

- **PASS** — no release-blocking finding at this HEAD;
- **CHANGES_REQUIRED** — implementation or required evidence is wrong/incomplete;
- **ISSUE_STALE / SPEC_DECISION_REQUIRED** — the issue itself lacks or conflicts on a
  material requirement;
- **INSUFFICIENT_EVIDENCE** — required proof did not run or does not prove the claim.

Block only for correctness, stated requirements, security/privacy, data integrity,
required compatibility/recovery, or missing/invalid required evidence. Optional style
suggestions should not create endless remediation loops. After a fix, rerun the affected
oracles and re-review the new relevant diff/HEAD; a previous PASS is not proof for a new
HEAD.

A second independent review is required for L3/release-critical changes or when the
first review exposes systemic risk.

### Hosted proof, merge and promotion

The repository ruleset-required `PR Gate` at the current PR HEAD is the authoritative
hosted **merge** gate. It is intentionally risk-aware: ordinary PRs run the nearest
relevant oracles, while cross-boundary/data/protocol changes retain the stronger boundary
checks that can catch their real failure modes. Draft/ready-for-review state does not
select fast versus full verification.

After merge, `main` runs the broad software suite and aggregates one exact-SHA
`Promotion Gate`. Promotion/release must not infer broad proof from the lighter PR Gate.
If Promotion Gate fails, diagnose the owning lane and keep that SHA unpromoted; do not
weaken PR Gate or release policy to hide the failure.

Domain-specific workflows may provide additional required-by-process evidence for the
affected boundary; do not claim that evidence until the corresponding check actually
succeeds on the tested SHA.

Do not copy hosted check lists or a synthetic `hosted-proven` status into the PR body.
After merge, the repository automatically deletes the implementation branch when GitHub
can identify it as the merged PR head. Preserve a branch only when it still contains
unique reviewed work. Do not accumulate `-v2`, `-v3`, `-final-v2` branches as project
state.

### Human-only completion

Hardware, credentials, purchases, production access, irreversible operations, and
subjective product decisions are separate gates. Software work can merge while the
parent feature remains open for a named human/evidence child issue.

If an issue itself still requires the human gate, use `Refs #N`, not `Closes #N`.

## Risk-matched gates

| Risk | Typical change | Local loop | Final local gate | Extra proof |
|---|---|---|---|---|
| L0 | docs/comments/metadata | syntax/link check | relevant validator | targeted PR Gate |
| L1 | pure logic/isolated UI/tooling | nearest unit/host test | touched package/tool tests | targeted PR Gate |
| L2 | API/protocol/auth/persistence/firmware FSM | unit + focused integration | affected packages + one boundary E2E | exact-head targeted CI + final diff review |
| L3 | migration/destructive data/concurrency runtime/credential/security/release | focused failure/recovery tests | full relevant race/integration/recovery gate | independent review + broad promotion/immutable evidence where it proves a distinct property |

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
compact and current, not a raw activity log, a copy of the issue, or a second CI
database.

Default useful content:

- link the owning issue and state the implemented outcome/why;
- map non-trivial `A*` acceptance items to implementation/evidence when that helps
  review;
- list local verification only when it actually ran; GitHub Checks own exact-head proof;
- state meaningful risk and a concrete rollback when the change has one;
- state remaining PR work, issue work, or human/hardware/provider gates;
- use `Refs` versus `Closes` consistently with whether the issue is actually complete.

Add scope/non-goals, evidence boundaries, decisions, or a review map only when they help
a reviewer make a decision. The PR template is guidance, not a prose correctness gate.
Mechanical prose wording must not block a technically valid merge; repeated objective
mistakes belong in code/test/CI linters instead.

## Test and evidence rules

- Prefer deterministic assertions over logs and prose.
- Test behavior at the lowest layer that owns it, then one boundary test for wiring.
- A scenario test must prove its acceptance property, not merely execute the code path.
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

- the requested Product/System/Failure outputs exist;
- all required acceptance criteria and relevant positive/negative/recovery scenarios
  pass at their required evidence level;
- the final diff has no unresolved release-blocking finding;
- no unrelated fallback/parallel product path was introduced;
- current-main drift was reconciled;
- required exact-head GitHub Checks pass;
- rollback/recovery is understood;
- the PR states any remaining unproven higher-tier claim;
- code is merged to `main` and the implementation branch is retired.

A merged SHA is not automatically a promoted/release-qualified SHA. Broad software,
provider, physical, manufacturing, or human gates remain separate when the claimed
property requires them.

Parent features may remain open for separate provider/HIL/human qualification without
keeping completed software implementation issues artificially open.

## After 5-10 PRs

Measure review findings, CI reruns, stale-issue incidents, stale-evidence incidents,
merge conflicts, duplicate expensive tests, incorrect issue closures, README/status
drift, PR lead time, and average agents per task. Convert a repeated deterministic
failure into a linter. Create a Skill, nested `AGENTS.md`, or additional framework only
after repeated evidence demonstrates a real need.

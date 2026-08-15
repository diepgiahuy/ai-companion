# AI development workflow

This is the human-readable execution contract for AI-assisted changes in this
repository. It optimizes for reliable outcomes, short feedback loops, and evidence
that another person can reproduce. Product test capabilities remain documented in
`docs/TEST_EVIDENCE_LADDER.md`.

## Core rule

Use the cheapest objective check that can catch the failure. Broaden verification
only when the change crosses a real boundary or reaches its final review state.

More commands, agents, artifacts, or narration are not stronger evidence by
themselves. A claim is proven only by a relevant pass/fail oracle executed against
the code being reviewed.

## Flow

```text
issue truth
  -> acceptance matrix
  -> narrow implementation loop
  -> risk-matched local gate
  -> final diff review
  -> exact-SHA hosted CI
  -> merge
  -> human-only gate, if any
```

### Checkpoint 1: Ready to implement

Record in the task or PR:

- intended outcome and explicit non-goals;
- live issue dependencies and whether they are closed;
- owned files/contracts and likely test commands;
- acceptance items classified as software-testable or human-only;
- risk level from the table below.

Stop discovery when these facts are known. Do not audit the whole repository unless
the issue changes a cross-cutting contract or explicitly requests a repository audit.

### Checkpoint 2: Behavior implemented

During implementation, repeat only this narrow loop:

1. Add or update one observable test/oracle.
2. Make the smallest coherent code change.
3. Run formatter/static checks for touched files and the nearest package or host test.
4. Fix the first useful failure; do not rerun unrelated green suites.

After the same unexplained failure occurs twice, stop repeating it. Inspect the root
cause directly or escalate the exact blocker with the command and output.

### Checkpoint 3: Locally verified

Run the risk-matched gate once on the final local diff. Save command, result, and
tested commit. A rerun is required only when subsequent edits can affect that gate.

### Checkpoint 4: Review complete

Review the complete final diff once, prioritizing correctness, security/privacy,
concurrency/lifecycle, rollback, and missing acceptance tests. Fix findings and rerun
only affected checks. A second independent review is required only for high-risk or
release-checkpoint changes.

### Checkpoint 5: Hosted proof and merge

GitHub required checks on the PR's exact head SHA are the authoritative hosted proof.
Do not duplicate the same full suite locally and in multiple ad-hoc workflows unless
the duplicate has a distinct failure oracle. After merge, watch push CI only for
high-risk changes or when branch and push workflows materially differ.

### Checkpoint 6: Human-only completion

Hardware, credentials, purchases, production access, irreversible device operations,
and subjective product decisions are reported separately. Software work may be
`software-proven` while the issue remains open for a named human gate. Include a
step-by-step command/UI runbook, expected result, evidence to capture, and rollback.

## Risk-matched gates

| Risk | Typical change | Local implementation loop | Final local gate | Extra proof |
|---|---|---|---|---|
| L0 | Docs, comments, metadata | syntax/link check | relevant document validator | none |
| L1 | Pure logic or isolated UI | nearest unit/host test | touched package tests | PR CI |
| L2 | API, protocol, auth, persistence, firmware FSM | unit + focused integration | affected packages plus one boundary E2E | exact-SHA CI and final diff review |
| L3 | migration, destructive data path, concurrency runtime, credential/security boundary, release | focused failure/recovery tests | full relevant race/integration/recovery gate | independent review, immutable artifact when it proves a distinct property, post-merge CI |

Risk can increase during implementation when evidence reveals a broader boundary.
Do not classify an entire issue as L3 merely because the repository contains L3 code.

## Test and evidence rules

- Prefer deterministic assertions over logs and agent-written summaries.
- Test behavior at the lowest layer that owns it, then add one boundary test for
  wiring. Do not reproduce the same scenario at every tier.
- Mocks prove orchestration and failure handling, not provider, radio, acoustic, or
  hardware quality. Follow `docs/TEST_EVIDENCE_LADDER.md` for promotion language.
- Local tests are fast diagnostics. Required exact-SHA CI is the hosted merge oracle.
- Record artifacts and digests only when artifact identity matters, such as migration,
  restore, firmware image, benchmark corpus, or physical evidence.
- Never mark `passed` from source inspection, expected output, a different SHA, or an
  agent report that cannot expose the executed command and result.

## Review rules

Normal PR review is one final-diff pass. `docs/STATIC_REVIEW_GATE.md` applies to an
immutable release checkpoint, not every implementation commit or normal PR.

Findings come first and include severity, file/line, user impact, and the missing or
failing oracle. Avoid style findings unless they cause ambiguity or maintenance risk.
If there are no findings, state the residual test or environment gaps.

## Agent delegation

One lead owns the issue and final proof. Delegate only bounded lanes with stable
inputs, non-overlapping outputs, and a verifiable return contract.

Good delegated lanes:

- read-only dependency or issue triage returned as structured data;
- an isolated package with a stable interface;
- running a named test suite and returning command, SHA, exit code, and artifact;
- independent final-diff review.

Do not delegate implementation when the worker cannot access the required repository,
sandbox, dependency, service, or test runner. Do not use multiple agents to review the
same surface without a measured reason. The lead re-runs or checks critical evidence;
an agent's prose is not proof.

## No-duplicate rule

Before running a costly gate, ask:

1. What specific failure can this catch?
2. Was that property already tested on the same code?
3. Did the code or environment change since that proof?
4. Will the result change the merge decision?

Skip the gate if no answer identifies new decision value. Prefer one parameterized
command or CI matrix over repeated commands that test the same property.

## Human-readable status template

Use this compact format in task updates and PR descriptions:

```text
Checkpoint: <ready | implementing | locally-verified | reviewed | hosted-proven | merged>
Scope: <one sentence>
Risk: <L0-L3 and reason>
Changed: <observable behavior, not a file inventory>
Proof: <command/workflow + SHA + result>
Remaining: <next software step or named human-only gate>
Blocker: <none, or exact blocker and owner>
```

## Definition of done

A software change is done when acceptance behavior is implemented, relevant tests pass,
the final diff has no unresolved release-blocking finding, exact-SHA required CI passes,
rollback is understood, and documentation states any unproven higher-tier claim. Release
tags, checkpoint reports, artifact digests, post-merge watchers, and physical evidence
are added only when the risk table or release process requires them.

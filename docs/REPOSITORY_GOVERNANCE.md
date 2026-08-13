# Repository Governance

This document defines the merge/release governance contract for `ai-companion`.
GitHub branch protection/rulesets enforce it; CI and this document do not substitute
for repository settings.

## Protected `main` target

Apply one branch rule/ruleset to `main` with these invariants:

- changes normally land through pull requests;
- require branches to be up to date before merge so required checks are evaluated
  against the current base / exact merge candidate;
- block force-push and branch deletion;
- require conversation resolution before merge;
- do not expose the self-hosted `esp32s3-hil` runner to automatic public/fork PR code;
- do not require physical HIL on every PR;
- administrators may use an emergency bypass only with an auditable follow-up as
  described below.

For this single-maintainer repository, do not require an impossible self-approval.
Required hosted CI plus explicit PR review/readiness is the merge gate. If independent
maintainers are added later, increase required approving reviews without weakening
required checks.

## Required hosted checks

The stable always-on checks currently suitable for protected-main requirements are:

- `e2e`
- `Evidence truth gate`
- `Go reproducibility, vet, race, vulnerabilities`
- `CodeQL Go`

`Dependency review` is required only after GitHub Dependency Graph is enabled and the
check is proven available. The workflow intentionally reports `UNAVAILABLE`, not fake
`PASS`, when the repository feature is disabled or unsupported.

Path-specific/specialist gates (for example protocol/firmware compile and future
software-device/Wokwi gates) remain required by their owning issue/PR acceptance
criteria even when GitHub cannot express every conditional gate as one global branch
protection context.

## Stacked PRs

For a stack such as `#14 -> #16`:

1. Review and merge the lower/base PR first.
2. Rebase or retarget the upper PR onto the updated `main`.
3. Require a fresh exact-head CI run against the current base.
4. Never treat a green run from the pre-merge stacked base as sufficient promotion
   evidence for the retargeted PR.

## Backlog label reconciliation

GitHub-native issue `blocked_by` dependencies are the source of truth. The
`dependency-label-reconciler` workflow only maintains `status:ready` / `status:blocked`
as a dispatcher cache. It runs on issue close/reopen, on a schedule, and manually.

A claimed `status:in-progress` issue is never silently rewritten by the reconciler.
If dependency edges are edited while work is in progress, the owner must re-run the
issue's blocker preflight before continuing.

## Trusted physical HIL

`.github/workflows/firmware_hil.yml` must remain `workflow_dispatch` only on the
self-hosted `esp32s3-hil` runner. Public/fork PR triggers must never be added. Physical
evidence records the tested commit/ref, toolchain and DUT identity and is separate from
hosted CI evidence.

## Release/checkpoint rule

A production tag/release must:

- point to a commit reachable from `main`;
- record the exact source SHA;
- verify the stable hosted checks for that SHA before building/attesting artifacts;
- preserve evidence truth: missing provider/physical/security gates remain unproven.

The release workflow enforces the reachable-from-main and required-check preflight.

## Emergency admin bypass

Use only to recover from a repository-governance failure, never to skip an inconvenient
product gate. Record in an issue or PR:

- actor;
- reason;
- affected SHA/ref;
- which normal rule/check was bypassed.

Immediately run/re-run all normally required hosted checks on the resulting `main` SHA
and reconcile any failed or stale evidence before tagging/releasing further work.

## Administration step

The branch/ruleset settings themselves require GitHub repository Administration
permission. Automation/connectors without that permission must report the setting as
`UNPROVEN/NOT APPLIED`; they must not infer protection from CI files alone.

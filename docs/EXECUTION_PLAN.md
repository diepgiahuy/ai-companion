# Historical execution plan

> **Retired as a live scheduler.** This file previously copied issue status, dependency order and agent-pick rules into Markdown. That model drifted as issues/PRs merged and is no longer used for current work selection.

## Current execution sources

Use one truth per concern:

- **Requirement / acceptance:** GitHub Issue.
- **Live ready/blocked/in-progress state:** GitHub issue metadata and repository-native dependency state where configured.
- **Implementation under review:** PR diff at current head.
- **Execution explanation:** PR description.
- **Hosted proof:** GitHub Checks/artifacts.
- **Merged product truth:** `main` code/schema/config.
- **Implementation workflow / delegation / risk rules:** [`../ai_development_workflow.md`](../ai_development_workflow.md).
- **Evidence promotion boundaries:** [`TEST_EVIDENCE_LADDER.md`](TEST_EVIDENCE_LADDER.md).

Do not rebuild a hand-copied DAG, open-PR list, current branch table or dispatcher queue in this file.

## Why this was retired

The old plan embedded issue numbers, statuses, branches and prerequisites that changed faster than the document. That created duplicate context, stale agent decisions and unnecessary coordination/reconstruction work.

The replacement operating model uses:

```text
Issue
  -> one small coherent execution lane
  -> PR execution record
  -> direct oracle + risk-matched hosted checks
  -> merge to main
```

Default execution is one accountable lead. Parallel implementation is used only when shared contracts are stable, primary ownership does not overlap, each lane has an independent oracle, and no lane depends on another lane's unmerged output.

## Historical content

The previous detailed dispatcher/DAG remains available through Git history for forensic context. It must not be interpreted as current backlog or architecture truth.

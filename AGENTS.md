# Repository guidance for coding agents

This file contains durable repository invariants for any coding agent. It is not the
implementation workflow and it is not a live project-status document.

## Source-of-truth precedence

Use one source of truth per concern:

1. `main` code and merged schema/config are product truth.
2. GitHub Issues define remaining requirements and acceptance criteria.
3. Pull-request diffs are the implementation under review; the PR description is the
   canonical execution record for that change.
4. GitHub Checks/artifacts are hosted automated proof for the tested PR head.
5. `evidence/status.json` records only promoted evidence claims, not routine change
   history or live branch status.
6. `README.md`, architecture docs, and ADRs document durable merged behavior and
   decisions. They must not duplicate live PR/branch queues.

If prose conflicts with current `main`, inspect the merged implementation and recent
PR evidence before changing code or status claims. An issue can become stale: use it for
the requested outcome/acceptance, not as an unquestioned snapshot of current code or
live dependency status.

## No silent assumptions

Before relying on a material fact, verify it from the most authoritative available
source. Material facts include current files/symbols, schema/API shape, issue blockers,
CI/test state, dependency/provider behavior, security/authorization boundaries, and
hardware capabilities.

Use this verification order:

1. current repository/GitHub state for facts about this project;
2. current primary/official source for external APIs, frameworks, protocols, security,
   hardware, provider behavior, or version-sensitive claims;
3. established current repository pattern when the official source does not decide a
   project-specific design question;
4. common industry practice only as a candidate approach, never as repository truth.

If a correctness-, security-, data-integrity-, migration-, concurrency-, or product-
semantic fact remains unknown after those checks, mark it unknown and run a focused
spike or request a decision instead of silently guessing. Common practice is not a
substitute for a verifiable fact.

## Start with repository truth

- Inspect only the source, tests, config, issue dependencies, and recent changes that
  can affect the requested outcome. Do not audit the entire repository by default.
- Verify referenced paths, commands, APIs, dependencies, and hardware before relying
  on them. Mark a new path explicitly when creating one.
- Separate requirement, verified fact, implementation decision, and benchmark/spike
  hypothesis.
- Never describe planned infrastructure, providers, CI, simulation, or hardware proof
  as active until it exists and has relevant evidence.
- Prefer primary documentation for time-sensitive framework, protocol, security, and
  hardware decisions.

## Repository map

- `backend/`: Go modular monolith and provider adapters.
- `backend/internal/domain/`: domain types and ports.
- `backend/internal/pgstore/`: authoritative PostgreSQL persistence and transactional
  outbox.
- `backend/internal/store/`: SQLite only for explicit migration/recovery tooling and
  isolated tests.
- `backend/internal/controlplane/`: device twins, feature/config/privacy/identity/OTA
  control-plane code.
- `components/companion_app/`: hardware-independent firmware application logic.
- `components/esp32_board/`: ESP32-S3 hardware adapters.
- `main/`: ESP-IDF composition root.
- `host/`: host simulation and deterministic firmware-core tests.
- `tests/firmware/`: trusted physical HIL tests.
- `wokwi/`: simulator configuration.
- `ai_development_workflow.md`: canonical implementation, review, delegation, and
  verification lifecycle.
- `docs/architecture/AI_COMPANION_RESET_EXECUTION_PLANS_V2_CANONICAL_2026-08-17.md`:
  sole Markdown execution/status ledger for the architecture-reset/release sequence.
- `.agents/rules/github_issue_generation.md`: issue-type and executable-issue contract.
- `docs/TEST_EVIDENCE_LADDER.md`: evidence classification and promotion boundaries.

## Architecture invariants

- PostgreSQL is the sole product source of truth. Do not add a SQLite/PostgreSQL
  product selector, fallback, shadow read, or dual write.
- Domain state stays behind typed Go ports. The LLM, semantic memory, feature metadata,
  and device UI are not authoritative databases.
- Mutations that require atomic durable state plus event delivery use the existing
  transactional outbox boundary.
- Firmware consumes explicit backend protocol/device-twin contracts. It does not call
  an LLM, semantic router, or MCP server directly.
- `FeatureModule` is metadata and must not load arbitrary executable code.
- External integrations may use MCP behind backend policy. Internal product commands
  do not require an MCP round trip.
- Privacy, authorization, entitlement, rollout, and remote configuration remain
  separate concerns.

## Safety and human gates

- Planning/review/research requests are read-only unless implementation is explicitly
  authorized.
- For authorized implementation, first revalidate the issue against exact current
  `main`, live blockers, relevant code, and any version-sensitive external facts.
- Non-material drift may change the implementation approach but must not silently
  change the requested output or acceptance criteria.
- Material drift, conflicting product intent, or an unresolved material fact requires
  a spike/decision/replan before code changes continue.
- For valid implementation work, make the in-scope reversible changes and run the
  narrowest relevant non-destructive oracle without asking first.
- Require explicit human authorization for purchases, production migrations,
  deployment, irreversible eFuse/secure-boot operations, secret rotation, destructive
  data actions, or material scope expansion.
- Never claim a command, check, provider, simulator, or physical test passed unless it
  actually ran against the identified code and the result is available.
- Public PR code must never execute automatically on the personal HIL runner.

## Context and execution management

Keep agent context small without creating a second status system.

- Start from the owning GitHub Issue and exact current `main`.
- Load only the source, tests, ADR, and external references needed for that issue.
- Use the canonical execution ledger only when the task belongs to the architecture-reset/release sequence.
- Do not create or update derived `docs/plans/PHASE_*.md` status snapshots. Those files are retired.
- Put implementation-specific progress and evidence in the current PR description, not in a parallel Markdown checklist.
- Keep one small coherent execution lane by default. Follow `ai_development_workflow.md` for risk, delegation, review, and proof.
- During active development, do not keep permanent backward-compatibility wrappers or dual legacy/new internal paths. Delete the replaced internal path after the cutover is proven.

## Canonical pointers

For implementation lifecycle, issue revalidation, risk classification, delegation,
independent final review, PR execution records, verification, and definition of done,
follow `ai_development_workflow.md`.

For issue types, strict output/acceptance/scenario specification, and clean splitting,
follow `.agents/rules/github_issue_generation.md`.

For engineering operating philosophy, invariants, and quality gates, follow
`.agents/rules/senior_engineering_invariants.md`.

For what host tests, software-device Tier 1, Wokwi, and physical HIL can prove, follow
`docs/TEST_EVIDENCE_LADDER.md`.

Do not duplicate those procedures into vendor-specific `.codex`, `.claude`, `.gemini`,
nested `AGENTS.md`, ad-hoc workflow frameworks, or new Skills without measured repeated
need.

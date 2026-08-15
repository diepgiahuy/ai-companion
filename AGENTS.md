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
PR evidence before changing code or status claims.

## Start with repository truth

- Inspect only the source, tests, config, issue dependencies, and recent changes that
  can affect the requested outcome. Do not audit the entire repository by default.
- Verify referenced paths, commands, APIs, dependencies, and hardware before relying
  on them. Mark a new path explicitly when creating one.
- Separate requirement, implementation decision, and benchmark/spike hypothesis.
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
- `.agents/rules/github_issue_generation.md`: ready-issue specification contract.
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
- For authorized implementation, make the in-scope reversible changes and run the
  narrowest relevant non-destructive oracle without asking first.
- Require explicit human authorization for purchases, production migrations,
  deployment, irreversible eFuse/secure-boot operations, secret rotation, destructive
  data actions, or material scope expansion.
- Never claim a command, check, provider, simulator, or physical test passed unless it
  actually ran against the identified code and the result is available.
- Public PR code must never execute automatically on the personal HIL runner.

## Canonical pointers

For implementation lifecycle, risk classification, delegation, review, PR execution
records, verification, and definition of done, follow `ai_development_workflow.md`.

For issue specification and clean splitting, follow
`.agents/rules/github_issue_generation.md`.

For what host tests, software-device Tier 1, Wokwi, and physical HIL can prove, follow
`docs/TEST_EVIDENCE_LADDER.md`.

Do not duplicate those procedures into vendor-specific `.codex`, `.claude`, `.gemini`,
nested `AGENTS.md`, or ad-hoc workflow files without measured need.

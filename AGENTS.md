# Repository guidance for coding agents

This file is the source of truth for agent behavior in this repository. Architecture
and roadmap documents describe product direction; they do not prove that a feature,
dependency, CI gate, or hardware setup exists.

## Start with repository truth

1. Inspect the relevant source, tests, configuration, and recent changes before
   proposing an implementation.
2. Verify every referenced path, command, dependency, and API. If a path does not
   exist, say whether creating it is part of the change.
3. Separate product requirements from implementation decisions and from hypotheses
   that still need a benchmark or spike.
4. Do not describe planned infrastructure as active. A check is required only after
   its workflow and configuration are present on the target branch.
5. Prefer primary documentation for time-sensitive framework, protocol, security,
   and hardware claims.

## Repository map

- `backend/`: Go modular monolith and provider adapters.
- `backend/internal/domain/`: domain types and ports.
- `backend/internal/store/`: current SQLite persistence and transactional outbox.
- `backend/internal/controlplane/`: device twins, feature metadata, config, privacy,
  identity, and OTA control-plane code.
- `components/companion_app/`: hardware-independent firmware application logic.
- `components/esp32_board/`: ESP32-S3 hardware adapters.
- `main/`: ESP-IDF composition root.
- `host/`: host simulation and deterministic firmware-core tests.
- `tests/firmware/`: physical HIL tests. These are not unit tests.
- `wokwi/`: simulator configuration.
- `docs/COMMERCIAL_ARCHITECTURE.md`: current architecture and evolution seams.
- `ai_development_workflow.md`: issue, PR, validation, and agent coordination flow.

## Working agreement

- For review, research, diagnosis, or planning requests, inspect and report without
  changing files unless the request also authorizes implementation.
- For implementation requests, make the in-scope changes and run the narrowest
  relevant non-destructive checks without asking first.
- Preserve existing architecture boundaries unless the issue explicitly changes
  them and records the trade-off.
- Do not rewrite unrelated user changes.
- Require confirmation before production migrations, irreversible hardware or eFuse
  operations, purchases, secret rotation, destructive data actions, deployment, or
  material scope expansion.
- Never claim a command, CI job, simulator, provider, or physical test passed unless
  it actually ran and its result is available.

## Architecture boundaries

- SQLite is the current POC source of truth. Postgres and other production
  replacements remain adapters or roadmap items until implemented.
- Domain state stays behind typed Go ports. The LLM, semantic memory, feature
  metadata, and device UI are not authoritative databases.
- State mutations that emit durable events use the existing transactional outbox
  when atomic state plus event delivery is required.
- Firmware consumes explicit backend protocol and device-twin contracts. It does not
  call an LLM, semantic router, or MCP server directly.
- `FeatureModule` is metadata. It must not load arbitrary executable code.
- External integrations may use MCP behind backend policy boundaries. Internal
  product commands do not require an MCP round trip.
- Privacy, authorization, feature rollout, entitlement, and remote configuration are
  separate concerns.

## Verification

Use the cheapest layer that can detect the failure, then add broader coverage where
the behavior crosses a real boundary.

- `make test`: repository checks and host/backend tests.
- `make e2e-offline`: deterministic offline end-to-end path.
- `make e2e`: local end-to-end path when its dependencies are available.
- `make test-container`: containerized integration path when Docker is available.
- `source "$HOME/esp/esp-idf/export.sh" && idf.py build`: ESP-IDF build on a
  configured host.
- Physical HIL runs only on trusted repository refs through a manually authorized
  workflow. Public pull-request code must not run automatically on a personal
  self-hosted runner.

Mocks and fakes are valid for pure logic, failure injection, and hardware/provider
ports. Use real SQLite or other implemented stores for persistence integration, and
real hardware for behavior that depends on RF, timing, audio, display, power, or
physical input. Do not substitute console commands for a physical HIL claim unless
the firmware deliberately exposes a documented test-control interface.

## Issues and agent delegation

A ready issue contains an outcome, current-state references, scope, non-goals,
decisions, open questions, observable acceptance criteria, test layers,
security/privacy impact, dependencies, owned paths, and rollback notes.

Use one accountable lead per issue. Parallelize only workstreams with stable
contracts and non-overlapping ownership. Separate worktrees or branches are
preferred; an integrator owns shared contracts, migrations, and merge order. PR size
is governed by reviewability and risk, not an arbitrary line count.

When research is needed, compare maintained options against repository fit,
security, resource limits, migration cost, and measured performance. "Newest" is not
an acceptance criterion.

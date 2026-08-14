# PostgreSQL Phase A

This directory is the versioned PostgreSQL schema contract for issue #20. It is intentionally stacked on the post-#27 durable-idempotency branch so the first PostgreSQL baseline does not encode obsolete global idempotency-key semantics.

## Current pins

- PostgreSQL: `18.4-bookworm` multi-platform index digest `sha256:1961f96e6029a02c3812d7cb329a3b03a3ac2bb067058dec17b0f5596aca9296`.
- Atlas Community: `1.2.0-community-distroless` multi-platform index digest `sha256:0527e6aaaf8078ca3398cac75209f8f43a13e96bc15b3dfdeffe7cef5451c96e`.

These are Phase-A reproducibility pins. Re-verify them before a later production cutover; do not silently replace them with floating `latest` tags.

## Boundary

Phase A adds schema/migration evidence only. It does **not** add a SQLite/PostgreSQL runtime selector, dual-write, or production database cutover. SQLite remains the only product repository until later #20 phases prove pgx repository parity and execute the hard cut.

The baseline maps stored RFC3339 text times to `timestamptz`, JSON documents to `jsonb`, integer flags to `boolean`, and keeps the post-#27 actor+operation+client-key idempotency primary key. Transactional outbox triggers are reproduced in PostgreSQL so later repository work cannot accidentally drop event atomicity.

The CI job creates a disposable PostgreSQL instance, generates the Atlas directory hash in a temporary copy, validates and applies the migration, checks default-deny privacy/idempotency/outbox invariants, and proves a second apply is a no-op. Once the first CI run prints the generated `atlas.sum`, commit that exact file so later edits are integrity-checked rather than regenerated silently.

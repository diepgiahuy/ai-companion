# PostgreSQL schema contract

This directory is the versioned PostgreSQL schema contract for issue #20. It is intentionally stacked on the post-#27 durable-idempotency branch so the first PostgreSQL baseline does not encode obsolete global idempotency-key semantics.

## Current pins

- PostgreSQL: `18.4-bookworm` multi-platform index digest `sha256:1961f96e6029a02c3812d7cb329a3b03a3ac2bb067058dec17b0f5596aca9296`.
- Atlas Community: `1.2.0-community-distroless` multi-platform index digest `sha256:0527e6aaaf8078ca3398cac75209f8f43a13e96bc15b3dfdeffe7cef5451c96e`.

These are reproducibility pins. Re-verify them before an upgrade; do not silently replace them with floating `latest` tags.

## Boundary

Atlas owns this schema and `companiond` verifies revision `20260815093000` before runtime initialization. PostgreSQL is the sole product repository. No SQLite/PostgreSQL runtime selector, fallback, shadow read, or dual write is allowed; SQLite remains only in explicit migration/recovery tooling and isolated tests.

After every Atlas apply, the migration owner runs `ops/postgres/configure_runtime_role.psql` to refresh the non-DDL application's grants for newly created tables/sequences. Migration-owner credentials are not valid `companiond` runtime credentials.

River owns the separate `river` schema. Bootstrap it after Atlas and before runtime-role grants:

```bash
cd backend
go run ./cmd/companion-river-migrate up --database-url "$MIGRATION_DATABASE_URL"
go run ./cmd/companion-river-migrate validate --database-url "$MIGRATION_DATABASE_URL"
```

The `up` action creates the schema only through the migration role and rejects an existing schema owned by another role. `validate` and `companiond` are read-only with respect to schema structure. Run the role-grant script after both Atlas and River migrations.

The baseline maps stored RFC3339 text times to `timestamptz`, JSON documents to `jsonb`, integer flags to `boolean`, and keeps the post-#27 actor+operation+client-key idempotency primary key. Transactional outbox triggers are reproduced in PostgreSQL so later repository work cannot accidentally drop event atomicity.

The CI job creates a disposable PostgreSQL instance, generates the Atlas directory hash in a temporary copy, validates and applies the migration, checks default-deny privacy/idempotency/outbox invariants, and proves a second apply is a no-op. Once the first CI run prints the generated `atlas.sum`, commit that exact file so later edits are integrity-checked rather than regenerated silently.

Voice-mail metadata participates in the canonical 25-table PostgreSQL/SQLite recovery parity. Ogg Opus bytes intentionally do not live in either relational database. Backup and restore must snapshot `COMPANION_VOICE_MAIL_DIR` consistently with the database and verify object checksum/size before promotion. Deleting an active object does not claim deletion from external filesystem snapshots or backups; those follow the backup retention policy.

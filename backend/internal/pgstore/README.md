# PostgreSQL product repository

This package is the sole product persistence adapter selected by `companiond`.
It must not create a SQLite/PostgreSQL selector, fallback, shadow read, or dual-write
path. Atlas owns schema changes; application startup only verifies the compiled
revision contract.

Current slice proves:

- bounded pgx pool policy with fail-closed TLS requirements for non-loopback
  PostgreSQL endpoints;
- actor + operation + client-key durable idempotency replay/conflict semantics;
- transaction-scoped advisory locking so concurrent first attempts cannot both
  execute the same domain mutation before the ledger row exists;
- exact Atlas revision and required table/outbox-trigger startup verification;
- real PostgreSQL 18.4 integration after the Atlas-owned schema is applied.

Repository implementations stay behind existing Companion domain ports. SQLite is
available only to `companion-migrate` for explicit import/export/recovery and to
isolated tests. Rollback uses the tested export/restore procedure, never product
dual-write.

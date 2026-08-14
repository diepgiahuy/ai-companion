# PostgreSQL Phase B repository evidence

This package is migration/cutover evidence for issue #20. It is not selected by
`companiond` yet and must not create a long-lived SQLite/PostgreSQL selector or
dual-write path.

Current slice proves:

- bounded pgx pool policy with fail-closed TLS requirements for non-loopback
  PostgreSQL endpoints;
- actor + operation + client-key durable idempotency replay/conflict semantics;
- transaction-scoped advisory locking so concurrent first attempts cannot both
  execute the same domain mutation before the ledger row exists;
- real PostgreSQL 18.4 integration after the Atlas-owned schema is applied.

Later Phase-B repository ports must stay behind existing Companion domain ports.
Phase C switches the product composition only after full offline parity, backup /
restore evidence, and Tier-1 application scenarios are proven. The cutover must
remove SQLite product reads/writes in the same reviewed sequence; permanent
shadow-read or dual-write is not an accepted rollback strategy.

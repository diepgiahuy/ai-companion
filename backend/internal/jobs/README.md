# Durable jobs

River v0.43.0 owns durable background execution after the PostgreSQL hard cut. The first migrated category is privacy retention cleanup.

## Runtime contract

- Schema: `river`, migrated only by `companion-river-migrate` with the migration role.
- Queue: `maintenance`, one worker, five attempts, River default exponential retry.
- Schedule: every 6 hours by default and once at startup unless disabled.
- Uniqueness: one retention job per configured schedule period.
- Time bounds: 10-minute job timeout, 20-minute stuck-job rescue threshold, 8-second soft-stop timeout.
- Privileges: runtime receives only schema usage plus table/sequence DML. River reindexing is disabled so the application role never needs object ownership or DDL.
- Shutdown: the supervisor treats River as critical and drains/cancels workers before the shared pgx pool closes.

Configuration is exposed through `COMPANION_RIVER_RETENTION_INTERVAL`, `COMPANION_RIVER_JOB_TIMEOUT`, `COMPANION_RIVER_RESCUE_AFTER`, `COMPANION_RIVER_SOFT_STOP_TIMEOUT`, and `COMPANION_RIVER_RUN_ON_START`.

With `COMPANION_ADMIN_TOKEN` configured, operators can enqueue retention with `POST /v1/admin/jobs/retention` and read bounded process-lifetime counters from `GET /v1/admin/jobs/metrics` using `Authorization: Bearer <token>`.

## Evidence

The PostgreSQL workflow runs against the non-DDL application role and proves transaction rollback/commit visibility, uniqueness, retry, simulated stuck-job restart rescue, context cancellation, and graceful stop. It uploads exact-head migration, test, role, database-size, queue-state, restore, and rollback artifacts. Local tests without `COMPANION_RIVER_TEST_DSN` skip only the real-PostgreSQL integration cases.

#!/usr/bin/env python3
import json
import sqlite3
import sys
from pathlib import Path


def fail(message: str) -> None:
    raise SystemExit(message)


def main() -> None:
    if len(sys.argv) != 3:
        fail("usage: verify_idempotency_db.py <db> <out-json>")
    db_path = Path(sys.argv[1])
    out_path = Path(sys.argv[2])
    con = sqlite3.connect(db_path)
    try:
        rows = con.execute(
            "SELECT amount_vnd, description, COUNT(*) FROM expenses "
            "WHERE description IN ('tier1 idempotency original','tier1 idempotency concurrent') "
            "GROUP BY amount_vnd, description ORDER BY amount_vnd"
        ).fetchall()
        expected = [(77000, "tier1 idempotency original", 1), (99000, "tier1 idempotency concurrent", 1)]
        if rows != expected:
            fail(f"unexpected committed idempotency expenses: {rows!r}; want {expected!r}")
        conflict_count = con.execute("SELECT COUNT(*) FROM expenses WHERE amount_vnd=88000").fetchone()[0]
        if conflict_count != 0:
            fail(f"conflicting retry mutated {conflict_count} expense row(s)")
        ledger = con.execute(
            "SELECT operation, COUNT(*), COUNT(DISTINCT actor_id), COUNT(DISTINCT idempotency_key) "
            "FROM idempotency_records WHERE operation='expense.log' GROUP BY operation"
        ).fetchone()
        if ledger is None or ledger[1:] != (2, 1, 2):
            fail(f"unexpected expense.log ledger summary: {ledger!r}; want 2 records, 1 actor, 2 keys")
        reservations = con.execute("SELECT COUNT(*) FROM legacy_idempotency_reservations").fetchone()[0]
        payload = {
            "schema_version": 1,
            "evidence_class": "tier1_orchestration",
            "result": "passed",
            "contract": "durable_actor_operation_key_request_hash",
            "equivalent_restart_replay_rows": 1,
            "conflicting_retry_mutations": conflict_count,
            "concurrent_equivalent_rows": 1,
            "expense_log_ledger_records": ledger[1],
            "expense_log_actor_count": ledger[2],
            "expense_log_key_count": ledger[3],
            "legacy_reservations": reservations,
            "promotion": "orchestration_only",
        }
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
        print("PASS durable idempotency DB: restart replay, conflict rejection, concurrent at-most-once")
    finally:
        con.close()


if __name__ == "__main__":
    main()

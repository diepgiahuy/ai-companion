#!/usr/bin/env python3
import json
import sqlite3
import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit("usage: verify_tool_db.py DB OUTPUT_JSON")

db = Path(sys.argv[1])
out = Path(sys.argv[2])
connection = sqlite3.connect(db)
rows = connection.execute(
    "SELECT idempotency_key,user_id,amount_vnd,category,description,occurred_at FROM expenses ORDER BY id"
).fetchall()
connection.close()
if len(rows) != 1:
    raise SystemExit(f"authoritative mutation count = {len(rows)}, want exactly 1")
key, user_id, amount, category, description, occurred_at = rows[0]
if amount != 50000 or category != "food" or description != "tier1 deterministic expense":
    raise SystemExit(f"unexpected authoritative mutation: {rows[0]!r}")
result = {
    "schema_version": 1,
    "evidence_class": "tier1_orchestration",
    "promotion": "orchestration_only",
    "result": "passed",
    "mutation": {
        "table": "expenses",
        "count": 1,
        "amount_vnd": amount,
        "category": category,
        "description": description,
        "occurred_at": occurred_at,
        "user_id": user_id,
        "idempotency_key_present": bool(key),
    },
}
out.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print("TOOL MUTATION EVIDENCE PASS: exactly one authoritative expense mutation")

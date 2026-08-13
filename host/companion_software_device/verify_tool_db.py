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

expenses = connection.execute(
    "SELECT idempotency_key,user_id,amount_vnd,category,description,occurred_at FROM expenses ORDER BY id"
).fetchall()
budgets = connection.execute(
    "SELECT user_id,period,limit_vnd FROM budgets ORDER BY period"
).fetchall()
notes = connection.execute(
    "SELECT idempotency_key,user_id,content FROM notes ORDER BY id"
).fetchall()
journal = connection.execute(
    "SELECT idempotency_key,user_id,content,occurred_at FROM journal_entries ORDER BY id"
).fetchall()
scheduled = connection.execute(
    "SELECT idempotency_key,user_id,device_id,kind,title,fire_at,status FROM reminders ORDER BY id"
).fetchall()
memories = connection.execute(
    "SELECT user_id,memory_key,kind,value,valid_from,source,deleted_at FROM memories ORDER BY id"
).fetchall()
connection.close()

if len(expenses) != 1:
    raise SystemExit(f"expense mutation count = {len(expenses)}, want exactly 1")
expense_key, expense_user, amount, category, description, occurred_at = expenses[0]
if amount != 50000 or category != "food" or description != "tier1 deterministic expense":
    raise SystemExit(f"unexpected expense mutation: {expenses[0]!r}")
if expense_user != "default" or not expense_key:
    raise SystemExit(f"expense ownership/idempotency mismatch: {expenses[0]!r}")

if budgets != [("default", "weekly", 1000000)]:
    raise SystemExit(f"unexpected budget parity state: {budgets!r}")

if len(notes) != 1 or notes[0][1:] != ("default", "tier1 note") or not notes[0][0]:
    raise SystemExit(f"unexpected note parity state: {notes!r}")

if len(journal) != 1 or journal[0][1] != "default" or journal[0][2] != "tier1 journal" or not journal[0][0]:
    raise SystemExit(f"unexpected journal parity state: {journal!r}")

if len(scheduled) != 2:
    raise SystemExit(f"scheduled mutation count = {len(scheduled)}, want reminder+timer")
by_kind = {row[3]: row for row in scheduled}
if set(by_kind) != {"reminder", "timer"}:
    raise SystemExit(f"scheduled kinds = {set(by_kind)!r}, want reminder+timer")
for kind, title in (("reminder", "tier1 reminder"), ("timer", "tier1 timer")):
    row = by_kind[kind]
    key, user_id, device_id, _, actual_title, fire_at, status = row
    if not key or user_id != "default" or device_id != "software-device-tool":
        raise SystemExit(f"{kind} ownership/idempotency mismatch: {row!r}")
    if actual_title != title or not fire_at or status not in {"pending", "active"}:
        raise SystemExit(f"unexpected {kind} state: {row!r}")

current_memories = [row for row in memories if row[6] is None]
if len(current_memories) != 1:
    raise SystemExit(f"current memory count = {len(current_memories)}, want exactly 1")
memory_row = current_memories[0]
if memory_row[0] != "default" or memory_row[1] != "preferred_language" or memory_row[2] != "semantic" or memory_row[3] != "Vietnamese" or memory_row[5] != "user_explicit":
    raise SystemExit(f"unexpected memory parity state: {memory_row!r}")

result = {
    "schema_version": 2,
    "evidence_class": "tier1_orchestration",
    "promotion": "orchestration_only",
    "result": "passed",
    # Backward-compatible expense summary retained for older artifact readers.
    "mutation": {
        "table": "expenses",
        "count": 1,
        "amount_vnd": amount,
        "category": category,
        "description": description,
        "occurred_at": occurred_at,
        "user_id": expense_user,
        "idempotency_key_present": bool(expense_key),
    },
    "representative_parity": {
        "expense": {"count": 1, "amount_vnd": amount, "idempotency_key_present": True},
        "budget": {"period": "weekly", "limit_vnd": 1000000},
        "note": {"count": 1, "content": "tier1 note", "idempotency_key_present": True},
        "journal": {"count": 1, "content": "tier1 journal", "idempotency_key_present": True},
        "reminder": {"count": 1, "title": "tier1 reminder", "device_id": "software-device-tool", "idempotency_key_present": True},
        "timer": {"count": 1, "title": "tier1 timer", "device_id": "software-device-tool", "idempotency_key_present": True},
        "memory": {"count": 1, "key": "preferred_language", "kind": "semantic", "value": "Vietnamese"},
    },
}
out.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print("TOOL PARITY EVIDENCE PASS: expense/budget/note/journal/reminder/timer/memory authoritative state verified")

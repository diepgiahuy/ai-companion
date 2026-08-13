#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def replace(path, old, new):
    p = ROOT / path
    text = p.read_text(encoding="utf-8")
    if text.count(old) != 1:
        raise SystemExit(f"{path} legacy compatibility target drifted: {old[:100]!r}")
    p.write_text(text.replace(old, new), encoding="utf-8")

replace("backend/internal/store/store.go",
'''\t_, err := s.db.ExecContext(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES(?,?,?,?)`, key, owner(userID), content, time.Now().UTC().Format(time.RFC3339Nano))\n''',
'''\tuser := owner(userID)\n\t_, err := s.db.ExecContext(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at) SELECT ?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM notes WHERE user_id=? AND idempotency_key=?)`, key, user, content, time.Now().UTC().Format(time.RFC3339Nano), user, key)\n''')
replace("backend/internal/store/store.go",
'''\t\tif _, err := tx.ExecContext(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES(?,?,?,?,?,?,?)`, k, owner(userID), x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC().Format(time.RFC3339Nano), now); err != nil {\n''',
'''\t\tuser := owner(userID)\n\t\tif _, err := tx.ExecContext(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM expenses WHERE user_id=? AND idempotency_key=?)`, k, user, x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC().Format(time.RFC3339Nano), now, user, k); err != nil {\n''')
replace("backend/internal/store/store.go",
'''\t_, err := s.db.ExecContext(ctx, `INSERT INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at) VALUES(?,?,?,?,?)`, key, owner(userID), content, occurredAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))\n''',
'''\tuser := owner(userID)\n\t_, err := s.db.ExecContext(ctx, `INSERT INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at) SELECT ?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM journal_entries WHERE user_id=? AND idempotency_key=?)`, key, user, content, occurredAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), user, key)\n''')
replace("backend/internal/store/store.go",
'''\t_, err := s.db.ExecContext(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES(?,?,?,?,?,?,'pending',0,'',0,?)`, key, owner(userID), strings.TrimSpace(deviceID), kind, title, fireAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))\n''',
'''\tuser := owner(userID)\n\t_, err := s.db.ExecContext(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) SELECT ?,?,?,?,?,?,'pending',0,'',0,? WHERE NOT EXISTS (SELECT 1 FROM reminders WHERE user_id=? AND idempotency_key=?)`, key, user, strings.TrimSpace(deviceID), kind, title, fireAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), user, key)\n''')
replace("backend/internal/store/store.go",
'''\t_, err := s.db.ExecContext(ctx, `INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES(?,?,?,?,?,?,?)`, key, owner(userID), strings.TrimSpace(deviceID), path, strings.TrimSpace(transcript), durationMS, time.Now().UTC().Format(time.RFC3339Nano))\n''',
'''\tuser := owner(userID)\n\t_, err := s.db.ExecContext(ctx, `INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM voice_memos WHERE user_id=? AND idempotency_key=?)`, key, user, strings.TrimSpace(deviceID), path, strings.TrimSpace(transcript), durationMS, time.Now().UTC().Format(time.RFC3339Nano), user, key)\n''')
replace("backend/internal/store/platform.go",
'''\tresult, e := s.db.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, key, user, device, provider, symbol, currency, operator, threshold, now.Format(time.RFC3339Nano))\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n\tid, e := result.LastInsertId()\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n''',
'''\tresult, e := s.db.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) SELECT ?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM market_watches WHERE user_id=? AND idempotency_key=?)`, key, user, device, provider, symbol, currency, operator, threshold, now.Format(time.RFC3339Nano), user, key)\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n\tid, e := result.LastInsertId()\n\tif e != nil || id == 0 {\n\t\te = s.db.QueryRowContext(ctx, `SELECT id FROM market_watches WHERE user_id=? AND idempotency_key=? ORDER BY id LIMIT 1`, user, key).Scan(&id)\n\t\tif e != nil {\n\t\t\treturn market.Watch{}, e\n\t\t}\n\t}\n''')
print("legacy scoped create compatibility applied")

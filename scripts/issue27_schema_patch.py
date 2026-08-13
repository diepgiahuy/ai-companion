#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

store = ROOT / "backend/internal/store/store.go"
text = store.read_text(encoding="utf-8")
needle = "idempotency_key TEXT NOT NULL UNIQUE"
if text.count(needle) != 5:
    raise SystemExit(f"store.go expected 5 legacy global UNIQUE definitions, found {text.count(needle)}")
text = text.replace(needle, "idempotency_key TEXT NOT NULL")
old = '''\tif err := s.migratePlatform(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}'''
new = '''\tif err := s.migrateIdempotency(); err != nil {\n\t\treturn err\n\t}\n\tif err := s.migratePlatform(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}'''
if text.count(old) != 1:
    raise SystemExit(f"store.go migration hook expected once, found {text.count(old)}")
store.write_text(text.replace(old, new), encoding="utf-8")

idem = ROOT / "backend/internal/store/idempotency.go"
text = idem.read_text(encoding="utf-8")
old = '''func (s *Store) migrateIdempotency() error {\n\tif _, err := s.db.Exec(idempotencyLedgerDDL); err != nil {\n\t\treturn fmt.Errorf("idempotency ledger migration: %w", err)\n\t}\n\treturn nil\n}'''
new = '''func (s *Store) migrateIdempotency() error {\n\tif _, err := s.db.Exec(idempotencyLedgerDDL); err != nil {\n\t\treturn fmt.Errorf("idempotency ledger migration: %w", err)\n\t}\n\treturn s.migrateLegacyIdempotencyUniqueness()\n}'''
if text.count(old) != 1:
    raise SystemExit("idempotency.go migration function drifted")
idem.write_text(text.replace(old, new), encoding="utf-8")

platform = ROOT / "backend/internal/store/platform.go"
text = platform.read_text(encoding="utf-8")
if text.count(needle) != 1:
    raise SystemExit(f"platform.go expected 1 legacy global UNIQUE definition, found {text.count(needle)}")
text = text.replace(needle, "idempotency_key TEXT NOT NULL")
old = '''\tfor _, q := range stmts {\n\t\tif _, err := s.db.Exec(q); err != nil {\n\t\t\treturn fmt.Errorf("platform migration: %w", err)\n\t\t}\n\t}\n\tfor _, c := range []struct{ table, column, alter string }{'''
new = '''\tfor _, q := range stmts {\n\t\tif _, err := s.db.Exec(q); err != nil {\n\t\t\treturn fmt.Errorf("platform migration: %w", err)\n\t\t}\n\t}\n\tif err := s.migrateMarketWatchIdempotencyUniqueness(); err != nil {\n\t\treturn err\n\t}\n\tfor _, c := range []struct{ table, column, alter string }{'''
if text.count(old) != 1:
    raise SystemExit("platform.go migration insertion point drifted")
platform.write_text(text.replace(old, new), encoding="utf-8")

(ROOT / "backend/internal/store/idempotency_schema_test.go").write_text(r'''package store

import (
    "database/sql"
    "path/filepath"
    "testing"

    _ "modernc.org/sqlite"
)

func TestLegacyGlobalIdempotencyMigrationPreservesRowsAndAllowsActorReuse(t *testing.T) {
    path := filepath.Join(t.TempDir(), "legacy.db")
    db, err := sql.Open("sqlite", path)
    if err != nil { t.Fatal(err) }
    for _, statement := range []string{
        `CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,created_at TEXT NOT NULL)`,
        `INSERT INTO notes(id,idempotency_key,user_id,content,created_at) VALUES(7,'shared-key','actor-a','keep me','2026-01-01T00:00:00Z')`,
    } {
        if _, err := db.Exec(statement); err != nil { db.Close(); t.Fatal(err) }
    }
    if err := db.Close(); err != nil { t.Fatal(err) }

    s, err := Open(path)
    if err != nil { t.Fatal(err) }
    defer s.Close()
    var id int64
    var key, user, content string
    if err := s.db.QueryRow(`SELECT id,idempotency_key,user_id,content FROM notes WHERE id=7`).Scan(&id, &key, &user, &content); err != nil { t.Fatal(err) }
    if id != 7 || key != "shared-key" || user != "actor-a" || content != "keep me" { t.Fatalf("legacy row changed: id=%d key=%q user=%q content=%q", id, key, user, content) }
    global, err := hasSingleColumnUniqueIndex(s.db, "notes", "idempotency_key")
    if err != nil { t.Fatal(err) }
    if global { t.Fatal("legacy global idempotency uniqueness still exists") }
    if _, err := s.db.Exec(`INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES('shared-key','actor-b','allowed','2026-01-02T00:00:00Z')`); err != nil { t.Fatalf("different actor could not reuse old client key: %v", err) }
}

func TestFreshSchemasHaveNoGlobalIdempotencyKeyUniqueness(t *testing.T) {
    s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
    if err != nil { t.Fatal(err) }
    defer s.Close()
    for _, table := range []string{"notes", "expenses", "journal_entries", "reminders", "voice_memos", "market_watches"} {
        global, err := hasSingleColumnUniqueIndex(s.db, table, "idempotency_key")
        if err != nil { t.Fatal(err) }
        if global { t.Fatalf("%s still has global idempotency uniqueness", table) }
    }
    var ledger string
    if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='idempotency_records'`).Scan(&ledger); err != nil { t.Fatal(err) }
}
''', encoding="utf-8")
print("issue27 production idempotency schema patch applied")

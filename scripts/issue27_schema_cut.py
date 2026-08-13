#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def replace(path, old, new, count=1):
    p = ROOT / path
    text = p.read_text(encoding="utf-8")
    actual = text.count(old)
    if actual != count:
        raise SystemExit(f"{path} drift: expected {count}, found {actual}: {old[:120]!r}")
    p.write_text(text.replace(old, new), encoding="utf-8")

# Fresh schemas no longer encode global idempotency. Durable semantics live in
# the actor+operation ledger at the mutation boundary.
for table in ("notes", "expenses", "journal_entries", "reminders", "voice_memos"):
    replace("backend/internal/store/store.go", "idempotency_key TEXT NOT NULL UNIQUE", "idempotency_key TEXT NOT NULL", 1)
replace("backend/internal/store/platform.go", "idempotency_key TEXT NOT NULL UNIQUE", "idempotency_key TEXT NOT NULL", 1)

# Legacy/internal create helpers must not pretend INSERT OR IGNORE is durable
# idempotency after the unique constraint disappears.
for sql in (
    "INSERT OR IGNORE INTO notes",
    "INSERT OR IGNORE INTO expenses",
    "INSERT OR IGNORE INTO journal_entries",
    "INSERT OR IGNORE INTO reminders",
    "INSERT OR IGNORE INTO voice_memos",
):
    replace("backend/internal/store/store.go", sql, sql.replace("INSERT OR IGNORE", "INSERT"), 1)

old_market = '''\tnow := time.Now().UTC()\n\t_, e := s.db.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, key, user, device, provider, symbol, currency, operator, threshold, now.Format(time.RFC3339Nano))\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n\tvar w market.Watch\n\tvar en, last int\n\tvar created string\n\te = s.db.QueryRowContext(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE idempotency_key=?`, key).Scan(&w.ID, &w.UserID, &w.DeviceID, &w.Provider, &w.Symbol, &w.Currency, &w.Operator, &w.Threshold, &en, &last, &created)\n'''
new_market = '''\tnow := time.Now().UTC()\n\tresult, e := s.db.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, key, user, device, provider, symbol, currency, operator, threshold, now.Format(time.RFC3339Nano))\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n\tid, e := result.LastInsertId()\n\tif e != nil {\n\t\treturn market.Watch{}, e\n\t}\n\tvar w market.Watch\n\tvar en, last int\n\tvar created string\n\te = s.db.QueryRowContext(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE id=?`, id).Scan(&w.ID, &w.UserID, &w.DeviceID, &w.Provider, &w.Symbol, &w.Currency, &w.Operator, &w.Threshold, &en, &last, &created)\n'''
replace("backend/internal/store/platform.go", old_market, new_market)

# Production migration order: create ledger/reservations; reserve+rebuild old
# base tables before platform triggers are created; rebuild old market table
# after platform schema creation.
old_tail = '''\tfor _, target := range [][2]string{{"expenses", "occurred_at"}, {"journal_entries", "occurred_at"}, {"reminders", "fire_at"}} {\n\t\tif err := s.normalizeTimeColumn(target[0], target[1]); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tif err := s.migratePlatform(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n'''
new_tail = '''\tfor _, target := range [][2]string{{"expenses", "occurred_at"}, {"journal_entries", "occurred_at"}, {"reminders", "fire_at"}} {\n\t\tif err := s.normalizeTimeColumn(target[0], target[1]); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tif err := s.migrateIdempotency(); err != nil {\n\t\treturn err\n\t}\n\tif err := s.migrateLegacyIdempotencyUniqueness(); err != nil {\n\t\treturn err\n\t}\n\tif err := s.migratePlatform(); err != nil {\n\t\treturn err\n\t}\n\tif err := s.migrateMarketWatchIdempotencyUniqueness(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n'''
replace("backend/internal/store/store.go", old_tail, new_tail)

# Ledger also owns migration reservations for pre-ledger keys whose semantic
# request hash cannot be reconstructed safely.
p = ROOT / "backend/internal/store/idempotency.go"
p.write_text('''package store\n\nimport (\n\t"context"\n\t"database/sql"\n\t"errors"\n\t"fmt"\n\n\t"companion-server/internal/idempotency"\n)\n\nconst idempotencyLedgerDDL = `CREATE TABLE IF NOT EXISTS idempotency_records (\n    actor_id TEXT NOT NULL,\n    operation TEXT NOT NULL,\n    idempotency_key TEXT NOT NULL,\n    request_hash TEXT NOT NULL,\n    outcome_json TEXT NOT NULL,\n    created_at TEXT NOT NULL,\n    PRIMARY KEY(actor_id,operation,idempotency_key)\n)`\n\nconst legacyIdempotencyReservationsDDL = `CREATE TABLE IF NOT EXISTS legacy_idempotency_reservations (\n    operation TEXT NOT NULL,\n    idempotency_key TEXT NOT NULL,\n    source_table TEXT NOT NULL,\n    created_at TEXT NOT NULL,\n    PRIMARY KEY(operation,idempotency_key)\n)`\n\nfunc (s *Store) migrateIdempotency() error {\n\tif _, err := s.db.Exec(idempotencyLedgerDDL); err != nil {\n\t\treturn fmt.Errorf("idempotency ledger migration: %w", err)\n\t}\n\tif _, err := s.db.Exec(legacyIdempotencyReservationsDDL); err != nil {\n\t\treturn fmt.Errorf("legacy idempotency reservation migration: %w", err)\n\t}\n\treturn nil\n}\n\nfunc idempotencyRecord(ctx context.Context, tx *sql.Tx, request idempotency.Request) (hash, outcome string, found bool, err error) {\n\terr = tx.QueryRowContext(ctx, `SELECT request_hash,outcome_json FROM idempotency_records WHERE actor_id=? AND operation=? AND idempotency_key=?`, request.Actor, request.Operation, request.Key).Scan(&hash, &outcome)\n\tif errors.Is(err, sql.ErrNoRows) {\n\t\treturn "", "", false, nil\n\t}\n\tif err != nil {\n\t\treturn "", "", false, err\n\t}\n\treturn hash, outcome, true, nil\n}\n\nfunc legacyIdempotencyReserved(ctx context.Context, tx *sql.Tx, operation, key string) (bool, error) {\n\tvar marker int\n\terr := tx.QueryRowContext(ctx, `SELECT 1 FROM legacy_idempotency_reservations WHERE operation=? AND idempotency_key=?`, operation, key).Scan(&marker)\n\tif errors.Is(err, sql.ErrNoRows) {\n\t\treturn false, nil\n\t}\n\tif err != nil {\n\t\treturn false, err\n\t}\n\treturn true, nil\n}\n''', encoding="utf-8")

old_found = '''\tif found {\n\t\tif !idempotency.EqualHash(storedHash, request.RequestHash) {\n\t\t\treturn idempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}\n\t\t}\n\t\treturn idempotentOutcome{JSON: storedOutcome, Replayed: true}, nil\n\t}\n\tvalue, err := mutate(tx)\n'''
new_found = '''\tif found {\n\t\tif !idempotency.EqualHash(storedHash, request.RequestHash) {\n\t\t\treturn idempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}\n\t\t}\n\t\treturn idempotentOutcome{JSON: storedOutcome, Replayed: true}, nil\n\t}\n\treserved, err := legacyIdempotencyReserved(ctx, tx, request.Operation, request.Key)\n\tif err != nil {\n\t\treturn idempotentOutcome{}, err\n\t}\n\tif reserved {\n\t\treturn idempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}\n\t}\n\tvalue, err := mutate(tx)\n'''
replace("backend/internal/store/idempotency_run.go", old_found, new_found)

# Replace schema migration helper with reservation-aware version.
schema = ROOT / "backend/internal/store/idempotency_schema.go"
schema.write_text(r'''package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type idempotencyTableMigration struct {
	Table              string
	CreateSQL          string
	Columns            string
	IndexesSQL         []string
	LegacyOperations   []string
	ReserveBatchParent bool
}

var baseIdempotencyMigrations = []idempotencyTableMigration{
	{Table: "notes", CreateSQL: `CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,content,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_notes_user ON notes(user_id,id DESC)`}, LegacyOperations: []string{"note.create"}},
	{Table: "expenses", CreateSQL: `CREATE TABLE expenses (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',amount_vnd INTEGER NOT NULL CHECK(amount_vnd>0),category TEXT NOT NULL,description TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_expenses_user_time ON expenses(user_id,occurred_at)`}, LegacyOperations: []string{"expense.create", "expense.log"}, ReserveBatchParent: true},
	{Table: "journal_entries", CreateSQL: `CREATE TABLE journal_entries (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,content,occurred_at,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_journal_user_time ON journal_entries(user_id,occurred_at)`}, LegacyOperations: []string{"journal.create"}},
	{Table: "reminders", CreateSQL: `CREATE TABLE reminders (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL DEFAULT 'reminder',title TEXT NOT NULL,fire_at TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at TEXT NOT NULL DEFAULT '',paused_remaining_seconds INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders(status,next_attempt_at,fire_at)`, `CREATE INDEX IF NOT EXISTS idx_reminders_owner ON reminders(user_id,device_id,status,fire_at)`}, LegacyOperations: []string{"reminder.create", "timer.create"}},
	{Table: "voice_memos", CreateSQL: `CREATE TABLE voice_memos (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,transcript TEXT NOT NULL DEFAULT '',duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_voice_memos_owner ON voice_memos(user_id,device_id,id DESC)`}, LegacyOperations: []string{"voice_memo.save"}},
}

var marketWatchIdempotencyMigration = idempotencyTableMigration{
	Table: "market_watches", CreateSQL: `CREATE TABLE market_watches (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL,user_id TEXT NOT NULL,device_id TEXT NOT NULL,provider TEXT NOT NULL,symbol TEXT NOT NULL,currency TEXT NOT NULL,operator TEXT NOT NULL CHECK(operator IN ('<','<=','>','>=')),threshold REAL NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,last_state INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL)`, Columns: "id,idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at", IndexesSQL: []string{`CREATE INDEX IF NOT EXISTS idx_market_watches_enabled ON market_watches(enabled,provider,symbol)`}, LegacyOperations: []string{"market.watch.create"},
}

func (s *Store) migrateLegacyIdempotencyUniqueness() error {
	for _, migration := range baseIdempotencyMigrations {
		if err := s.rebuildGlobalIdempotencyTable(migration); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateMarketWatchIdempotencyUniqueness() error {
	return s.rebuildGlobalIdempotencyTable(marketWatchIdempotencyMigration)
}

func (s *Store) rebuildGlobalIdempotencyTable(migration idempotencyTableMigration) error {
	global, err := hasSingleColumnUniqueIndex(s.db, migration.Table, "idempotency_key")
	if err != nil {
		return fmt.Errorf("inspect %s idempotency uniqueness: %w", migration.Table, err)
	}
	if !global {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := reserveLegacyKeys(tx, migration); err != nil {
		return err
	}
	legacy := migration.Table + "__legacy_global_idempotency"
	if _, err := tx.Exec(`ALTER TABLE ` + migration.Table + ` RENAME TO ` + legacy); err != nil {
		return fmt.Errorf("rename %s for idempotency migration: %w", migration.Table, err)
	}
	if _, err := tx.Exec(migration.CreateSQL); err != nil {
		return fmt.Errorf("recreate %s without global idempotency uniqueness: %w", migration.Table, err)
	}
	copySQL := `INSERT INTO ` + migration.Table + `(` + migration.Columns + `) SELECT ` + migration.Columns + ` FROM ` + legacy
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy %s idempotency migration: %w", migration.Table, err)
	}
	if _, err := tx.Exec(`DROP TABLE ` + legacy); err != nil {
		return fmt.Errorf("drop legacy %s after idempotency migration: %w", migration.Table, err)
	}
	for _, indexSQL := range migration.IndexesSQL {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("recreate %s index after idempotency migration: %w", migration.Table, err)
		}
	}
	return tx.Commit()
}

func reserveLegacyKeys(tx *sql.Tx, migration idempotencyTableMigration) error {
	rows, err := tx.Query(`SELECT DISTINCT idempotency_key FROM ` + migration.Table + ` WHERE trim(idempotency_key)!=''`)
	if err != nil {
		return fmt.Errorf("load %s legacy idempotency keys: %w", migration.Table, err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, strings.TrimSpace(key))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	reserve := func(operation, key string) error {
		if key == "" {
			return nil
		}
		_, err := tx.Exec(`INSERT OR IGNORE INTO legacy_idempotency_reservations(operation,idempotency_key,source_table,created_at) VALUES(?,?,?,?)`, operation, key, migration.Table, now)
		return err
	}
	for _, key := range keys {
		for _, operation := range migration.LegacyOperations {
			if err := reserve(operation, key); err != nil {
				return fmt.Errorf("reserve %s legacy key: %w", migration.Table, err)
			}
		}
		if migration.ReserveBatchParent {
			if parent, ok := legacyBatchParent(key); ok {
				if err := reserve("expense.log", parent); err != nil {
					return fmt.Errorf("reserve expense legacy batch parent: %w", err)
				}
			}
		}
	}
	return nil
}

func legacyBatchParent(key string) (string, bool) {
	idx := strings.LastIndexByte(key, ':')
	if idx <= 0 || idx == len(key)-1 {
		return "", false
	}
	if _, err := strconv.ParseUint(key[idx+1:], 10, 32); err != nil {
		return "", false
	}
	return key[:idx], true
}

func hasSingleColumnUniqueIndex(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		return false, err
	}
	var uniqueIndexNames []string
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return false, err
		}
		if unique != 0 {
			uniqueIndexNames = append(uniqueIndexNames, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, name := range uniqueIndexNames {
		indexRows, err := db.Query(`PRAGMA index_info(` + name + `)`)
		if err != nil {
			return false, err
		}
		var columns []string
		for indexRows.Next() {
			var indexSeq, cid int
			var indexedColumn string
			if err := indexRows.Scan(&indexSeq, &cid, &indexedColumn); err != nil {
				indexRows.Close()
				return false, err
			}
			columns = append(columns, indexedColumn)
		}
		if err := indexRows.Err(); err != nil {
			indexRows.Close()
			return false, err
		}
		if err := indexRows.Close(); err != nil {
			return false, err
		}
		if len(columns) == 1 && columns[0] == column {
			return true, nil
		}
	}
	return false, nil
}
''', encoding="utf-8")

# Add migration/replay regression coverage.
test = ROOT / "backend/internal/store/idempotency_schema_cut_test.go"
test.write_text(r'''package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"companion-server/internal/idempotency"
)

func TestLegacyGlobalIdempotencyMigratesToReservations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil { t.Fatal(err) }
	_, err = db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,created_at TEXT NOT NULL)`)
	if err != nil { t.Fatal(err) }
	_, err = db.Exec(`INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES('legacy-note','legacy-user','old','2030-01-01T00:00:00Z')`)
	if err != nil { t.Fatal(err) }
	_, err = db.Exec(`CREATE TABLE expenses (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',amount_vnd INTEGER NOT NULL CHECK(amount_vnd>0),category TEXT NOT NULL,description TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`)
	if err != nil { t.Fatal(err) }
	_, err = db.Exec(`INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES('legacy-batch:0','legacy-user',10000,'food','old','2030-01-01T00:00:00Z','2030-01-01T00:00:00Z')`)
	if err != nil { t.Fatal(err) }
	if err := db.Close(); err != nil { t.Fatal(err) }

	s, err := Open(path)
	if err != nil { t.Fatal(err) }
	defer s.Close()
	for _, table := range []string{"notes", "expenses"} {
		unique, err := hasSingleColumnUniqueIndex(s.db, table, "idempotency_key")
		if err != nil { t.Fatal(err) }
		if unique { t.Fatalf("%s still has global idempotency uniqueness", table) }
	}
	notes, err := s.ListNotes(context.Background(), "legacy-user", 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "old" { t.Fatalf("legacy note not preserved: %+v %v", notes, err) }

	hash, _ := idempotency.HashValue(map[string]any{"content":"new"})
	err = s.CreateNoteMutation(context.Background(), idempotency.Request{Actor:"actor:new", Operation:"note.create", Key:"legacy-note", RequestHash:hash}, "new-user", "new")
	if !idempotency.IsConflict(err) { t.Fatalf("legacy note key should be reserved, got %v", err) }
	batchHash, _ := idempotency.HashValue([]any{map[string]any{"amount_vnd":10000}})
	err = s.CreateExpensesMutation(context.Background(), idempotency.Request{Actor:"actor:new", Operation:"expense.log", Key:"legacy-batch", RequestHash:batchHash}, "new-user", []ExpenseInput{{AmountVND:10000, Category:"food", OccurredAt:time.Date(2030,1,1,0,0,0,0,time.UTC)}})
	if !idempotency.IsConflict(err) { t.Fatalf("legacy batch parent should be reserved, got %v", err) }
}

func TestActorScopedKeysSurviveRestartAndConcurrentRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actor.db")
	s, err := Open(path)
	if err != nil { t.Fatal(err) }
	hash, _ := idempotency.HashValue(map[string]any{"content":"same"})
	for _, actor := range []struct{ actor, user string }{{"actor:a","user-a"},{"actor:b","user-b"}} {
		req := idempotency.Request{Actor:actor.actor, Operation:"note.create", Key:"shared-key", RequestHash:hash}
		if err := s.CreateNoteMutation(context.Background(), req, actor.user, "same"); err != nil { t.Fatal(err) }
	}
	if err := s.Close(); err != nil { t.Fatal(err) }

	s, err = Open(path)
	if err != nil { t.Fatal(err) }
	defer s.Close()
	req := idempotency.Request{Actor:"actor:a", Operation:"note.create", Key:"shared-key", RequestHash:hash}
	if err := s.CreateNoteMutation(context.Background(), req, "user-a", "same"); err != nil { t.Fatalf("restart replay failed: %v", err) }

	concurrentHash, _ := idempotency.HashValue(map[string]any{"content":"concurrent"})
	concurrent := idempotency.Request{Actor:"actor:c", Operation:"note.create", Key:"concurrent-key", RequestHash:concurrentHash}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.CreateNoteMutation(context.Background(), concurrent, "user-c", "concurrent") }()
	}
	wg.Wait(); close(errs)
	for err := range errs { if err != nil { t.Fatalf("concurrent replay: %v", err) } }
	notes, err := s.ListNotes(context.Background(), "user-c", 20)
	if err != nil { t.Fatal(err) }
	if len(notes) != 1 { t.Fatalf("concurrent mutation count=%d, want 1", len(notes)) }
}

func TestFreshSchemaAllowsActorScopedMarketWatchKey(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "market.db"))
	if err != nil { t.Fatal(err) }
	defer s.Close()
	unique, err := hasSingleColumnUniqueIndex(s.db, "market_watches", "idempotency_key")
	if err != nil { t.Fatal(err) }
	if unique { t.Fatal("fresh market_watches still has global unique key") }
	hash, _ := idempotency.HashValue(map[string]any{"symbol":"XAU/USD"})
	for _, x := range []struct{actor,user string}{{"actor:a","user-a"},{"actor:b","user-b"}} {
		_, err := s.CreateMarketWatchMutation(context.Background(), idempotency.Request{Actor:x.actor,Operation:"market.watch.create",Key:"watch-key",RequestHash:hash}, x.user, "device", "provider", "XAU/USD", "USD", ">", 3000)
		if err != nil { t.Fatal(err) }
	}
}
''', encoding="utf-8")
print("issue27 schema hard cut applied")

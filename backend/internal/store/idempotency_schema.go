package store

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

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
)

const maxListLimit = 100

type Store struct{ db *sql.DB }
type ExpenseInput = domain.ExpenseInput
type Expense = domain.Expense
type Note = domain.Note
type JournalEntry = domain.JournalEntry
type Reminder = domain.ScheduledItem
type VoiceMemo = domain.VoiceMemo

type ConversationMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	statements := []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS turn_results (turn_id TEXT PRIMARY KEY,response TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS expenses (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',amount_vnd INTEGER NOT NULL CHECK(amount_vnd>0),category TEXT NOT NULL,description TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS journal_entries (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS reminders (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL DEFAULT 'reminder',title TEXT NOT NULL,fire_at TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at TEXT NOT NULL DEFAULT '',paused_remaining_seconds INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS conversation_messages (id INTEGER PRIMARY KEY AUTOINCREMENT,turn_key TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',thread_id TEXT NOT NULL DEFAULT 'default',role TEXT NOT NULL CHECK(role IN ('user','assistant')),content TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(turn_key,role))`,
		`CREATE TABLE IF NOT EXISTS budgets (user_id TEXT NOT NULL DEFAULT '',period TEXT NOT NULL,limit_vnd INTEGER NOT NULL CHECK(limit_vnd>=0),updated_at TEXT NOT NULL,PRIMARY KEY(user_id,period))`,
		`CREATE TABLE IF NOT EXISTS voice_memos (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,transcript TEXT NOT NULL DEFAULT '',duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),created_at TEXT NOT NULL)`,
	}
	for _, q := range statements {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	for _, c := range []struct{ t, n, a string }{
		{"notes", "user_id", `ALTER TABLE notes ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
		{"expenses", "user_id", `ALTER TABLE expenses ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
		{"journal_entries", "user_id", `ALTER TABLE journal_entries ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
		{"reminders", "user_id", `ALTER TABLE reminders ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
		{"reminders", "device_id", `ALTER TABLE reminders ADD COLUMN device_id TEXT NOT NULL DEFAULT ''`},
		{"reminders", "kind", `ALTER TABLE reminders ADD COLUMN kind TEXT NOT NULL DEFAULT 'reminder'`},
		{"reminders", "attempts", `ALTER TABLE reminders ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`},
		{"reminders", "next_attempt_at", `ALTER TABLE reminders ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT ''`},
		{"reminders", "paused_remaining_seconds", `ALTER TABLE reminders ADD COLUMN paused_remaining_seconds INTEGER NOT NULL DEFAULT 0`},
		{"conversation_messages", "user_id", `ALTER TABLE conversation_messages ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
		{"conversation_messages", "thread_id", `ALTER TABLE conversation_messages ADD COLUMN thread_id TEXT NOT NULL DEFAULT 'default'`},
		{"voice_memos", "user_id", `ALTER TABLE voice_memos ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`},
	} {
		if err := s.ensureColumn(c.t, c.n, c.a); err != nil {
			return err
		}
	}
	if err := s.ensureBudgetOwnership(); err != nil {
		return err
	}
	// Preserve pre-ownership POC data. The project was single-user before this
	// migration, so unscoped rows are adopted by the configurable/default owner.
	for _, q := range []string{
		`UPDATE notes SET user_id='default' WHERE user_id=''`,
		`UPDATE expenses SET user_id='default' WHERE user_id=''`,
		`UPDATE journal_entries SET user_id='default' WHERE user_id=''`,
		`UPDATE reminders SET user_id='default' WHERE user_id=''`,
		`UPDATE budgets SET user_id='default' WHERE user_id=''`,
		`UPDATE voice_memos SET user_id='default' WHERE user_id=''`,
		`UPDATE conversation_messages SET user_id='default' WHERE user_id=''`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("legacy ownership migration: %w", err)
		}
	}
	// Older POC builds keyed conversation rows by device_id. Fresh/current
	// schemas intentionally do not have that column, so only adopt it when it
	// actually exists.
	hasLegacyDeviceID, err := s.hasColumn("conversation_messages", "device_id")
	if err != nil {
		return err
	}
	if hasLegacyDeviceID {
		if _, err := s.db.Exec(`UPDATE conversation_messages SET thread_id=device_id WHERE (thread_id='' OR thread_id='default') AND device_id!=''`); err != nil {
			return fmt.Errorf("legacy conversation ownership migration: %w", err)
		}
	}
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_expenses_user_time ON expenses(user_id,occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_user ON notes(user_id,id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_journal_user_time ON journal_entries(user_id,occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders(status,next_attempt_at,fire_at)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_owner ON reminders(user_id,device_id,status,fire_at)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_scope ON conversation_messages(user_id,thread_id,id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_voice_memos_owner ON voice_memos(user_id,device_id,id DESC)`,
	}
	for _, q := range indexes {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	for _, target := range [][2]string{{"expenses", "occurred_at"}, {"journal_entries", "occurred_at"}, {"reminders", "fire_at"}} {
		if err := s.normalizeTimeColumn(target[0], target[1]); err != nil {
			return err
		}
	}
	if err := s.migratePlatform(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureBudgetOwnership() error {
	rows, err := s.db.Query(`PRAGMA table_info(budgets)`)
	if err != nil {
		return err
	}
	hasUser := false
	for rows.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "user_id" {
			hasUser = true
		}
	}
	rows.Close()
	if hasUser {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE budgets RENAME TO budgets_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE TABLE budgets (user_id TEXT NOT NULL DEFAULT '',period TEXT NOT NULL,limit_vnd INTEGER NOT NULL CHECK(limit_vnd>=0),updated_at TEXT NOT NULL,PRIMARY KEY(user_id,period))`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO budgets(user_id,period,limit_vnd,updated_at) SELECT 'default',period,limit_vnd,updated_at FROM budgets_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE budgets_legacy`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &nn, &def, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) ensureColumn(table, column, alter string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &nn, &def, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if _, err := s.db.Exec(alter); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}
func (s *Store) normalizeTimeColumn(table, column string) error {
	rows, err := s.db.Query(fmt.Sprintf("SELECT id,%s FROM %s", column, table))
	if err != nil {
		return err
	}
	type rv struct {
		id  int64
		raw string
	}
	var vals []rv
	for rows.Next() {
		var x rv
		if err := rows.Scan(&x.id, &x.raw); err != nil {
			rows.Close()
			return err
		}
		vals = append(vals, x)
	}
	rows.Close()
	for _, x := range vals {
		p, err := parseStoredTime(x.raw)
		if err != nil {
			return err
		}
		v := p.UTC().Format(time.RFC3339Nano)
		if v != x.raw {
			if _, err := s.db.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE id=?", table, column), v, x.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) TurnResult(ctx context.Context, turnID string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT response FROM turn_results WHERE turn_id=?`, turnID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}
func (s *Store) SaveTurnResult(ctx context.Context, turnID, response string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO turn_results(turn_id,response,created_at) VALUES(?,?,?)`, turnID, response, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func owner(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}
func (s *Store) CreateNote(ctx context.Context, userID, key, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO notes(idempotency_key,user_id,content,created_at) VALUES(?,?,?,?)`, key, owner(userID), content, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) ListNotes(ctx context.Context, userID string, limit int) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,content,created_at FROM notes WHERE user_id=? ORDER BY id DESC LIMIT ?`, owner(userID), boundedLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var x Note
		var raw string
		if err := rows.Scan(&x.ID, &x.UserID, &x.Content, &raw); err != nil {
			return nil, err
		}
		x.CreatedAt, _ = parseStoredTime(raw)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) UpdateNote(ctx context.Context, userID string, id int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	return s.execChanged(ctx, "note", `UPDATE notes SET content=? WHERE id=? AND user_id=?`, content, id, owner(userID))
}
func (s *Store) DeleteNote(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "note", `DELETE FROM notes WHERE id=? AND user_id=?`, id, owner(userID))
}

func (s *Store) CreateExpense(ctx context.Context, userID, key string, amount int64, category, description string, occurredAt time.Time) error {
	return s.CreateExpenses(ctx, userID, key, []ExpenseInput{{AmountVND: amount, Category: category, Description: description, OccurredAt: occurredAt}})
}
func (s *Store) CreateExpenses(ctx context.Context, userID, key string, items []ExpenseInput) error {
	if len(items) < 1 || len(items) > 20 {
		return fmt.Errorf("expenses must contain between 1 and 20 items")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, x := range items {
		if x.AmountVND <= 0 || x.AmountVND > 1_000_000_000 {
			return fmt.Errorf("item %d amount_vnd is outside the accepted range", i)
		}
		x.Category = strings.TrimSpace(x.Category)
		if x.Category == "" {
			x.Category = "other"
		}
		x.Description = strings.TrimSpace(x.Description)
		if x.OccurredAt.IsZero() {
			return fmt.Errorf("item %d occurred_at is required", i)
		}
		k := key
		if len(items) > 1 {
			k = fmt.Sprintf("%s:%d", key, i)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES(?,?,?,?,?,?,?)`, k, owner(userID), x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC().Format(time.RFC3339Nano), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) ExpenseTotal(ctx context.Context, userID string, from, to time.Time) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_vnd),0) FROM expenses WHERE user_id=? AND occurred_at>=? AND occurred_at<?`, owner(userID), from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)).Scan(&v)
	return v, err
}
func (s *Store) ListExpenses(ctx context.Context, userID string, from, to time.Time, category string, limit int) ([]Expense, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("invalid expense range")
	}
	q := `SELECT id,user_id,amount_vnd,category,description,occurred_at FROM expenses WHERE user_id=? AND occurred_at>=? AND occurred_at<?`
	args := []any{owner(userID), from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)}
	if strings.TrimSpace(category) != "" {
		q += ` AND category=?`
		args = append(args, strings.TrimSpace(category))
	}
	q += ` ORDER BY occurred_at DESC,id DESC LIMIT ?`
	args = append(args, boundedLimit(limit))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expense
	for rows.Next() {
		var x Expense
		var raw string
		if err := rows.Scan(&x.ID, &x.UserID, &x.AmountVND, &x.Category, &x.Description, &raw); err != nil {
			return nil, err
		}
		x.OccurredAt, _ = parseStoredTime(raw)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) UpdateExpense(ctx context.Context, userID string, id, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 {
		return fmt.Errorf("amount_vnd is outside the accepted range")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	return s.execChanged(ctx, "expense", `UPDATE expenses SET amount_vnd=?,category=?,description=?,occurred_at=? WHERE id=? AND user_id=?`, amount, strings.TrimSpace(category), strings.TrimSpace(description), occurredAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
}
func (s *Store) DeleteExpense(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "expense", `DELETE FROM expenses WHERE id=? AND user_id=?`, id, owner(userID))
}

func (s *Store) CreateJournal(ctx context.Context, userID, key, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("journal content is required")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("journal occurred_at is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at) VALUES(?,?,?,?,?)`, key, owner(userID), content, occurredAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) ListJournal(ctx context.Context, userID string, from, to time.Time, limit int) ([]JournalEntry, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("invalid journal range")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,content,occurred_at FROM journal_entries WHERE user_id=? AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at DESC,id DESC LIMIT ?`, owner(userID), from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), boundedLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalEntry
	for rows.Next() {
		var x JournalEntry
		var raw string
		if err := rows.Scan(&x.ID, &x.UserID, &x.Content, &raw); err != nil {
			return nil, err
		}
		x.OccurredAt, _ = parseStoredTime(raw)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) UpdateJournal(ctx context.Context, userID string, id int64, content string, occurredAt time.Time) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("journal content is required")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("journal occurred_at is required")
	}
	return s.execChanged(ctx, "journal entry", `UPDATE journal_entries SET content=?,occurred_at=? WHERE id=? AND user_id=?`, strings.TrimSpace(content), occurredAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
}
func (s *Store) DeleteJournal(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "journal entry", `DELETE FROM journal_entries WHERE id=? AND user_id=?`, id, owner(userID))
}

func (s *Store) SetBudget(ctx context.Context, userID, period string, limit int64) error {
	if limit < 0 {
		return fmt.Errorf("budget must be >= 0")
	}
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO budgets(user_id,period,limit_vnd,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,period) DO UPDATE SET limit_vnd=excluded.limit_vnd,updated_at=excluded.updated_at`, owner(userID), period, limit, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) BudgetLimit(ctx context.Context, userID, period string) (int64, bool, error) {
	period, err := validBudgetPeriod(period)
	if err != nil {
		return 0, false, err
	}
	var v int64
	err = s.db.QueryRowContext(ctx, `SELECT limit_vnd FROM budgets WHERE user_id=? AND period=?`, owner(userID), period).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return v, err == nil, err
}
func (s *Store) DeleteBudget(ctx context.Context, userID, period string) error {
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	return s.execChanged(ctx, "budget", `DELETE FROM budgets WHERE user_id=? AND period=?`, owner(userID), period)
}

func validBudgetPeriod(period string) (string, error) {
	period = strings.ToLower(strings.TrimSpace(period))
	if period == "" {
		period = "weekly"
	}
	switch period {
	case "daily", "weekly", "monthly":
		return period, nil
	default:
		return "", fmt.Errorf("unsupported budget period %q", period)
	}
}

func (s *Store) CreateReminderForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error {
	return s.createScheduled(ctx, userID, key, deviceID, "reminder", title, fireAt)
}
func (s *Store) CreateTimerForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error {
	return s.createScheduled(ctx, userID, key, deviceID, "timer", title, fireAt)
}
func (s *Store) createScheduled(ctx context.Context, userID, key, deviceID, kind, title string, fireAt time.Time) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("scheduled item title is required")
	}
	if kind != "reminder" && kind != "timer" {
		return fmt.Errorf("invalid scheduled kind")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES(?,?,?,?,?,?,'pending',0,'',0,?)`, key, owner(userID), strings.TrimSpace(deviceID), kind, title, fireAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) ListReminders(ctx context.Context, userID, deviceID, status string, limit int) ([]Reminder, error) {
	return s.listScheduled(ctx, userID, deviceID, "reminder", status, limit)
}
func (s *Store) ListTimers(ctx context.Context, userID, deviceID, status string, limit int) ([]Reminder, error) {
	return s.listScheduled(ctx, userID, deviceID, "timer", status, limit)
}
func (s *Store) listScheduled(ctx context.Context, userID, deviceID, kind, status string, limit int) ([]Reminder, error) {
	q := `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds FROM reminders WHERE user_id=? AND kind=?`
	args := []any{owner(userID), kind}
	if deviceID != "" {
		q += ` AND (device_id=? OR device_id='')`
		args = append(args, deviceID)
	}
	if status == "active" {
		q += ` AND status IN ('pending','dispatching','sent','paused')`
	} else if status != "" && status != "all" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY fire_at ASC,id ASC LIMIT ?`
	args = append(args, boundedLimit(limit))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReminders(rows)
}
func (s *Store) UpdateScheduledItem(ctx context.Context, userID string, id int64, title string, fireAt time.Time) error {
	if strings.TrimSpace(title) == "" || fireAt.IsZero() {
		return fmt.Errorf("title and fire_at are required")
	}
	return s.execChanged(ctx, "scheduled item", `UPDATE reminders SET title=?,fire_at=?,status='pending',attempts=0,next_attempt_at='',paused_remaining_seconds=0 WHERE id=? AND user_id=? AND status!='fired'`, strings.TrimSpace(title), fireAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
}
func (s *Store) PauseTimer(ctx context.Context, userID string, id int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind, status, raw string
	if err := tx.QueryRowContext(ctx, `SELECT kind,status,fire_at FROM reminders WHERE id=? AND user_id=?`, id, owner(userID)).Scan(&kind, &status, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("timer not found")
		}
		return err
	}
	if kind != "timer" {
		return fmt.Errorf("scheduled item is not a timer")
	}
	if status != "pending" {
		return fmt.Errorf("timer cannot be paused from status %q", status)
	}
	fireAt, err := parseStoredTime(raw)
	if err != nil {
		return err
	}
	delta := fireAt.Sub(now)
	if delta <= 0 {
		return fmt.Errorf("timer is already due")
	}
	remaining := int64((delta + time.Second - 1) / time.Second)
	res, err := tx.ExecContext(ctx, `UPDATE reminders SET status='paused',paused_remaining_seconds=?,attempts=0,next_attempt_at='' WHERE id=? AND user_id=? AND kind='timer' AND status='pending'`, remaining, id, owner(userID))
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("timer pause raced with another update")
	}
	return tx.Commit()
}

func (s *Store) ResumeTimer(ctx context.Context, userID string, id int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind, status string
	var remaining int64
	if err := tx.QueryRowContext(ctx, `SELECT kind,status,paused_remaining_seconds FROM reminders WHERE id=? AND user_id=?`, id, owner(userID)).Scan(&kind, &status, &remaining); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("timer not found")
		}
		return err
	}
	if kind != "timer" {
		return fmt.Errorf("scheduled item is not a timer")
	}
	if status != "paused" || remaining < 1 {
		return fmt.Errorf("timer is not paused")
	}
	fireAt := now.Add(time.Duration(remaining) * time.Second).UTC()
	res, err := tx.ExecContext(ctx, `UPDATE reminders SET status='pending',fire_at=?,paused_remaining_seconds=0,attempts=0,next_attempt_at='' WHERE id=? AND user_id=? AND kind='timer' AND status='paused'`, fireAt.Format(time.RFC3339Nano), id, owner(userID))
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("timer resume raced with another update")
	}
	return tx.Commit()
}

func (s *Store) CancelScheduledItem(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "scheduled item", `UPDATE reminders SET status='cancelled' WHERE id=? AND user_id=? AND status IN ('pending','dispatching','sent','paused')`, id, owner(userID))
}
func (s *Store) DeleteScheduledItem(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "scheduled item", `DELETE FROM reminders WHERE id=? AND user_id=?`, id, owner(userID))
}
func (s *Store) NextReminder(ctx context.Context, userID, deviceID string, now time.Time) (Reminder, bool, error) {
	q := `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds FROM reminders WHERE user_id=? AND kind='reminder' AND status IN ('pending','sent') AND fire_at>?`
	args := []any{owner(userID), now.UTC().Format(time.RFC3339Nano)}
	if deviceID != "" {
		q += ` AND (device_id=? OR device_id='')`
		args = append(args, deviceID)
	}
	q += ` ORDER BY fire_at ASC,id ASC LIMIT 1`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return Reminder{}, false, err
	}
	defer rows.Close()
	items, err := scanReminders(rows)
	if err != nil {
		return Reminder{}, false, err
	}
	if len(items) == 0 {
		return Reminder{}, false, nil
	}
	return items[0], true, nil
}
func (s *Store) ClaimDueReminders(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	raw := now.UTC().Format(time.RFC3339Nano)
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds FROM reminders WHERE (status='pending' AND fire_at<=?) OR (status='sent' AND next_attempt_at!='' AND next_attempt_at<=?) ORDER BY fire_at ASC,id ASC LIMIT ?`, raw, raw, boundedLimit(limit))
	if err != nil {
		return nil, err
	}
	items, err := scanReminders(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	claimed := items[:0]
	for _, x := range items {
		res, err := tx.ExecContext(ctx, `UPDATE reminders SET status='dispatching' WHERE id=? AND status IN ('pending','sent')`, x.ID)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			x.Status = "dispatching"
			claimed = append(claimed, x)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}
func (s *Store) RecoverDispatchingReminders(ctx context.Context) (int64, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE reminders SET status=CASE WHEN attempts>0 THEN 'sent' ELSE 'pending' END WHERE status='dispatching'`)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
func (s *Store) MarkReminderSent(ctx context.Context, id int64, nextAttempt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE reminders SET status='sent',attempts=attempts+1,next_attempt_at=? WHERE id=? AND status='dispatching'`, nextAttempt.UTC().Format(time.RFC3339Nano), id)
	return err
}
func (s *Store) AcknowledgeReminder(ctx context.Context, userID, deviceID string, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE reminders SET status='fired',next_attempt_at='' WHERE id=? AND user_id=? AND (device_id=? OR device_id='') AND status IN ('sent','dispatching','fired')`, id, owner(userID), strings.TrimSpace(deviceID))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("alarm acknowledgement is not owned by this user/device")
	}
	return nil
}
func (s *Store) ReleaseReminder(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE reminders SET status=CASE WHEN attempts>0 THEN 'sent' ELSE 'pending' END WHERE id=? AND status='dispatching'`, id)
	return err
}

// MarkReminderFired is retained for legacy internal tests/callers. Device-originated
// acknowledgements must use AcknowledgeReminder so ownership/targeting is checked.
func (s *Store) MarkReminderFired(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE reminders SET status='fired',next_attempt_at='' WHERE id=? AND status IN ('sent','dispatching')`, id)
	return err
}

func (s *Store) SaveConversationMessageScoped(ctx context.Context, turnKey, userID, threadID, role, content string) error {
	if role != "user" && role != "assistant" {
		return fmt.Errorf("invalid conversation role %q", role)
	}
	if strings.TrimSpace(threadID) == "" {
		threadID = "default"
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO conversation_messages(turn_key,user_id,thread_id,role,content,created_at) VALUES(?,?,?,?,?,?)`, turnKey, owner(userID), threadID, role, strings.TrimSpace(content), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) ConversationHistoryScoped(ctx context.Context, userID, threadID string, limit int) ([]ConversationMessage, error) {
	if limit <= 0 || limit > 32 {
		limit = 12
	}
	if strings.TrimSpace(threadID) == "" {
		threadID = "default"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT role,content,created_at FROM (SELECT id,role,content,created_at FROM conversation_messages WHERE user_id=? AND thread_id=? ORDER BY id DESC LIMIT ?) ORDER BY id ASC`, owner(userID), threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationMessage
	for rows.Next() {
		var x ConversationMessage
		var raw string
		if err := rows.Scan(&x.Role, &x.Content, &raw); err != nil {
			return nil, err
		}
		x.CreatedAt, _ = parseStoredTime(raw)
		out = append(out, x)
	}
	return out, rows.Err()
}

// Legacy device-scoped wrappers.
func (s *Store) DeleteConversationThread(ctx context.Context, userID, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = "default"
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversation_messages WHERE user_id=? AND thread_id=?`, owner(userID), threadID)
	return err
}

func (s *Store) SaveConversationMessage(ctx context.Context, turnKey, deviceID, role, content string) error {
	return s.SaveConversationMessageScoped(ctx, turnKey, deviceID, "default", role, content)
}
func (s *Store) ConversationHistory(ctx context.Context, deviceID string, limit int) ([]ConversationMessage, error) {
	return s.ConversationHistoryScoped(ctx, deviceID, "default", limit)
}

func (s *Store) CreateVoiceMemo(ctx context.Context, userID, key, deviceID, path, transcript string, durationMS int64) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("voice memo path is required")
	}
	if durationMS < 0 {
		return fmt.Errorf("voice memo duration must be non-negative")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES(?,?,?,?,?,?,?)`, key, owner(userID), strings.TrimSpace(deviceID), path, strings.TrimSpace(transcript), durationMS, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) VoiceMemoByKey(ctx context.Context, userID, key string) (VoiceMemo, bool, error) {
	var x VoiceMemo
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=? AND idempotency_key=?`, owner(userID), key).Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Path, &x.Transcript, &x.DurationMS, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceMemo{}, false, nil
	}
	if err != nil {
		return VoiceMemo{}, false, err
	}
	x.CreatedAt, _ = parseStoredTime(raw)
	return x, true, nil
}
func (s *Store) VoiceMemoByID(ctx context.Context, userID string, id int64) (VoiceMemo, bool, error) {
	var x VoiceMemo
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=? AND id=?`, owner(userID), id).Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Path, &x.Transcript, &x.DurationMS, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceMemo{}, false, nil
	}
	if err != nil {
		return VoiceMemo{}, false, err
	}
	x.CreatedAt, _ = parseStoredTime(raw)
	return x, true, nil
}

func (s *Store) ListVoiceMemos(ctx context.Context, userID, deviceID string, limit int) ([]VoiceMemo, error) {
	q := `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=?`
	args := []any{owner(userID)}
	if deviceID != "" {
		q += ` AND (device_id=? OR device_id='')`
		args = append(args, deviceID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, boundedLimit(limit))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VoiceMemo
	for rows.Next() {
		var x VoiceMemo
		var raw string
		if err := rows.Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Path, &x.Transcript, &x.DurationMS, &raw); err != nil {
			return nil, err
		}
		x.CreatedAt, _ = parseStoredTime(raw)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) DeleteVoiceMemo(ctx context.Context, userID string, id int64) error {
	return s.execChanged(ctx, "voice memo", `DELETE FROM voice_memos WHERE id=? AND user_id=?`, id, owner(userID))
}

func (s *Store) execChanged(ctx context.Context, label, query string, args ...any) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	return requireChanged(result, err, label)
}
func requireChanged(result sql.Result, err error, label string) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s not found or not mutable", label)
	}
	return nil
}
func parseStoredTime(v string) (time.Time, error) {
	p, err := time.Parse(time.RFC3339Nano, v)
	if err == nil {
		return p, nil
	}
	return time.Parse(time.RFC3339, v)
}
func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		var x Reminder
		var fire, next string
		if err := rows.Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Kind, &x.Title, &fire, &x.Status, &x.Attempts, &next, &x.PausedRemainingSeconds); err != nil {
			return nil, err
		}
		var err error
		x.FireAt, err = parseStoredTime(fire)
		if err != nil {
			return nil, err
		}
		if next != "" {
			t, e := parseStoredTime(next)
			if e == nil {
				x.NextAttempt = &t
			}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func boundedLimit(n int) int {
	if n <= 0 {
		return 10
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

// Backward-compatible helpers used by older tests and migrations.
func (s *Store) CreateReminder(ctx context.Context, key, title string, fireAt time.Time) error {
	return s.CreateReminderForDevice(ctx, "", key, "", title, fireAt)
}

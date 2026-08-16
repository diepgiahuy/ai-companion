package pgstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateNote(ctx context.Context, userID, key, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "notes", userID, key); err != nil { return err }
	_, err = tx.Exec(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at)
		SELECT $1,$2,$3,$4 WHERE NOT EXISTS (SELECT 1 FROM notes WHERE user_id=$2 AND idempotency_key=$1)`,
		key, owner(userID), content, time.Now().UTC())
	if err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) ListNotes(ctx context.Context, userID string, limit int) ([]domain.Note, error) {
	return s.QueryNotes(ctx, userID, domain.NoteQuery{Limit: limit})
}

func (s *Store) QueryNotes(ctx context.Context, userID string, query domain.NoteQuery) ([]domain.Note, error) {
	sqlQuery := `SELECT id,user_id,content,created_at FROM notes WHERE user_id=$1`
	args := []any{owner(userID)}
	argIdx := 2
	if !query.From.IsZero() {
		sqlQuery += fmt.Sprintf(` AND created_at >= $%d`, argIdx)
		args = append(args, query.From.UTC())
		argIdx++
	}
	if !query.To.IsZero() {
		sqlQuery += fmt.Sprintf(` AND created_at < $%d`, argIdx)
		args = append(args, query.To.UTC())
		argIdx++
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		sqlQuery += fmt.Sprintf(` AND content ILIKE $%d`, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}
	sqlQuery += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, argIdx)
	args = append(args, boundedLimit(query.Limit))

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.Note
	for rows.Next() {
		var x domain.Note
		if err := rows.Scan(&x.ID, &x.UserID, &x.Content, &x.CreatedAt); err != nil { return nil, err }
		x.CreatedAt = x.CreatedAt.UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) UpdateNote(ctx context.Context, userID string, id int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" { return fmt.Errorf("note content is required") }
	tag, err := s.pool.Exec(ctx, `UPDATE notes SET content=$1 WHERE id=$2 AND user_id=$3`, content, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "note")
}

func (s *Store) DeleteNote(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notes WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "note")
}

func (s *Store) CreateExpense(ctx context.Context, userID, key string, amount int64, category, description string, occurredAt time.Time) error {
	return s.CreateExpenses(ctx, userID, key, []domain.ExpenseInput{{AmountVND: amount, Category: category, Description: description, OccurredAt: occurredAt}})
}

func (s *Store) CreateExpenses(ctx context.Context, userID, key string, items []domain.ExpenseInput) error {
	if len(items) < 1 || len(items) > 20 { return fmt.Errorf("expenses must contain between 1 and 20 items") }
	validated := append([]domain.ExpenseInput(nil), items...)
	for i := range validated {
		x := &validated[i]
		if x.AmountVND <= 0 || x.AmountVND > 1_000_000_000 { return fmt.Errorf("item %d amount_vnd is outside the accepted range", i) }
		x.Category = strings.TrimSpace(x.Category)
		if x.Category == "" { x.Category = "other" }
		x.Description = strings.TrimSpace(x.Description)
		if x.OccurredAt.IsZero() { return fmt.Errorf("item %d occurred_at is required", i) }
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "expenses", userID, key); err != nil { return err }
	now := time.Now().UTC()
	for i, x := range validated {
		itemKey := key
		if len(validated) > 1 { itemKey = fmt.Sprintf("%s:%d", key, i) }
		_, err := tx.Exec(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at)
			SELECT $1,$2,$3,$4,$5,$6,$7 WHERE NOT EXISTS (SELECT 1 FROM expenses WHERE user_id=$2 AND idempotency_key=$1)`,
			itemKey, owner(userID), x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC(), now)
		if err != nil { return err }
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpenseTotal(ctx context.Context, userID string, from, to time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_vnd),0) FROM expenses WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3`, owner(userID), from.UTC(), to.UTC()).Scan(&total)
	return total, err
}

func (s *Store) ListExpenses(ctx context.Context, userID string, from, to time.Time, category string, limit int) ([]domain.Expense, error) {
	if !to.After(from) { return nil, fmt.Errorf("invalid expense range") }
	query := `SELECT id,user_id,amount_vnd,category,description,occurred_at FROM expenses WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3`
	args := []any{owner(userID), from.UTC(), to.UTC()}
	if strings.TrimSpace(category) != "" {
		query += ` AND category=$4 ORDER BY occurred_at DESC,id DESC LIMIT $5`
		args = append(args, strings.TrimSpace(category), boundedLimit(limit))
	} else {
		query += ` ORDER BY occurred_at DESC,id DESC LIMIT $4`
		args = append(args, boundedLimit(limit))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.Expense
	for rows.Next() {
		var x domain.Expense
		if err := rows.Scan(&x.ID, &x.UserID, &x.AmountVND, &x.Category, &x.Description, &x.OccurredAt); err != nil { return nil, err }
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) UpdateExpense(ctx context.Context, userID string, id, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 { return fmt.Errorf("amount_vnd is outside the accepted range") }
	if occurredAt.IsZero() { return fmt.Errorf("occurred_at is required") }
	tag, err := s.pool.Exec(ctx, `UPDATE expenses SET amount_vnd=$1,category=$2,description=$3,occurred_at=$4 WHERE id=$5 AND user_id=$6`, amount, strings.TrimSpace(category), strings.TrimSpace(description), occurredAt.UTC(), id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "expense")
}

func (s *Store) DeleteExpense(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM expenses WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "expense")
}

func (s *Store) SetBudget(ctx context.Context, userID, period string, limit int64) error {
	if limit < 0 { return fmt.Errorf("budget must be >= 0") }
	period, err := validBudgetPeriod(period)
	if err != nil { return err }
	_, err = s.pool.Exec(ctx, `INSERT INTO budgets(user_id,period,limit_vnd,updated_at) VALUES($1,$2,$3,$4)
		ON CONFLICT(user_id,period) DO UPDATE SET limit_vnd=EXCLUDED.limit_vnd,updated_at=EXCLUDED.updated_at`, owner(userID), period, limit, time.Now().UTC())
	return err
}

func (s *Store) BudgetLimit(ctx context.Context, userID, period string) (int64, bool, error) {
	period, err := validBudgetPeriod(period)
	if err != nil { return 0, false, err }
	var limit int64
	err = s.pool.QueryRow(ctx, `SELECT limit_vnd FROM budgets WHERE user_id=$1 AND period=$2`, owner(userID), period).Scan(&limit)
	if err == pgx.ErrNoRows { return 0, false, nil }
	return limit, err == nil, err
}

func (s *Store) DeleteBudget(ctx context.Context, userID, period string) error {
	period, err := validBudgetPeriod(period)
	if err != nil { return err }
	tag, err := s.pool.Exec(ctx, `DELETE FROM budgets WHERE user_id=$1 AND period=$2`, owner(userID), period)
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "budget")
}

func (s *Store) SetSavingsGoal(ctx context.Context, userID, period string, targetVND int64, description string, effectiveFrom time.Time) error {
	if err := domain.ValidateSavingsTarget(targetVND); err != nil {
		return err
	}
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	if effectiveFrom.IsZero() {
		effectiveFrom = time.Now().UTC()
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `INSERT INTO savings_goals(user_id, period, target_vnd, description, effective_from, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT(user_id, period) DO UPDATE SET target_vnd=EXCLUDED.target_vnd, description=EXCLUDED.description, effective_from=EXCLUDED.effective_from, updated_at=EXCLUDED.updated_at`,
		owner(userID), period, targetVND, strings.TrimSpace(description), effectiveFrom, now, now)
	return err
}

func (s *Store) GetSavingsGoal(ctx context.Context, userID, period string) (domain.SavingsGoal, bool, error) {
	period, err := validBudgetPeriod(period)
	if err != nil {
		return domain.SavingsGoal{}, false, err
	}
	var g domain.SavingsGoal
	err = s.pool.QueryRow(ctx, `SELECT user_id, period, target_vnd, description, effective_from, created_at, updated_at FROM savings_goals WHERE user_id=$1 AND period=$2`, owner(userID), period).
		Scan(&g.UserID, &g.Period, &g.TargetVND, &g.Description, &g.EffectiveFrom, &g.CreatedAt, &g.UpdatedAt)
	if err == pgx.ErrNoRows {
		return domain.SavingsGoal{}, false, nil
	}
	return g, err == nil, err
}

func (s *Store) DeleteSavingsGoal(ctx context.Context, userID, period string) error {
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM savings_goals WHERE user_id=$1 AND period=$2`, owner(userID), period)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "savings_goal")
}

func (s *Store) CreateJournal(ctx context.Context, userID, key, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content)
	if content == "" { return fmt.Errorf("journal content is required") }
	if occurredAt.IsZero() { return fmt.Errorf("journal occurred_at is required") }
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "journal_entries", userID, key); err != nil { return err }
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at)
		SELECT $1,$2,$3,$4,$5 WHERE NOT EXISTS (SELECT 1 FROM journal_entries WHERE user_id=$2 AND idempotency_key=$1)`, key, owner(userID), content, occurredAt.UTC(), time.Now().UTC())
	if err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) ListJournal(ctx context.Context, userID string, from, to time.Time, limit int) ([]domain.JournalEntry, error) {
	if !to.After(from) { return nil, fmt.Errorf("invalid journal range") }
	rows, err := s.pool.Query(ctx, `SELECT id,user_id,content,occurred_at FROM journal_entries WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3 ORDER BY occurred_at DESC,id DESC LIMIT $4`, owner(userID), from.UTC(), to.UTC(), boundedLimit(limit))
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.JournalEntry
	for rows.Next() {
		var x domain.JournalEntry
		if err := rows.Scan(&x.ID, &x.UserID, &x.Content, &x.OccurredAt); err != nil { return nil, err }
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) UpdateJournal(ctx context.Context, userID string, id int64, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content)
	if content == "" { return fmt.Errorf("journal content is required") }
	if occurredAt.IsZero() { return fmt.Errorf("journal occurred_at is required") }
	tag, err := s.pool.Exec(ctx, `UPDATE journal_entries SET content=$1,occurred_at=$2 WHERE id=$3 AND user_id=$4`, content, occurredAt.UTC(), id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "journal entry")
}

func (s *Store) DeleteJournal(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM journal_entries WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "journal entry")
}

func (s *Store) CreateReminderForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error {
	return s.createScheduled(ctx, userID, key, deviceID, "reminder", title, fireAt)
}

func (s *Store) CreateTimerForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error {
	return s.createScheduled(ctx, userID, key, deviceID, "timer", title, fireAt)
}

func (s *Store) createScheduled(ctx context.Context, userID, key, deviceID, kind, title string, fireAt time.Time) error {
	title = strings.TrimSpace(title)
	if title == "" { return fmt.Errorf("scheduled item title is required") }
	if fireAt.IsZero() { return fmt.Errorf("fire_at is required") }
	if kind != "reminder" && kind != "timer" { return fmt.Errorf("invalid scheduled kind") }
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "reminders", userID, key); err != nil { return err }
	_, err = tx.Exec(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at)
		SELECT $1,$2,$3,$4,$5,$6,'pending',0,NULL,0,$7 WHERE NOT EXISTS (SELECT 1 FROM reminders WHERE user_id=$2 AND idempotency_key=$1)`,
		key, owner(userID), strings.TrimSpace(deviceID), kind, title, fireAt.UTC(), time.Now().UTC())
	if err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) ListReminders(ctx context.Context, userID, deviceID, status string, limit int) ([]domain.ScheduledItem, error) {
	return s.listScheduled(ctx, userID, deviceID, "reminder", status, limit)
}

func (s *Store) ListTimers(ctx context.Context, userID, deviceID, status string, limit int) ([]domain.ScheduledItem, error) {
	return s.listScheduled(ctx, userID, deviceID, "timer", status, limit)
}

func (s *Store) listScheduled(ctx context.Context, userID, deviceID, kind, status string, limit int) ([]domain.ScheduledItem, error) {
	query := `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,COALESCE(next_attempt_at,'epoch'::timestamptz),next_attempt_at IS NOT NULL,paused_remaining_seconds FROM reminders WHERE user_id=$1 AND kind=$2`
	args := []any{owner(userID), kind}
	next := 3
	if strings.TrimSpace(deviceID) != "" {
		query += fmt.Sprintf(` AND (device_id=$%d OR device_id='')`, next)
		args = append(args, strings.TrimSpace(deviceID)); next++
	}
	status = strings.TrimSpace(status)
	if status == "active" {
		query += ` AND status IN ('pending','dispatching','sent','paused')`
	} else if status != "" && status != "all" {
		query += fmt.Sprintf(` AND status=$%d`, next)
		args = append(args, status); next++
	}
	query += fmt.Sprintf(` ORDER BY fire_at ASC,id ASC LIMIT $%d`, next)
	args = append(args, boundedLimit(limit))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanScheduled(rows)
}

func scanScheduled(rows pgx.Rows) ([]domain.ScheduledItem, error) {
	var out []domain.ScheduledItem
	for rows.Next() {
		var x domain.ScheduledItem
		var next time.Time
		var hasNext bool
		if err := rows.Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Kind, &x.Title, &x.FireAt, &x.Status, &x.Attempts, &next, &hasNext, &x.PausedRemainingSeconds); err != nil { return nil, err }
		if hasNext { next = next.UTC(); x.NextAttempt = &next }
		x.FireAt = x.FireAt.UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) UpdateScheduledItem(ctx context.Context, userID string, id int64, title string, fireAt time.Time) error {
	title = strings.TrimSpace(title)
	if title == "" || fireAt.IsZero() { return fmt.Errorf("title and fire_at are required") }
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET title=$1,fire_at=$2,status='pending',attempts=0,next_attempt_at=NULL,paused_remaining_seconds=0 WHERE id=$3 AND user_id=$4 AND status!='fired'`, title, fireAt.UTC(), id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "scheduled item")
}

func (s *Store) PauseTimer(ctx context.Context, userID string, id int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	var kind, status string
	var fireAt time.Time
	if err := tx.QueryRow(ctx, `SELECT kind,status,fire_at FROM reminders WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, owner(userID)).Scan(&kind, &status, &fireAt); err != nil {
		if err == pgx.ErrNoRows { return fmt.Errorf("timer not found") }
		return err
	}
	if kind != "timer" { return fmt.Errorf("scheduled item is not a timer") }
	if status != "pending" { return fmt.Errorf("timer cannot be paused from status %q", status) }
	delta := fireAt.Sub(now)
	if delta <= 0 { return fmt.Errorf("timer is already due") }
	remaining := int64((delta + time.Second - 1) / time.Second)
	tag, err := tx.Exec(ctx, `UPDATE reminders SET status='paused',paused_remaining_seconds=$1,attempts=0,next_attempt_at=NULL WHERE id=$2 AND user_id=$3 AND kind='timer' AND status='pending'`, remaining, id, owner(userID))
	if err != nil { return err }
	if err := requireRowsChanged(tag.RowsAffected(), "timer"); err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) ResumeTimer(ctx context.Context, userID string, id int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	var kind, status string
	var remaining int64
	if err := tx.QueryRow(ctx, `SELECT kind,status,paused_remaining_seconds FROM reminders WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, owner(userID)).Scan(&kind, &status, &remaining); err != nil {
		if err == pgx.ErrNoRows { return fmt.Errorf("timer not found") }
		return err
	}
	if kind != "timer" { return fmt.Errorf("scheduled item is not a timer") }
	if status != "paused" || remaining < 1 { return fmt.Errorf("timer is not paused") }
	fireAt := now.Add(time.Duration(remaining) * time.Second).UTC()
	tag, err := tx.Exec(ctx, `UPDATE reminders SET status='pending',fire_at=$1,paused_remaining_seconds=0,attempts=0,next_attempt_at=NULL WHERE id=$2 AND user_id=$3 AND kind='timer' AND status='paused'`, fireAt, id, owner(userID))
	if err != nil { return err }
	if err := requireRowsChanged(tag.RowsAffected(), "timer"); err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) CancelScheduledItem(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET status='cancelled' WHERE id=$1 AND user_id=$2 AND status IN ('pending','dispatching','sent','paused')`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "scheduled item")
}

func (s *Store) DeleteScheduledItem(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM reminders WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "scheduled item")
}

func (s *Store) CreateVoiceMemo(ctx context.Context, userID, key, deviceID, path, transcript string, durationMS int64) error {
	path = strings.TrimSpace(path)
	if path == "" { return fmt.Errorf("voice memo path is required") }
	if durationMS < 0 { return fmt.Errorf("voice memo duration must be non-negative") }
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "voice_memos", userID, key); err != nil { return err }
	_, err = tx.Exec(ctx, `INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at)
		SELECT $1,$2,$3,$4,$5,$6,$7 WHERE NOT EXISTS (SELECT 1 FROM voice_memos WHERE user_id=$2 AND idempotency_key=$1)`,
		key, owner(userID), strings.TrimSpace(deviceID), path, strings.TrimSpace(transcript), durationMS, time.Now().UTC())
	if err != nil { return err }
	return tx.Commit(ctx)
}

func (s *Store) VoiceMemoByKey(ctx context.Context, userID, key string) (domain.VoiceMemo, bool, error) {
	return s.voiceMemo(ctx, `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=$1 AND idempotency_key=$2`, owner(userID), key)
}

func (s *Store) VoiceMemoByID(ctx context.Context, userID string, id int64) (domain.VoiceMemo, bool, error) {
	return s.voiceMemo(ctx, `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=$1 AND id=$2`, owner(userID), id)
}

func (s *Store) voiceMemo(ctx context.Context, query string, args ...any) (domain.VoiceMemo, bool, error) {
	var x domain.VoiceMemo
	err := s.pool.QueryRow(ctx, query, args...).Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Path, &x.Transcript, &x.DurationMS, &x.CreatedAt)
	if err == pgx.ErrNoRows { return domain.VoiceMemo{}, false, nil }
	if err != nil { return domain.VoiceMemo{}, false, err }
	x.CreatedAt = x.CreatedAt.UTC()
	return x, true, nil
}

func (s *Store) ListVoiceMemos(ctx context.Context, userID, deviceID string, limit int) ([]domain.VoiceMemo, error) {
	return s.QueryVoiceMemos(ctx, userID, domain.VoiceMemoQuery{DeviceID: deviceID, Limit: limit})
}

func (s *Store) QueryVoiceMemos(ctx context.Context, userID string, query domain.VoiceMemoQuery) ([]domain.VoiceMemo, error) {
	sqlQuery := `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE user_id=$1`
	args := []any{owner(userID)}
	argIdx := 2
	if dev := strings.TrimSpace(query.DeviceID); dev != "" {
		sqlQuery += fmt.Sprintf(` AND (device_id=$%d OR device_id='')`, argIdx)
		args = append(args, dev)
		argIdx++
	}
	if !query.From.IsZero() {
		sqlQuery += fmt.Sprintf(` AND created_at >= $%d`, argIdx)
		args = append(args, query.From.UTC())
		argIdx++
	}
	if !query.To.IsZero() {
		sqlQuery += fmt.Sprintf(` AND created_at < $%d`, argIdx)
		args = append(args, query.To.UTC())
		argIdx++
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		sqlQuery += fmt.Sprintf(` AND transcript ILIKE $%d`, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}
	sqlQuery += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, argIdx)
	args = append(args, boundedLimit(query.Limit))

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.VoiceMemo
	for rows.Next() {
		var x domain.VoiceMemo
		if err := rows.Scan(&x.ID, &x.UserID, &x.DeviceID, &x.Path, &x.Transcript, &x.DurationMS, &x.CreatedAt); err != nil { return nil, err }
		x.CreatedAt = x.CreatedAt.UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) DeleteVoiceMemo(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM voice_memos WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil { return err }
	return requireRowsChanged(tag.RowsAffected(), "voice memo")
}

func (s *Store) ListUserDevices(ctx context.Context, userID string) ([]domain.DeviceItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT device_id, user_id, COALESCE(plan,''), status, created_at, rotated_at FROM device_credentials WHERE user_id=$1 AND status='active' ORDER BY created_at ASC`, owner(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DeviceItem
	for rows.Next() {
		var d domain.DeviceItem
		if err := rows.Scan(&d.DeviceID, &d.UserID, &d.Plan, &d.Status, &d.CreatedAt, &d.RotatedAt); err != nil {
			return nil, err
		}
		d.CreatedAt = d.CreatedAt.UTC()
		d.RotatedAt = d.RotatedAt.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

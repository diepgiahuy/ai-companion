package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
)

func requireMutationOperation(request idempotency.Request, operation string) error {
	if strings.TrimSpace(request.Operation) != operation {
		return fmt.Errorf("idempotency operation mismatch: got %q want %q", request.Operation, operation)
	}
	return request.Validate()
}

func runMutationMarker(ctx context.Context, s *Store, request idempotency.Request, operation string, mutate func(*sql.Tx) error) error {
	if err := requireMutationOperation(request, operation); err != nil {
		return err
	}
	_, err := s.runIdempotentMutation(ctx, request, func(tx *sql.Tx) (any, error) {
		if err := mutate(tx); err != nil {
			return nil, err
		}
		return map[string]bool{"committed": true}, nil
	})
	return err
}

func runMutationValue[T any](ctx context.Context, s *Store, request idempotency.Request, operation string, mutate func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	if err := requireMutationOperation(request, operation); err != nil {
		return zero, err
	}
	outcome, err := s.runIdempotentMutation(ctx, request, func(tx *sql.Tx) (any, error) {
		return mutate(tx)
	})
	if err != nil {
		return zero, err
	}
	var value T
	if err := json.Unmarshal([]byte(outcome.JSON), &value); err != nil {
		return zero, fmt.Errorf("decode committed %s outcome: %w", operation, err)
	}
	return value, nil
}

func (s *Store) CreateExpenseMutation(ctx context.Context, request idempotency.Request, userID string, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 {
		return fmt.Errorf("amount_vnd is outside the accepted range")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "other"
	}
	description = strings.TrimSpace(description)
	return runMutationMarker(ctx, s, request, "expense.create", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES(?,?,?,?,?,?,?)`, request.Key, owner(userID), amount, category, description, occurredAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func (s *Store) CreateExpensesMutation(ctx context.Context, request idempotency.Request, userID string, items []domain.ExpenseInput) error {
	if len(items) < 1 || len(items) > 20 {
		return fmt.Errorf("expenses must contain between 1 and 20 items")
	}
	validated := make([]domain.ExpenseInput, len(items))
	copy(validated, items)
	for i := range validated {
		x := &validated[i]
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
	}
	return runMutationMarker(ctx, s, request, "expense.log", func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for i, x := range validated {
			key := request.Key
			if len(validated) > 1 {
				key = fmt.Sprintf("%s:%d", request.Key, i)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES(?,?,?,?,?,?,?)`, key, owner(userID), x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC().Format(time.RFC3339Nano), now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) UpdateExpenseMutation(ctx context.Context, request idempotency.Request, userID string, id, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 {
		return fmt.Errorf("amount_vnd is outside the accepted range")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	return runMutationMarker(ctx, s, request, "expense.update", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE expenses SET amount_vnd=?,category=?,description=?,occurred_at=? WHERE id=? AND user_id=?`, amount, strings.TrimSpace(category), strings.TrimSpace(description), occurredAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
		return requireChanged(result, err, "expense")
	})
}

func (s *Store) DeleteExpenseMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "expense.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM expenses WHERE id=? AND user_id=?`, id, owner(userID))
		return requireChanged(result, err, "expense")
	})
}

func (s *Store) SetBudgetMutation(ctx context.Context, request idempotency.Request, userID, period string, limit int64) error {
	if limit < 0 {
		return fmt.Errorf("budget must be >= 0")
	}
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	return runMutationMarker(ctx, s, request, "budget.set", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO budgets(user_id,period,limit_vnd,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,period) DO UPDATE SET limit_vnd=excluded.limit_vnd,updated_at=excluded.updated_at`, owner(userID), period, limit, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func (s *Store) DeleteBudgetMutation(ctx context.Context, request idempotency.Request, userID, period string) error {
	period, err := validBudgetPeriod(period)
	if err != nil {
		return err
	}
	return runMutationMarker(ctx, s, request, "budget.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM budgets WHERE user_id=? AND period=?`, owner(userID), period)
		return requireChanged(result, err, "budget")
	})
}

func (s *Store) CreateNoteMutation(ctx context.Context, request idempotency.Request, userID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	return runMutationMarker(ctx, s, request, "note.create", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES(?,?,?,?)`, request.Key, owner(userID), content, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func (s *Store) UpdateNoteMutation(ctx context.Context, request idempotency.Request, userID string, id int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	return runMutationMarker(ctx, s, request, "note.update", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE notes SET content=? WHERE id=? AND user_id=?`, content, id, owner(userID))
		return requireChanged(result, err, "note")
	})
}

func (s *Store) DeleteNoteMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "note.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE id=? AND user_id=?`, id, owner(userID))
		return requireChanged(result, err, "note")
	})
}

func (s *Store) CreateJournalMutation(ctx context.Context, request idempotency.Request, userID, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("journal content is required")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("journal occurred_at is required")
	}
	return runMutationMarker(ctx, s, request, "journal.create", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at) VALUES(?,?,?,?,?)`, request.Key, owner(userID), content, occurredAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func (s *Store) UpdateJournalMutation(ctx context.Context, request idempotency.Request, userID string, id int64, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("journal content is required")
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("journal occurred_at is required")
	}
	return runMutationMarker(ctx, s, request, "journal.update", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE journal_entries SET content=?,occurred_at=? WHERE id=? AND user_id=?`, content, occurredAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
		return requireChanged(result, err, "journal entry")
	})
}

func (s *Store) DeleteJournalMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "journal.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM journal_entries WHERE id=? AND user_id=?`, id, owner(userID))
		return requireChanged(result, err, "journal entry")
	})
}

func (s *Store) CreateReminderMutation(ctx context.Context, request idempotency.Request, userID, deviceID, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	return s.createScheduledMutation(ctx, request, "reminder.create", userID, deviceID, "reminder", title, fireAt)
}

func (s *Store) CreateTimerMutation(ctx context.Context, request idempotency.Request, userID, deviceID, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	return s.createScheduledMutation(ctx, request, "timer.create", userID, deviceID, "timer", title, fireAt)
}

func (s *Store) createScheduledMutation(ctx context.Context, request idempotency.Request, operation, userID, deviceID, kind, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.ScheduledItem{}, fmt.Errorf("scheduled item title is required")
	}
	if fireAt.IsZero() {
		return domain.ScheduledItem{}, fmt.Errorf("fire_at is required")
	}
	return runMutationValue(ctx, s, request, operation, func(tx *sql.Tx) (domain.ScheduledItem, error) {
		result, err := tx.ExecContext(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES(?,?,?,?,?,?,'pending',0,'',0,?)`, request.Key, owner(userID), strings.TrimSpace(deviceID), kind, title, fireAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return domain.ScheduledItem{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return domain.ScheduledItem{}, err
		}
		return domain.ScheduledItem{ID: id, UserID: owner(userID), DeviceID: strings.TrimSpace(deviceID), Kind: kind, Title: title, FireAt: fireAt.UTC(), Status: "pending"}, nil
	})
}

func (s *Store) UpdateScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64, title string, fireAt time.Time) error {
	title = strings.TrimSpace(title)
	if title == "" || fireAt.IsZero() {
		return fmt.Errorf("title and fire_at are required")
	}
	return runMutationMarker(ctx, s, request, "schedule.update", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE reminders SET title=?,fire_at=?,status='pending',attempts=0,next_attempt_at='',paused_remaining_seconds=0 WHERE id=? AND user_id=? AND status!='fired'`, title, fireAt.UTC().Format(time.RFC3339Nano), id, owner(userID))
		return requireChanged(result, err, "scheduled item")
	})
}

func (s *Store) PauseTimerMutation(ctx context.Context, request idempotency.Request, userID string, id int64, now time.Time) error {
	return runMutationMarker(ctx, s, request, "timer.pause", func(tx *sql.Tx) error {
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
		result, err := tx.ExecContext(ctx, `UPDATE reminders SET status='paused',paused_remaining_seconds=?,attempts=0,next_attempt_at='' WHERE id=? AND user_id=? AND kind='timer' AND status='pending'`, remaining, id, owner(userID))
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("timer pause raced with another update")
		}
		return nil
	})
}

func (s *Store) ResumeTimerMutation(ctx context.Context, request idempotency.Request, userID string, id int64, now time.Time) error {
	return runMutationMarker(ctx, s, request, "timer.resume", func(tx *sql.Tx) error {
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
		result, err := tx.ExecContext(ctx, `UPDATE reminders SET status='pending',fire_at=?,paused_remaining_seconds=0,attempts=0,next_attempt_at='' WHERE id=? AND user_id=? AND kind='timer' AND status='paused'`, fireAt.Format(time.RFC3339Nano), id, owner(userID))
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("timer resume raced with another update")
		}
		return nil
	})
}

func (s *Store) CancelScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "schedule.cancel", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE reminders SET status='cancelled' WHERE id=? AND user_id=? AND status IN ('pending','dispatching','sent','paused')`, id, owner(userID))
		return requireChanged(result, err, "scheduled item")
	})
}

func (s *Store) DeleteScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "schedule.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM reminders WHERE id=? AND user_id=?`, id, owner(userID))
		return requireChanged(result, err, "scheduled item")
	})
}

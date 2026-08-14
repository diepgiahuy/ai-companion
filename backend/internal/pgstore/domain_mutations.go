package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
	"github.com/jackc/pgx/v5"
)

func requireMutationOperation(request idempotency.Request, operation string) error {
	if strings.TrimSpace(request.Operation) != operation {
		return fmt.Errorf("idempotency operation mismatch: got %q want %q", request.Operation, operation)
	}
	return request.Validate()
}

func runMutationMarker(ctx context.Context, s *Store, request idempotency.Request, operation string, mutate func(pgx.Tx) error) error {
	if err := requireMutationOperation(request, operation); err != nil { return err }
	_, err := RunIdempotent(ctx, s.pool, request, func(tx pgx.Tx) (any, error) {
		if err := mutate(tx); err != nil { return nil, err }
		return map[string]bool{"committed": true}, nil
	})
	return err
}

func runMutationValue[T any](ctx context.Context, s *Store, request idempotency.Request, operation string, mutate func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	if err := requireMutationOperation(request, operation); err != nil { return zero, err }
	outcome, err := RunIdempotent(ctx, s.pool, request, func(tx pgx.Tx) (any, error) { return mutate(tx) })
	if err != nil { return zero, err }
	var value T
	if err := json.Unmarshal([]byte(outcome.JSON), &value); err != nil {
		return zero, fmt.Errorf("decode committed %s outcome: %w", operation, err)
	}
	return value, nil
}

func (s *Store) CreateExpenseMutation(ctx context.Context, request idempotency.Request, userID string, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 { return fmt.Errorf("amount_vnd is outside the accepted range") }
	if occurredAt.IsZero() { return fmt.Errorf("occurred_at is required") }
	category = strings.TrimSpace(category); if category == "" { category = "other" }
	description = strings.TrimSpace(description)
	return runMutationMarker(ctx, s, request, "expense.create", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, request.Key, owner(userID), amount, category, description, occurredAt.UTC(), time.Now().UTC())
		return err
	})
}

func (s *Store) CreateExpensesMutation(ctx context.Context, request idempotency.Request, userID string, items []domain.ExpenseInput) error {
	if len(items) < 1 || len(items) > 20 { return fmt.Errorf("expenses must contain between 1 and 20 items") }
	validated := append([]domain.ExpenseInput(nil), items...)
	for i := range validated {
		x := &validated[i]
		if x.AmountVND <= 0 || x.AmountVND > 1_000_000_000 { return fmt.Errorf("item %d amount_vnd is outside the accepted range", i) }
		x.Category = strings.TrimSpace(x.Category); if x.Category == "" { x.Category = "other" }
		x.Description = strings.TrimSpace(x.Description)
		if x.OccurredAt.IsZero() { return fmt.Errorf("item %d occurred_at is required", i) }
	}
	return runMutationMarker(ctx, s, request, "expense.log", func(tx pgx.Tx) error {
		now := time.Now().UTC()
		for i, x := range validated {
			key := request.Key
			if len(validated) > 1 { key = fmt.Sprintf("%s:%d", request.Key, i) }
			if _, err := tx.Exec(ctx, `INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, key, owner(userID), x.AmountVND, x.Category, x.Description, x.OccurredAt.UTC(), now); err != nil { return err }
		}
		return nil
	})
}

func (s *Store) UpdateExpenseMutation(ctx context.Context, request idempotency.Request, userID string, id, amount int64, category, description string, occurredAt time.Time) error {
	if amount <= 0 || amount > 1_000_000_000 { return fmt.Errorf("amount_vnd is outside the accepted range") }
	if occurredAt.IsZero() { return fmt.Errorf("occurred_at is required") }
	return runMutationMarker(ctx, s, request, "expense.update", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE expenses SET amount_vnd=$1,category=$2,description=$3,occurred_at=$4 WHERE id=$5 AND user_id=$6`, amount, strings.TrimSpace(category), strings.TrimSpace(description), occurredAt.UTC(), id, owner(userID))
		if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "expense")
	})
}

func (s *Store) DeleteExpenseMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "expense.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM expenses WHERE id=$1 AND user_id=$2`, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "expense")
	})
}

func (s *Store) SetBudgetMutation(ctx context.Context, request idempotency.Request, userID, period string, limit int64) error {
	if limit < 0 { return fmt.Errorf("budget must be >= 0") }
	period, err := validBudgetPeriod(period); if err != nil { return err }
	return runMutationMarker(ctx, s, request, "budget.set", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO budgets(user_id,period,limit_vnd,updated_at) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,period) DO UPDATE SET limit_vnd=EXCLUDED.limit_vnd,updated_at=EXCLUDED.updated_at`, owner(userID), period, limit, time.Now().UTC())
		return err
	})
}

func (s *Store) DeleteBudgetMutation(ctx context.Context, request idempotency.Request, userID, period string) error {
	period, err := validBudgetPeriod(period); if err != nil { return err }
	return runMutationMarker(ctx, s, request, "budget.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM budgets WHERE user_id=$1 AND period=$2`, owner(userID), period); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "budget")
	})
}

func (s *Store) CreateNoteMutation(ctx context.Context, request idempotency.Request, userID, content string) error {
	content = strings.TrimSpace(content); if content == "" { return fmt.Errorf("note content is required") }
	return runMutationMarker(ctx, s, request, "note.create", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES($1,$2,$3,$4)`, request.Key, owner(userID), content, time.Now().UTC()); return err
	})
}

func (s *Store) UpdateNoteMutation(ctx context.Context, request idempotency.Request, userID string, id int64, content string) error {
	content = strings.TrimSpace(content); if content == "" { return fmt.Errorf("note content is required") }
	return runMutationMarker(ctx, s, request, "note.update", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notes SET content=$1 WHERE id=$2 AND user_id=$3`, content, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "note")
	})
}

func (s *Store) DeleteNoteMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "note.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM notes WHERE id=$1 AND user_id=$2`, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "note")
	})
}

func (s *Store) CreateJournalMutation(ctx context.Context, request idempotency.Request, userID, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content); if content == "" { return fmt.Errorf("journal content is required") }
	if occurredAt.IsZero() { return fmt.Errorf("journal occurred_at is required") }
	return runMutationMarker(ctx, s, request, "journal.create", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journal_entries(idempotency_key,user_id,content,occurred_at,created_at) VALUES($1,$2,$3,$4,$5)`, request.Key, owner(userID), content, occurredAt.UTC(), time.Now().UTC()); return err
	})
}

func (s *Store) UpdateJournalMutation(ctx context.Context, request idempotency.Request, userID string, id int64, content string, occurredAt time.Time) error {
	content = strings.TrimSpace(content); if content == "" { return fmt.Errorf("journal content is required") }
	if occurredAt.IsZero() { return fmt.Errorf("journal occurred_at is required") }
	return runMutationMarker(ctx, s, request, "journal.update", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE journal_entries SET content=$1,occurred_at=$2 WHERE id=$3 AND user_id=$4`, content, occurredAt.UTC(), id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "journal entry")
	})
}

func (s *Store) DeleteJournalMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "journal.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM journal_entries WHERE id=$1 AND user_id=$2`, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "journal entry")
	})
}

func (s *Store) CreateReminderMutation(ctx context.Context, request idempotency.Request, userID, deviceID, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	return s.createScheduledMutation(ctx, request, "reminder.create", userID, deviceID, "reminder", title, fireAt)
}

func (s *Store) CreateTimerMutation(ctx context.Context, request idempotency.Request, userID, deviceID, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	return s.createScheduledMutation(ctx, request, "timer.create", userID, deviceID, "timer", title, fireAt)
}

func (s *Store) createScheduledMutation(ctx context.Context, request idempotency.Request, operation, userID, deviceID, kind, title string, fireAt time.Time) (domain.ScheduledItem, error) {
	title = strings.TrimSpace(title); if title == "" { return domain.ScheduledItem{}, fmt.Errorf("scheduled item title is required") }
	if fireAt.IsZero() { return domain.ScheduledItem{}, fmt.Errorf("fire_at is required") }
	return runMutationValue(ctx, s, request, operation, func(tx pgx.Tx) (domain.ScheduledItem, error) {
		created := time.Now().UTC(); var id int64
		err := tx.QueryRow(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES($1,$2,$3,$4,$5,$6,'pending',0,NULL,0,$7) RETURNING id`, request.Key, owner(userID), strings.TrimSpace(deviceID), kind, title, fireAt.UTC(), created).Scan(&id)
		if err != nil { return domain.ScheduledItem{}, err }
		return domain.ScheduledItem{ID:id, UserID:owner(userID), DeviceID:strings.TrimSpace(deviceID), Kind:kind, Title:title, FireAt:fireAt.UTC(), Status:"pending"}, nil
	})
}

func (s *Store) UpdateScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64, title string, fireAt time.Time) error {
	title = strings.TrimSpace(title); if title == "" || fireAt.IsZero() { return fmt.Errorf("title and fire_at are required") }
	return runMutationMarker(ctx, s, request, "schedule.update", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE reminders SET title=$1,fire_at=$2,status='pending',attempts=0,next_attempt_at=NULL,paused_remaining_seconds=0 WHERE id=$3 AND user_id=$4 AND status!='fired'`, title, fireAt.UTC(), id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "scheduled item")
	})
}

func (s *Store) PauseTimerMutation(ctx context.Context, request idempotency.Request, userID string, id int64, now time.Time) error {
	return runMutationMarker(ctx, s, request, "timer.pause", func(tx pgx.Tx) error {
		var kind, status string; var fireAt time.Time
		if err := tx.QueryRow(ctx, `SELECT kind,status,fire_at FROM reminders WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, owner(userID)).Scan(&kind,&status,&fireAt); err != nil {
			if err == pgx.ErrNoRows { return fmt.Errorf("timer not found") }; return err
		}
		if kind != "timer" { return fmt.Errorf("scheduled item is not a timer") }
		if status != "pending" { return fmt.Errorf("timer cannot be paused from status %q", status) }
		delta := fireAt.Sub(now); if delta <= 0 { return fmt.Errorf("timer is already due") }
		remaining := int64((delta + time.Second - 1) / time.Second)
		tag, err := tx.Exec(ctx, `UPDATE reminders SET status='paused',paused_remaining_seconds=$1,attempts=0,next_attempt_at=NULL WHERE id=$2 AND user_id=$3 AND kind='timer' AND status='pending'`, remaining,id,owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "timer")
	})
}

func (s *Store) ResumeTimerMutation(ctx context.Context, request idempotency.Request, userID string, id int64, now time.Time) error {
	return runMutationMarker(ctx, s, request, "timer.resume", func(tx pgx.Tx) error {
		var kind,status string; var remaining int64
		if err := tx.QueryRow(ctx, `SELECT kind,status,paused_remaining_seconds FROM reminders WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, owner(userID)).Scan(&kind,&status,&remaining); err != nil {
			if err == pgx.ErrNoRows { return fmt.Errorf("timer not found") }; return err
		}
		if kind != "timer" { return fmt.Errorf("scheduled item is not a timer") }
		if status != "paused" || remaining < 1 { return fmt.Errorf("timer is not paused") }
		fireAt := now.Add(time.Duration(remaining)*time.Second).UTC()
		tag, err := tx.Exec(ctx, `UPDATE reminders SET status='pending',fire_at=$1,paused_remaining_seconds=0,attempts=0,next_attempt_at=NULL WHERE id=$2 AND user_id=$3 AND kind='timer' AND status='paused'`, fireAt,id,owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "timer")
	})
}

func (s *Store) CancelScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "schedule.cancel", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE reminders SET status='cancelled' WHERE id=$1 AND user_id=$2 AND status IN ('pending','dispatching','sent','paused')`, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "scheduled item")
	})
}

func (s *Store) DeleteScheduledMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	return runMutationMarker(ctx, s, request, "schedule.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM reminders WHERE id=$1 AND user_id=$2`, id, owner(userID)); if err != nil { return err }; return requireRowsChanged(tag.RowsAffected(), "scheduled item")
	})
}

func (s *Store) ReplayVoiceMemoMutation(ctx context.Context, request idempotency.Request) (domain.VoiceMemo, bool, error) {
	if err := requireMutationOperation(request, "voice_memo.save"); err != nil { return domain.VoiceMemo{}, false, err }
	var storedHash string; var outcome []byte
	err := s.pool.QueryRow(ctx, `SELECT request_hash,outcome_json FROM idempotency_records WHERE actor_id=$1 AND operation=$2 AND idempotency_key=$3`, request.Actor, request.Operation, request.Key).Scan(&storedHash,&outcome)
	if err == pgx.ErrNoRows { return domain.VoiceMemo{}, false, nil }
	if err != nil { return domain.VoiceMemo{}, false, err }
	if !idempotency.EqualHash(storedHash, request.RequestHash) { return domain.VoiceMemo{}, false, idempotency.Conflict{Operation:request.Operation,Key:request.Key} }
	canonical, err := canonicalOutcomeJSON(outcome); if err != nil { return domain.VoiceMemo{}, false, err }
	var memo domain.VoiceMemo
	if err := json.Unmarshal([]byte(canonical), &memo); err != nil { return domain.VoiceMemo{}, false, fmt.Errorf("decode committed voice memo outcome: %w", err) }
	return memo, true, nil
}

func (s *Store) CreateVoiceMemoMutation(ctx context.Context, request idempotency.Request, userID, deviceID, path, transcript string, durationMS int64) (domain.VoiceMemo, error) {
	path = strings.TrimSpace(path); if path == "" { return domain.VoiceMemo{}, fmt.Errorf("voice memo path is required") }
	if durationMS < 0 { return domain.VoiceMemo{}, fmt.Errorf("voice memo duration must be non-negative") }
	return runMutationValue(ctx,s,request,"voice_memo.save",func(tx pgx.Tx)(domain.VoiceMemo,error){
		created:=time.Now().UTC(); var id int64
		err:=tx.QueryRow(ctx,`INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,request.Key,owner(userID),strings.TrimSpace(deviceID),path,strings.TrimSpace(transcript),durationMS,created).Scan(&id)
		if err!=nil{return domain.VoiceMemo{},err}
		return domain.VoiceMemo{ID:id,UserID:owner(userID),DeviceID:strings.TrimSpace(deviceID),Path:path,Transcript:strings.TrimSpace(transcript),DurationMS:durationMS,CreatedAt:created},nil
	})
}

func (s *Store) DeleteVoiceMemoMutation(ctx context.Context, request idempotency.Request, userID string, id int64) (domain.VoiceMemo, error) {
	if id < 1 { return domain.VoiceMemo{}, fmt.Errorf("voice memo id is required") }
	return runMutationValue(ctx,s,request,"voice_memo.delete",func(tx pgx.Tx)(domain.VoiceMemo,error){
		var memo domain.VoiceMemo
		if err:=tx.QueryRow(ctx,`SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE id=$1 AND user_id=$2 FOR UPDATE`,id,owner(userID)).Scan(&memo.ID,&memo.UserID,&memo.DeviceID,&memo.Path,&memo.Transcript,&memo.DurationMS,&memo.CreatedAt);err!=nil{
			if err==pgx.ErrNoRows{return domain.VoiceMemo{},fmt.Errorf("voice memo not found")};return domain.VoiceMemo{},err
		}
		tag,err:=tx.Exec(ctx,`DELETE FROM voice_memos WHERE id=$1 AND user_id=$2`,id,owner(userID));if err!=nil{return domain.VoiceMemo{},err};if err:=requireRowsChanged(tag.RowsAffected(),"voice memo");err!=nil{return domain.VoiceMemo{},err};memo.CreatedAt=memo.CreatedAt.UTC();return memo,nil
	})
}

var _ domain.DurableRepositories = (*Store)(nil)

package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/events"
	"companion-server/internal/idempotency"
	"companion-server/internal/market"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Enqueue(ctx context.Context, event events.Event) error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Source) == "" || strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("outbox event id source and type are required")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	data := event.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	if !json.Valid(data) {
		return fmt.Errorf("outbox data must be valid JSON")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,status,attempts,next_attempt_at,last_error) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,'pending',0,$7,'') ON CONFLICT(event_id) DO NOTHING`, event.ID, event.Source, event.Type, event.Subject, owner(event.UserID), string(data), event.Time.UTC())
	return err
}

func (s *Store) Claim(ctx context.Context, now time.Time, limit int) ([]events.Pending, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,event_id,source,event_type,subject,user_id,data_json,occurred_at,attempts,next_attempt_at FROM outbox WHERE status='pending' AND next_attempt_at<=$1 ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	var out []events.Pending
	var ids []int64
	for rows.Next() {
		var pending events.Pending
		var raw []byte
		if err := rows.Scan(&pending.RowID, &pending.Event.ID, &pending.Event.Source, &pending.Event.Type, &pending.Event.Subject, &pending.Event.UserID, &raw, &pending.Event.Time, &pending.Attempts, &pending.NextAttempt); err != nil {
			rows.Close()
			return nil, err
		}
		pending.Event.Time = pending.Event.Time.UTC()
		pending.NextAttempt = pending.NextAttempt.UTC()
		pending.Event.Data = append(json.RawMessage(nil), raw...)
		out = append(out, pending)
		ids = append(ids, pending.RowID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE outbox SET status='dispatching',attempts=attempts+1 WHERE id=ANY($1)`, ids); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MarkSent(ctx context.Context, rowID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET status='sent',last_error='' WHERE id=$1 AND status='dispatching'`, rowID)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "outbox event")
}

func (s *Store) Retry(ctx context.Context, rowID int64, lastError string, next time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET status='pending',last_error=$1,next_attempt_at=$2 WHERE id=$3 AND status='dispatching'`, strings.TrimSpace(lastError), next.UTC(), rowID)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "outbox event")
}

func (s *Store) RecoverOutbox(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET status='pending' WHERE status='dispatching'`)
	return err
}

func (s *Store) CreateMarketWatch(ctx context.Context, userID, deviceID, key, provider, symbol, currency, operator string, threshold float64) (market.Watch, error) {
	if err := market.ValidateOperator(operator); err != nil {
		return market.Watch{}, err
	}
	userID = owner(userID)
	deviceID = strings.TrimSpace(deviceID)
	key = strings.TrimSpace(key)
	provider = strings.TrimSpace(provider)
	symbol = strings.TrimSpace(symbol)
	currency = strings.TrimSpace(currency)
	if key == "" || provider == "" || symbol == "" || currency == "" || threshold <= 0 {
		return market.Watch{}, fmt.Errorf("idempotency key, provider, symbol, currency and positive threshold are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return market.Watch{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockLegacyIdentity(ctx, tx, "market_watches", userID, key); err != nil {
		return market.Watch{}, err
	}
	var existing market.Watch
	err = tx.QueryRow(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE user_id=$1 AND idempotency_key=$2 ORDER BY id LIMIT 1`, userID, key).Scan(&existing.ID, &existing.UserID, &existing.DeviceID, &existing.Provider, &existing.Symbol, &existing.Currency, &existing.Operator, &existing.Threshold, &existing.Enabled, &existing.LastState, &existing.CreatedAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return market.Watch{}, err
		}
		existing.CreatedAt = existing.CreatedAt.UTC()
		return existing, nil
	}
	if err != pgx.ErrNoRows {
		return market.Watch{}, err
	}
	created := time.Now().UTC()
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,false,$9) RETURNING id`, key, userID, deviceID, provider, symbol, currency, operator, threshold, created).Scan(&id)
	if err != nil {
		return market.Watch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return market.Watch{}, err
	}
	return market.Watch{ID: id, UserID: userID, DeviceID: deviceID, Provider: provider, Symbol: symbol, Currency: currency, Operator: operator, Threshold: threshold, Enabled: true, CreatedAt: created}, nil
}

func (s *Store) CreateMarketWatchMutation(ctx context.Context, request idempotency.Request, userID, deviceID, provider, symbol, currency, operator string, threshold float64) (market.Watch, error) {
	if err := market.ValidateOperator(operator); err != nil {
		return market.Watch{}, err
	}
	userID = owner(userID)
	deviceID = strings.TrimSpace(deviceID)
	provider = strings.TrimSpace(provider)
	symbol = strings.TrimSpace(symbol)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if provider == "" || symbol == "" || currency == "" || threshold <= 0 {
		return market.Watch{}, fmt.Errorf("provider, symbol, currency and positive threshold are required")
	}
	return runMutationValue(ctx, s, request, "market.watch.create", func(tx pgx.Tx) (market.Watch, error) {
		created := time.Now().UTC()
		watch := market.Watch{UserID: userID, DeviceID: deviceID, Provider: provider, Symbol: symbol, Currency: currency, Operator: operator, Threshold: threshold, Enabled: true, CreatedAt: created}
		if err := tx.QueryRow(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,false,$9) RETURNING id`, request.Key, userID, deviceID, provider, symbol, currency, operator, threshold, created).Scan(&watch.ID); err != nil {
			return market.Watch{}, err
		}
		return watch, nil
	})
}

func (s *Store) ListMarketWatches(ctx context.Context, userID, deviceID string, limit int) ([]market.Watch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE user_id=$1`
	args := []any{owner(userID)}
	if strings.TrimSpace(deviceID) != "" {
		query += ` AND device_id=$2 ORDER BY id DESC LIMIT $3`
		args = append(args, strings.TrimSpace(deviceID), limit)
	} else {
		query += ` ORDER BY id DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWatches(rows)
}

func scanWatches(rows pgx.Rows) ([]market.Watch, error) {
	var out []market.Watch
	for rows.Next() {
		var watch market.Watch
		if err := rows.Scan(&watch.ID, &watch.UserID, &watch.DeviceID, &watch.Provider, &watch.Symbol, &watch.Currency, &watch.Operator, &watch.Threshold, &watch.Enabled, &watch.LastState, &watch.CreatedAt); err != nil {
			return nil, err
		}
		watch.CreatedAt = watch.CreatedAt.UTC()
		out = append(out, watch)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMarketWatch(ctx context.Context, userID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM market_watches WHERE id=$1 AND user_id=$2`, id, owner(userID))
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "market watch")
}

func (s *Store) DeleteMarketWatchMutation(ctx context.Context, request idempotency.Request, userID string, id int64) error {
	if id < 1 {
		return fmt.Errorf("market watch id is required")
	}
	return runMutationMarker(ctx, s, request, "market.watch.delete", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM market_watches WHERE id=$1 AND user_id=$2`, id, owner(userID))
		if err != nil {
			return err
		}
		return requireRowsChanged(tag.RowsAffected(), "market watch")
	})
}

func (s *Store) EnabledMarketWatches(ctx context.Context, limit int) ([]market.Watch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE enabled=true ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWatches(rows)
}

func (s *Store) SetMarketWatchState(ctx context.Context, id int64, state bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE market_watches SET last_state=$1 WHERE id=$2`, state, id)
	return err
}

func (s *Store) TriggerMarketWatch(ctx context.Context, watch market.Watch, title string, fireAt time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE market_watches SET last_state=true WHERE id=$1 AND user_id=$2 AND enabled=true AND last_state=false`, watch.ID, owner(watch.UserID))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	key := fmt.Sprintf("market-watch:%d:cross:%d", watch.ID, fireAt.UTC().UnixNano())
	_, err = tx.Exec(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES($1,$2,$3,'reminder',$4,$5,'pending',0,NULL,0,$6)`, key, owner(watch.UserID), strings.TrimSpace(watch.DeviceID), strings.TrimSpace(title), fireAt.UTC(), time.Now().UTC())
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) RecoverDispatchingReminders(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET status='pending' WHERE status='dispatching'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ClaimDueReminders(ctx context.Context, now time.Time, limit int) ([]domain.ScheduledItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 32
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,COALESCE(next_attempt_at,'epoch'::timestamptz),next_attempt_at IS NOT NULL,paused_remaining_seconds FROM reminders WHERE ((status='pending' AND fire_at<=$1) OR (status='sent' AND next_attempt_at IS NOT NULL AND next_attempt_at<=$1)) ORDER BY COALESCE(next_attempt_at,fire_at),id LIMIT $2 FOR UPDATE SKIP LOCKED`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	items, err := scanScheduled(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		ids := make([]int64, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE reminders SET status='dispatching' WHERE id=ANY($1)`, ids); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) MarkReminderSent(ctx context.Context, id int64, next time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET status='sent',attempts=attempts+1,next_attempt_at=$1 WHERE id=$2 AND status='dispatching'`, next.UTC(), id)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "reminder")
}

func (s *Store) ReleaseReminder(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET status='pending',next_attempt_at=NULL WHERE id=$1 AND status='dispatching'`, id)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "reminder")
}

func (s *Store) AcknowledgeReminder(ctx context.Context, userID, deviceID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE reminders SET status='fired',next_attempt_at=NULL WHERE id=$1 AND user_id=$2 AND (device_id=$3 OR device_id='') AND status IN ('dispatching','sent')`, id, owner(userID), strings.TrimSpace(deviceID))
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "reminder")
}

func (s *Store) NextReminder(ctx context.Context, userID, deviceID string, now time.Time) (domain.ScheduledItem, bool, error) {
	var item domain.ScheduledItem
	var nextAttempt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds FROM reminders WHERE user_id=$1 AND (device_id=$2 OR device_id='') AND status IN ('pending','sent') AND fire_at>=$3 ORDER BY fire_at,id LIMIT 1`, owner(userID), strings.TrimSpace(deviceID), now.UTC()).Scan(&item.ID, &item.UserID, &item.DeviceID, &item.Kind, &item.Title, &item.FireAt, &item.Status, &item.Attempts, &nextAttempt, &item.PausedRemainingSeconds)
	if err == pgx.ErrNoRows {
		return item, false, nil
	}
	if err != nil {
		return item, false, err
	}
	item.FireAt = item.FireAt.UTC()
	if nextAttempt != nil {
		value := nextAttempt.UTC()
		item.NextAttempt = &value
	}
	return item, true, nil
}

var _ events.Outbox = (*Store)(nil)
var _ market.WatchRepository = (*Store)(nil)
var _ market.DurableWatchRepository = (*Store)(nil)

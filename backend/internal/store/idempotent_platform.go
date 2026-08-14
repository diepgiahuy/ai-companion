package store

import (
	"companion-server/internal/idempotency"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) UpsertMemoryMutation(ctx context.Context, request idempotency.Request, m memory.Item) (memory.Item, error) {
	if m.UserID == "" || strings.TrimSpace(m.Key) == "" || strings.TrimSpace(m.Value) == "" {
		return m, fmt.Errorf("user, key and value required")
	}
	return runMutationValue(ctx, s, request, "memory.remember", func(tx *sql.Tx) (memory.Item, error) {
		m.Key, m.Value = strings.TrimSpace(m.Key), strings.TrimSpace(m.Value)
		emb, _ := json.Marshal(m.Embedding)
		vf := m.ValidFrom.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE memories SET valid_to=? WHERE user_id=? AND memory_key=? AND valid_to IS NULL AND deleted_at IS NULL`, vf, m.UserID, m.Key); err != nil {
			return m, err
		}
		created := m.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO memories(user_id,memory_key,kind,value,valid_from,source,confidence,embedding,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.UserID, m.Key, string(m.Kind), m.Value, vf, m.Source, m.Confidence, string(emb), created.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return m, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return m, err
		}
		m.ID, m.CreatedAt = id, created.UTC()
		return m, nil
	})
}

func (s *Store) ForgetMemoryMutation(ctx context.Context, request idempotency.Request, user, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("memory key is required")
	}
	return runMutationMarker(ctx, s, request, "memory.forget", func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_at=?,valid_to=COALESCE(valid_to,?) WHERE user_id=? AND memory_key=? AND deleted_at IS NULL`, now, now, user, key)
		if err != nil {
			return err
		}
		return requireChanged(result, nil, "memory key")
	})
}

func (s *Store) CreateMarketWatchMutation(ctx context.Context, request idempotency.Request, user, device, provider, symbol, currency, operator string, threshold float64) (market.Watch, error) {
	if err := market.ValidateOperator(operator); err != nil {
		return market.Watch{}, err
	}
	if threshold <= 0 {
		return market.Watch{}, fmt.Errorf("threshold must be positive")
	}
	provider, symbol, currency, device = strings.TrimSpace(provider), strings.TrimSpace(symbol), strings.ToUpper(strings.TrimSpace(currency)), strings.TrimSpace(device)
	return runMutationValue(ctx, s, request, "market.watch.create", func(tx *sql.Tx) (market.Watch, error) {
		created := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, request.Key, user, device, provider, symbol, currency, operator, threshold, created.Format(time.RFC3339Nano))
		if err != nil {
			return market.Watch{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return market.Watch{}, err
		}
		return market.Watch{ID: id, UserID: user, DeviceID: device, Provider: provider, Symbol: symbol, Currency: currency, Operator: operator, Threshold: threshold, Enabled: true, CreatedAt: created}, nil
	})
}

func (s *Store) DeleteMarketWatchMutation(ctx context.Context, request idempotency.Request, user string, id int64) error {
	if id < 1 {
		return fmt.Errorf("market watch id is required")
	}
	return runMutationMarker(ctx, s, request, "market.watch.delete", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM market_watches WHERE id=? AND user_id=?`, id, user)
		return requireChanged(result, err, "market watch")
	})
}

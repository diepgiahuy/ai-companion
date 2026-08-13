package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/idempotency"
)

type idempotentOutcome struct {
	JSON     string
	Replayed bool
}

func (s *Store) runIdempotentMutation(ctx context.Context, request idempotency.Request, mutate func(*sql.Tx) (any, error)) (idempotentOutcome, error) {
	if err := s.migrateIdempotency(); err != nil {
		return idempotentOutcome{}, err
	}
	request.Actor = strings.TrimSpace(request.Actor)
	request.Operation = strings.TrimSpace(request.Operation)
	request.Key = strings.TrimSpace(request.Key)
	if err := request.Validate(); err != nil {
		return idempotentOutcome{}, err
	}
	if mutate == nil {
		return idempotentOutcome{}, fmt.Errorf("idempotent mutation callback is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return idempotentOutcome{}, err
	}
	defer tx.Rollback()

	storedHash, storedOutcome, found, err := idempotencyRecord(ctx, tx, request)
	if err != nil {
		return idempotentOutcome{}, err
	}
	if found {
		if !idempotency.EqualHash(storedHash, request.RequestHash) {
			return idempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}
		}
		return idempotentOutcome{JSON: storedOutcome, Replayed: true}, nil
	}
	value, err := mutate(tx)
	if err != nil {
		return idempotentOutcome{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return idempotentOutcome{}, fmt.Errorf("encode idempotency outcome: %w", err)
	}
	outcome := string(encoded)
	_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_records(actor_id,operation,idempotency_key,request_hash,outcome_json,created_at) VALUES(?,?,?,?,?,?)`, request.Actor, request.Operation, request.Key, strings.ToLower(request.RequestHash), outcome, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return idempotentOutcome{}, fmt.Errorf("commit idempotency record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return idempotentOutcome{}, err
	}
	return idempotentOutcome{JSON: outcome}, nil
}

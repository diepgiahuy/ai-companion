package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"companion-server/internal/idempotency"
)

const idempotencyLedgerDDL = `CREATE TABLE IF NOT EXISTS idempotency_records (
    actor_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    outcome_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(actor_id,operation,idempotency_key)
)`

func (s *Store) migrateIdempotency() error {
	if _, err := s.db.Exec(idempotencyLedgerDDL); err != nil {
		return fmt.Errorf("idempotency ledger migration: %w", err)
	}
	return nil
}

func idempotencyRecord(ctx context.Context, tx *sql.Tx, request idempotency.Request) (hash, outcome string, found bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT request_hash,outcome_json FROM idempotency_records WHERE actor_id=? AND operation=? AND idempotency_key=?`, request.Actor, request.Operation, request.Key).Scan(&hash, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return hash, outcome, true, nil
}

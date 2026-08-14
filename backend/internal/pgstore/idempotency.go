package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/idempotency"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IdempotentOutcome struct {
	JSON     string
	Replayed bool
}

// RunIdempotent serializes one actor/operation/client-key identity with a
// transaction-scoped advisory lock before reading or mutating. This prevents
// two concurrent first attempts from both executing the domain mutation before
// one loses the ledger primary-key race.
func RunIdempotent(
	ctx context.Context,
	pool *pgxpool.Pool,
	request idempotency.Request,
	mutate func(pgx.Tx) (any, error),
) (IdempotentOutcome, error) {
	if pool == nil {
		return IdempotentOutcome{}, fmt.Errorf("PostgreSQL pool is required")
	}
	request.Actor = strings.TrimSpace(request.Actor)
	request.Operation = strings.TrimSpace(request.Operation)
	request.Key = strings.TrimSpace(request.Key)
	if err := request.Validate(); err != nil {
		return IdempotentOutcome{}, err
	}
	if mutate == nil {
		return IdempotentOutcome{}, fmt.Errorf("idempotent mutation callback is required")
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IdempotentOutcome{}, fmt.Errorf("begin PostgreSQL idempotency transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryKey(request)); err != nil {
		return IdempotentOutcome{}, fmt.Errorf("lock PostgreSQL idempotency identity: %w", err)
	}

	var storedHash string
	var storedOutcome []byte
	err = tx.QueryRow(ctx,
		`SELECT request_hash, outcome_json FROM idempotency_records WHERE actor_id=$1 AND operation=$2 AND idempotency_key=$3`,
		request.Actor, request.Operation, request.Key,
	).Scan(&storedHash, &storedOutcome)
	switch err {
	case nil:
		if !idempotency.EqualHash(storedHash, request.RequestHash) {
			return IdempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}
		}
		return IdempotentOutcome{JSON: string(storedOutcome), Replayed: true}, nil
	case pgx.ErrNoRows:
	default:
		return IdempotentOutcome{}, fmt.Errorf("read PostgreSQL idempotency record: %w", err)
	}

	var reserved int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM legacy_idempotency_reservations WHERE operation=$1 AND idempotency_key=$2`,
		request.Operation, request.Key,
	).Scan(&reserved)
	if err == nil {
		return IdempotentOutcome{}, idempotency.Conflict{Operation: request.Operation, Key: request.Key}
	}
	if err != pgx.ErrNoRows {
		return IdempotentOutcome{}, fmt.Errorf("read PostgreSQL legacy idempotency reservation: %w", err)
	}

	value, err := mutate(tx)
	if err != nil {
		return IdempotentOutcome{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return IdempotentOutcome{}, fmt.Errorf("encode PostgreSQL idempotency outcome: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO idempotency_records(actor_id,operation,idempotency_key,request_hash,outcome_json,created_at)
		VALUES($1,$2,$3,$4,$5::jsonb,$6)`,
		request.Actor, request.Operation, request.Key, strings.ToLower(request.RequestHash), string(encoded), time.Now().UTC(),
	); err != nil {
		return IdempotentOutcome{}, fmt.Errorf("insert PostgreSQL idempotency record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IdempotentOutcome{}, fmt.Errorf("commit PostgreSQL idempotency transaction: %w", err)
	}
	return IdempotentOutcome{JSON: string(encoded)}, nil
}

func advisoryKey(request idempotency.Request) int64 {
	sum := sha256.Sum256([]byte(request.Actor + "\x00" + request.Operation + "\x00" + request.Key))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

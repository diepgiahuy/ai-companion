package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxListLimit = 100

// Store is the PostgreSQL implementation boundary used by domain repositories.
// Atlas owns schema migration; Store never creates or mutates schema.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func OpenStore(ctx context.Context, config PoolConfig) (*Store, error) {
	pool, err := Open(ctx, config)
	if err != nil {
		return nil, err
	}
	store, err := New(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Pool() *pgxpool.Pool {
	if s == nil {
		return nil
	}
	return s.pool
}

func owner(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	return value
}

func boundedLimit(limit int) int {
	if limit <= 0 || limit > maxListLimit {
		return maxListLimit
	}
	return limit
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

func requireRowsChanged(rows int64, label string) error {
	if rows != 1 {
		return fmt.Errorf("%s not found or not mutable", label)
	}
	return nil
}

// lockLegacyIdentity serializes the legacy non-ledger create methods by their
// user-visible idempotency key. Product tool mutations use RunIdempotent; this
// helper only preserves the old repository contract without a second ledger.
func lockLegacyIdentity(ctx context.Context, tx pgx.Tx, namespace, userID, key string) error {
	sum := sha256.Sum256([]byte(namespace + "\x00" + owner(userID) + "\x00" + strings.TrimSpace(key)))
	lock := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
		return fmt.Errorf("lock PostgreSQL legacy repository identity: %w", err)
	}
	return nil
}

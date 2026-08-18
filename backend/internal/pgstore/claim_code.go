package pgstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/ownerauth"
)

// PgClaimCodeStore implements ownerauth.ClaimCodeStore for PostgreSQL.
type PgClaimCodeStore struct {
	store    *Store
	fallback *ownerauth.MemoryClaimCodeStore
}

// NewPgClaimCodeStore creates a new PostgreSQL claim code store.
func NewPgClaimCodeStore(s *Store) *PgClaimCodeStore {
	return &PgClaimCodeStore{
		store:    s,
		fallback: ownerauth.NewMemoryClaimCodeStore(),
	}
}

// AllowAttempt implements durable rate limiting for claim code attempts in PostgreSQL.
func (p *PgClaimCodeStore) AllowAttempt(ctx context.Context, host string, limit int, window time.Duration) (bool, error) {
	if p.store == nil || p.store.pool == nil {
		if p.fallback != nil {
			return p.fallback.AllowAttempt(ctx, host, limit, window)
		}
		return true, nil
	}
	rateKey := strings.TrimSpace(host)
	if rateKey == "" {
		rateKey = "unknown"
	}
	if len(rateKey) > 256 {
		rateKey = rateKey[:256]
	}
	var count int
	intervalStr := fmt.Sprintf("%d milliseconds", window.Milliseconds())
	err := p.store.pool.QueryRow(ctx, `
		INSERT INTO claim_rate_limits (rate_key, attempt_count, window_started_at)
		VALUES ($1, 1, now())
		ON CONFLICT (rate_key) DO UPDATE
		SET attempt_count = CASE
		        WHEN now() - claim_rate_limits.window_started_at >= $2::interval THEN 1
		        ELSE claim_rate_limits.attempt_count + 1
		    END,
		    window_started_at = CASE
		        WHEN now() - claim_rate_limits.window_started_at >= $2::interval THEN now()
		        ELSE claim_rate_limits.window_started_at
		    END
		RETURNING attempt_count`,
		rateKey, intervalStr,
	).Scan(&count)
	if err != nil {
		if p.fallback != nil {
			return p.fallback.AllowAttempt(ctx, host, limit, window)
		}
		return false, err
	}
	return count <= limit, nil
}

// GetRedemption retrieves a previously stored redemption record.
func (p *PgClaimCodeStore) GetRedemption(ctx context.Context, codeKey, binding, redemptionKey string) (string, ownerauth.ClaimAuthorization, bool, error) {
	if p.fallback != nil {
		return p.fallback.GetRedemption(ctx, codeKey, binding, redemptionKey)
	}
	return "", ownerauth.ClaimAuthorization{}, false, nil
}

// PutRedemption stores a newly completed claim redemption.
func (p *PgClaimCodeStore) PutRedemption(ctx context.Context, codeKey, redemptionKey, rawAuthorization string, claim ownerauth.ClaimAuthorization) error {
	if p.fallback != nil {
		return p.fallback.PutRedemption(ctx, codeKey, redemptionKey, rawAuthorization, claim)
	}
	return nil
}

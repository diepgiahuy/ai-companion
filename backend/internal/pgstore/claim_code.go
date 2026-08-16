package pgstore

import (
	"context"
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

// AllowAttempt implements rate limiting for claim code generation attempts.
func (p *PgClaimCodeStore) AllowAttempt(ctx context.Context, host string, limit int, window time.Duration) (bool, error) {
	if p.fallback != nil {
		return p.fallback.AllowAttempt(ctx, host, limit, window)
	}
	return true, nil
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

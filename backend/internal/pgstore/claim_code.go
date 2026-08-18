package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/ownerauth"

	"github.com/jackc/pgx/v5"
)

const claimCodeRedemptionOperation = "owner.claim-code.redeem"

// PgClaimCodeStore implements ownerauth.ClaimCodeStore for PostgreSQL.
// Production callers fail closed when PostgreSQL is unavailable. There is no
// process-local fallback because rate limits and redemption retries must remain
// consistent across backend instances.
type PgClaimCodeStore struct {
	store *Store
}

func NewPgClaimCodeStore(s *Store) *PgClaimCodeStore {
	return &PgClaimCodeStore{store: s}
}

func (p *PgClaimCodeStore) available() bool {
	return p != nil && p.store != nil && p.store.pool != nil
}

// AllowAttempt implements durable rate limiting for claim code attempts in PostgreSQL.
func (p *PgClaimCodeStore) AllowAttempt(ctx context.Context, host string, limit int, window time.Duration) (bool, error) {
	if !p.available() {
		return false, fmt.Errorf("postgres claim-code store unavailable")
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
		return false, err
	}
	return count <= limit, nil
}

type claimCodeRedemptionOutcome struct {
	RawAuthorization string    `json:"raw_authorization"`
	UserID           string    `json:"user_id"`
	BootstrapID      string    `json:"bootstrap_id"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func claimCodeActor(codeKey string) string {
	return "claim-code:" + strings.TrimSpace(codeKey)
}

func claimCodeBindingHash(binding string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(binding)))
	return hex.EncodeToString(digest[:])
}

// GetRedemption retrieves a previously stored redemption record from the
// canonical actor-scoped idempotency table.
func (p *PgClaimCodeStore) GetRedemption(ctx context.Context, codeKey, binding, redemptionKey string) (string, ownerauth.ClaimAuthorization, bool, error) {
	if !p.available() {
		return "", ownerauth.ClaimAuthorization{}, false, fmt.Errorf("postgres claim-code store unavailable")
	}
	codeKey = strings.TrimSpace(codeKey)
	binding = strings.TrimSpace(binding)
	redemptionKey = strings.TrimSpace(redemptionKey)
	if codeKey == "" || binding == "" || redemptionKey == "" {
		return "", ownerauth.ClaimAuthorization{}, false, nil
	}

	var requestHash string
	var outcomeJSON []byte
	err := p.store.pool.QueryRow(ctx, `
		SELECT request_hash, outcome_json
		FROM idempotency_records
		WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3`,
		claimCodeActor(codeKey), claimCodeRedemptionOperation, redemptionKey,
	).Scan(&requestHash, &outcomeJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ownerauth.ClaimAuthorization{}, false, nil
	}
	if err != nil {
		return "", ownerauth.ClaimAuthorization{}, false, err
	}
	if requestHash != claimCodeBindingHash(binding) {
		return "", ownerauth.ClaimAuthorization{}, false, nil
	}
	var outcome claimCodeRedemptionOutcome
	if err := json.Unmarshal(outcomeJSON, &outcome); err != nil {
		return "", ownerauth.ClaimAuthorization{}, false, err
	}
	if outcome.RawAuthorization == "" || outcome.UserID == "" || outcome.BootstrapID != binding || !outcome.ExpiresAt.After(time.Now().UTC()) {
		return "", ownerauth.ClaimAuthorization{}, false, nil
	}
	claim := ownerauth.ClaimAuthorization{
		UserID:      outcome.UserID,
		BootstrapID: outcome.BootstrapID,
		ExpiresAt:   outcome.ExpiresAt,
	}
	return outcome.RawAuthorization, claim, true, nil
}

// PutRedemption stores a newly completed claim redemption with actor-scoped
// idempotency. A conflicting replay is rejected rather than overwritten.
func (p *PgClaimCodeStore) PutRedemption(ctx context.Context, codeKey, redemptionKey, rawAuthorization string, claim ownerauth.ClaimAuthorization) error {
	if !p.available() {
		return fmt.Errorf("postgres claim-code store unavailable")
	}
	codeKey = strings.TrimSpace(codeKey)
	redemptionKey = strings.TrimSpace(redemptionKey)
	rawAuthorization = strings.TrimSpace(rawAuthorization)
	claim.UserID = strings.TrimSpace(claim.UserID)
	claim.BootstrapID = strings.TrimSpace(claim.BootstrapID)
	if codeKey == "" || redemptionKey == "" || rawAuthorization == "" || claim.UserID == "" || claim.BootstrapID == "" || claim.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid claim-code redemption")
	}
	outcomeJSON, err := json.Marshal(claimCodeRedemptionOutcome{
		RawAuthorization: rawAuthorization,
		UserID:           claim.UserID,
		BootstrapID:      claim.BootstrapID,
		ExpiresAt:        claim.ExpiresAt,
	})
	if err != nil {
		return err
	}
	requestHash := claimCodeBindingHash(claim.BootstrapID)
	tag, err := p.store.pool.Exec(ctx, `
		INSERT INTO idempotency_records(actor_id, operation, idempotency_key, request_hash, outcome_json, created_at)
		VALUES($1, $2, $3, $4, $5::jsonb, now())
		ON CONFLICT (actor_id, operation, idempotency_key) DO NOTHING`,
		claimCodeActor(codeKey), claimCodeRedemptionOperation, redemptionKey, requestHash, string(outcomeJSON),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	raw, existing, found, err := p.GetRedemption(ctx, codeKey, claim.BootstrapID, redemptionKey)
	if err != nil {
		return err
	}
	if !found || raw != rawAuthorization || existing.UserID != claim.UserID || !existing.ExpiresAt.Equal(claim.ExpiresAt) {
		return fmt.Errorf("conflicting claim-code redemption replay")
	}
	return nil
}

package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/ownerauth"

	"github.com/jackc/pgx/v5"
)

// PgClaimSessionStore implements ownerauth.ClaimSessionStore backed by PostgreSQL.
type PgClaimSessionStore struct {
	store *Store
}

func NewPgClaimSessionStore(s *Store) *PgClaimSessionStore {
	return &PgClaimSessionStore{store: s}
}

func (p *PgClaimSessionStore) CreateSession(ctx context.Context, session ownerauth.ClaimSessionRecord) error {
	if p.store == nil || p.store.pool == nil {
		return fmt.Errorf("postgres store not available")
	}
	return p.store.CreateClaimSession(ctx, session)
}

func (p *PgClaimSessionStore) GetSessionByID(ctx context.Context, sessionID string) (ownerauth.ClaimSessionRecord, error) {
	if p.store == nil || p.store.pool == nil {
		return ownerauth.ClaimSessionRecord{}, fmt.Errorf("postgres store not available")
	}
	return p.store.GetClaimSessionByID(ctx, sessionID)
}

func (p *PgClaimSessionStore) GetSessionByDeviceCodeHash(ctx context.Context, deviceCodeHash string) (ownerauth.ClaimSessionRecord, error) {
	if p.store == nil || p.store.pool == nil {
		return ownerauth.ClaimSessionRecord{}, fmt.Errorf("postgres store not available")
	}
	return p.store.GetClaimSessionByDeviceCodeHash(ctx, deviceCodeHash)
}

func (p *PgClaimSessionStore) ApproveSession(ctx context.Context, sessionID, ownerUserID string, now time.Time) error {
	if p.store == nil || p.store.pool == nil {
		return fmt.Errorf("postgres store not available")
	}
	return p.store.ApproveClaimSession(ctx, sessionID, ownerUserID, now)
}

func (p *PgClaimSessionStore) DenySession(ctx context.Context, sessionID string, now time.Time) error {
	if p.store == nil || p.store.pool == nil {
		return fmt.Errorf("postgres store not available")
	}
	return p.store.DenyClaimSession(ctx, sessionID, now)
}

func (p *PgClaimSessionStore) PollSession(
	ctx context.Context,
	deviceCodeHash string,
	minInterval time.Duration,
	now time.Time,
	mintAuthFn func(bootstrapID, deviceID, ownerUserID string) (string, time.Time, error),
) (ownerauth.PollOutcome, error) {
	if p.store == nil || p.store.pool == nil {
		return ownerauth.PollOutcome{}, fmt.Errorf("postgres store not available")
	}
	return p.store.PollClaimSession(ctx, deviceCodeHash, minInterval, now, mintAuthFn)
}

func (s *Store) CreateClaimSession(ctx context.Context, session ownerauth.ClaimSessionRecord) error {
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.DeviceID = strings.TrimSpace(session.DeviceID)
	session.BootstrapID = strings.TrimSpace(session.BootstrapID)
	session.DeviceCodeHash = strings.TrimSpace(session.DeviceCodeHash)
	session.UserCodeHash = strings.TrimSpace(session.UserCodeHash)
	session.UserCodePlain = strings.TrimSpace(session.UserCodePlain)

	if session.SessionID == "" || session.DeviceID == "" || session.BootstrapID == "" ||
		len(session.DeviceCodeHash) != 64 || len(session.UserCodeHash) != 64 || session.UserCodePlain == "" {
		return errors.New("invalid claim session record")
	}
	if session.Status == "" {
		session.Status = ownerauth.ClaimSessionPending
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_claim_sessions (
			session_id, device_id, bootstrap_id, device_code_hash, user_code_hash, user_code_plain,
			status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		session.SessionID,
		session.DeviceID,
		session.BootstrapID,
		session.DeviceCodeHash,
		session.UserCodeHash,
		session.UserCodePlain,
		string(session.Status),
		session.ExpiresAt,
	)
	return err
}

func (s *Store) GetClaimSessionByID(ctx context.Context, sessionID string) (ownerauth.ClaimSessionRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ownerauth.ClaimSessionRecord{}, ownerauth.ErrSessionNotFound
	}
	var (
		rec             ownerauth.ClaimSessionRecord
		rawStatus       string
		ownerUserID     *string
		claimAuth       *string
		claimAuthExp    *time.Time
		approvedAt      *time.Time
		consumedAt      *time.Time
		lastPollAt      *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, device_id, bootstrap_id, device_code_hash, user_code_hash, user_code_plain,
		       owner_user_id, status, claim_authorization, claim_auth_expires_at, expires_at,
		       approved_at, consumed_at, last_poll_at, poll_count, created_at
		FROM device_claim_sessions
		WHERE session_id = $1`, sessionID).Scan(
		&rec.SessionID,
		&rec.DeviceID,
		&rec.BootstrapID,
		&rec.DeviceCodeHash,
		&rec.UserCodeHash,
		&rec.UserCodePlain,
		&ownerUserID,
		&rawStatus,
		&claimAuth,
		&claimAuthExp,
		&rec.ExpiresAt,
		&approvedAt,
		&consumedAt,
		&lastPollAt,
		&rec.PollCount,
		&rec.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ownerauth.ClaimSessionRecord{}, ownerauth.ErrSessionNotFound
	}
	if err != nil {
		return ownerauth.ClaimSessionRecord{}, err
	}

	rec.Status = ownerauth.ClaimSessionStatus(rawStatus)
	if ownerUserID != nil {
		rec.OwnerUserID = *ownerUserID
	}
	if claimAuth != nil {
		rec.ClaimAuthorization = *claimAuth
	}
	rec.ClaimAuthExpiresAt = claimAuthExp
	rec.ApprovedAt = approvedAt
	rec.ConsumedAt = consumedAt
	rec.LastPollAt = lastPollAt

	if rec.Status == ownerauth.ClaimSessionPending && !rec.ExpiresAt.After(time.Now().UTC()) {
		rec.Status = ownerauth.ClaimSessionExpired
	}
	return rec, nil
}

func (s *Store) GetClaimSessionByDeviceCodeHash(ctx context.Context, deviceCodeHash string) (ownerauth.ClaimSessionRecord, error) {
	deviceCodeHash = strings.TrimSpace(deviceCodeHash)
	if len(deviceCodeHash) != 64 {
		return ownerauth.ClaimSessionRecord{}, ownerauth.ErrSessionNotFound
	}
	var (
		rec          ownerauth.ClaimSessionRecord
		rawStatus    string
		ownerUserID  *string
		claimAuth    *string
		claimAuthExp *time.Time
		approvedAt   *time.Time
		consumedAt   *time.Time
		lastPollAt   *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, device_id, bootstrap_id, device_code_hash, user_code_hash, user_code_plain,
		       owner_user_id, status, claim_authorization, claim_auth_expires_at, expires_at,
		       approved_at, consumed_at, last_poll_at, poll_count, created_at
		FROM device_claim_sessions
		WHERE device_code_hash = $1`, deviceCodeHash).Scan(
		&rec.SessionID,
		&rec.DeviceID,
		&rec.BootstrapID,
		&rec.DeviceCodeHash,
		&rec.UserCodeHash,
		&rec.UserCodePlain,
		&ownerUserID,
		&rawStatus,
		&claimAuth,
		&claimAuthExp,
		&rec.ExpiresAt,
		&approvedAt,
		&consumedAt,
		&lastPollAt,
		&rec.PollCount,
		&rec.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ownerauth.ClaimSessionRecord{}, ownerauth.ErrSessionNotFound
	}
	if err != nil {
		return ownerauth.ClaimSessionRecord{}, err
	}

	rec.Status = ownerauth.ClaimSessionStatus(rawStatus)
	if ownerUserID != nil {
		rec.OwnerUserID = *ownerUserID
	}
	if claimAuth != nil {
		rec.ClaimAuthorization = *claimAuth
	}
	rec.ClaimAuthExpiresAt = claimAuthExp
	rec.ApprovedAt = approvedAt
	rec.ConsumedAt = consumedAt
	rec.LastPollAt = lastPollAt

	if rec.Status == ownerauth.ClaimSessionPending && !rec.ExpiresAt.After(time.Now().UTC()) {
		rec.Status = ownerauth.ClaimSessionExpired
	}
	return rec, nil
}

func (s *Store) ApproveClaimSession(ctx context.Context, sessionID, ownerUserID string, now time.Time) error {
	sessionID = strings.TrimSpace(sessionID)
	ownerUserID = strings.TrimSpace(ownerUserID)
	if sessionID == "" || ownerUserID == "" {
		return errors.New("session_id and owner_user_id are required")
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE device_claim_sessions
		SET status = 'approved', owner_user_id = $2, approved_at = $3
		WHERE session_id = $1 AND status = 'pending' AND expires_at > $3`,
		sessionID, ownerUserID, now,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// Diagnose failure cause
	current, err := s.GetClaimSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !current.ExpiresAt.After(now) {
		return ownerauth.ErrSessionExpired
	}
	if current.Status == ownerauth.ClaimSessionApproved || current.Status == ownerauth.ClaimSessionConsumed {
		return ownerauth.ErrSessionAlreadyApproved
	}
	if current.Status == ownerauth.ClaimSessionDenied {
		return ownerauth.ErrSessionDenied
	}
	return ownerauth.ErrSessionNotPending
}

func (s *Store) DenyClaimSession(ctx context.Context, sessionID string, now time.Time) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE device_claim_sessions
		SET status = 'denied'
		WHERE session_id = $1 AND status = 'pending' AND expires_at > $2`,
		sessionID, now,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	current, err := s.GetClaimSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !current.ExpiresAt.After(now) {
		return ownerauth.ErrSessionExpired
	}
	return ownerauth.ErrSessionNotPending
}

func (s *Store) PollClaimSession(
	ctx context.Context,
	deviceCodeHash string,
	minInterval time.Duration,
	now time.Time,
	mintAuthFn func(bootstrapID, deviceID, ownerUserID string) (string, time.Time, error),
) (ownerauth.PollOutcome, error) {
	deviceCodeHash = strings.TrimSpace(deviceCodeHash)
	if len(deviceCodeHash) != 64 {
		return ownerauth.PollOutcome{}, ownerauth.ErrSessionNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ownerauth.PollOutcome{}, err
	}
	defer tx.Rollback(ctx)

	var (
		sessionID    string
		deviceID     string
		bootstrapID  string
		ownerUserID  *string
		status       string
		claimAuth    *string
		claimAuthExp *time.Time
		expiresAt    time.Time
		lastPollAt   *time.Time
		pollCount    int
	)
	err = tx.QueryRow(ctx, `
		SELECT session_id, device_id, bootstrap_id, owner_user_id, status,
		       claim_authorization, claim_auth_expires_at, expires_at, last_poll_at, poll_count
		FROM device_claim_sessions
		WHERE device_code_hash = $1
		FOR UPDATE`, deviceCodeHash).Scan(
		&sessionID,
		&deviceID,
		&bootstrapID,
		&ownerUserID,
		&status,
		&claimAuth,
		&claimAuthExp,
		&expiresAt,
		&lastPollAt,
		&pollCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ownerauth.PollOutcome{}, ownerauth.ErrSessionNotFound
	}
	if err != nil {
		return ownerauth.PollOutcome{}, err
	}

	if !expiresAt.After(now) {
		if status == "pending" {
			_, _ = tx.Exec(ctx, `UPDATE device_claim_sessions SET status = 'expired', last_poll_at = $1, poll_count = poll_count + 1 WHERE session_id = $2`, now, sessionID)
			_ = tx.Commit(ctx)
		}
		return ownerauth.PollOutcome{Status: ownerauth.PollOutcomeExpired}, nil
	}

	if lastPollAt != nil && now.Sub(*lastPollAt) < minInterval {
		_, _ = tx.Exec(ctx, `UPDATE device_claim_sessions SET last_poll_at = $1, poll_count = poll_count + 1 WHERE session_id = $2`, now, sessionID)
		_ = tx.Commit(ctx)
		return ownerauth.PollOutcome{
			Status:          ownerauth.PollOutcomeSlowDown,
			IntervalSeconds: int(minInterval.Seconds()) * 2,
		}, nil
	}

	_, err = tx.Exec(ctx, `UPDATE device_claim_sessions SET last_poll_at = $1, poll_count = poll_count + 1 WHERE session_id = $2`, now, sessionID)
	if err != nil {
		return ownerauth.PollOutcome{}, err
	}

	switch status {
	case "pending":
		if err := tx.Commit(ctx); err != nil {
			return ownerauth.PollOutcome{}, err
		}
		return ownerauth.PollOutcome{
			Status:          ownerauth.PollOutcomePending,
			IntervalSeconds: int(minInterval.Seconds()),
		}, nil

	case "denied":
		if err := tx.Commit(ctx); err != nil {
			return ownerauth.PollOutcome{}, err
		}
		return ownerauth.PollOutcome{Status: ownerauth.PollOutcomeDenied}, nil

	case "approved":
		owner := ""
		if ownerUserID != nil {
			owner = *ownerUserID
		}
		token, tokenExp, err := mintAuthFn(bootstrapID, deviceID, owner)
		if err != nil {
			return ownerauth.PollOutcome{}, err
		}
		_, err = tx.Exec(ctx, `
			UPDATE device_claim_sessions
			SET status = 'consumed', consumed_at = $1, claim_authorization = $2, claim_auth_expires_at = $3
			WHERE session_id = $4`,
			now, token, tokenExp, sessionID,
		)
		if err != nil {
			return ownerauth.PollOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ownerauth.PollOutcome{}, err
		}
		return ownerauth.PollOutcome{
			Status:             ownerauth.PollOutcomeApproved,
			ClaimAuthorization: token,
			ExpiresAt:          tokenExp,
		}, nil

	case "consumed":
		if claimAuthExp != nil && claimAuthExp.After(now) && claimAuth != nil {
			if err := tx.Commit(ctx); err != nil {
				return ownerauth.PollOutcome{}, err
			}
			return ownerauth.PollOutcome{
				Status:             ownerauth.PollOutcomeApproved,
				ClaimAuthorization: *claimAuth,
				ExpiresAt:          *claimAuthExp,
			}, nil
		}
		if err := tx.Commit(ctx); err != nil {
			return ownerauth.PollOutcome{}, err
		}
		return ownerauth.PollOutcome{Status: ownerauth.PollOutcomeExpired}, nil

	default:
		if err := tx.Commit(ctx); err != nil {
			return ownerauth.PollOutcome{}, err
		}
		return ownerauth.PollOutcome{Status: ownerauth.PollOutcomeExpired}, nil
	}
}

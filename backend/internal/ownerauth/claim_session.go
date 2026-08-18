package ownerauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

type ClaimSessionStatus string

const (
	ClaimSessionPending  ClaimSessionStatus = "pending"
	ClaimSessionApproved ClaimSessionStatus = "approved"
	ClaimSessionDenied   ClaimSessionStatus = "denied"
	ClaimSessionExpired  ClaimSessionStatus = "expired"
	ClaimSessionConsumed ClaimSessionStatus = "consumed"
)

var (
	ErrSessionNotFound        = errors.New("claim session not found")
	ErrSessionExpired         = errors.New("claim session expired")
	ErrSessionNotPending      = errors.New("claim session not in pending state")
	ErrSessionAlreadyApproved = errors.New("claim session already approved")
	ErrSessionDenied          = errors.New("claim session denied")
)

type ClaimSessionRecord struct {
	SessionID          string             `json:"session_id"`
	DeviceID           string             `json:"device_id"`
	BootstrapID        string             `json:"bootstrap_id"`
	DeviceCodeHash     string             `json:"device_code_hash"`
	UserCodeHash       string             `json:"user_code_hash"`
	OwnerUserID        string             `json:"owner_user_id,omitempty"`
	Status             ClaimSessionStatus `json:"status"`
	ClaimAuthorization string             `json:"claim_authorization,omitempty"`
	ClaimAuthExpiresAt *time.Time         `json:"claim_auth_expires_at,omitempty"`
	ExpiresAt          time.Time          `json:"expires_at"`
	ApprovedAt         *time.Time         `json:"approved_at,omitempty"`
	ConsumedAt         *time.Time         `json:"consumed_at,omitempty"`
	LastPollAt         *time.Time         `json:"last_poll_at,omitempty"`
	PollCount          int                `json:"poll_count"`
	CreatedAt          time.Time          `json:"created_at"`
}

type PollOutcomeStatus string

const (
	PollOutcomePending  PollOutcomeStatus = "authorization_pending"
	PollOutcomeApproved PollOutcomeStatus = "approved"
	PollOutcomeDenied   PollOutcomeStatus = "access_denied"
	PollOutcomeExpired  PollOutcomeStatus = "expired_token"
	PollOutcomeSlowDown PollOutcomeStatus = "slow_down"
)

type PollOutcome struct {
	Status             PollOutcomeStatus
	ClaimAuthorization string
	ExpiresAt          time.Time
	IntervalSeconds    int
}

// ClaimSessionStore defines the contract for durable zero-typing device claim sessions.
type ClaimSessionStore interface {
	CreateSession(ctx context.Context, session ClaimSessionRecord) error
	GetSessionByID(ctx context.Context, sessionID string) (ClaimSessionRecord, error)
	GetSessionByDeviceCodeHash(ctx context.Context, deviceCodeHash string) (ClaimSessionRecord, error)
	ApproveSession(ctx context.Context, sessionID, ownerUserID string, now time.Time) error
	DenySession(ctx context.Context, sessionID string, now time.Time) error
	PollSession(ctx context.Context, deviceCodeHash string, minInterval time.Duration, now time.Time, mintAuthFn func(bootstrapID, deviceID, ownerUserID string) (string, time.Time, error)) (PollOutcome, error)
	AuthorizeClaim(ctx context.Context, rawAuthorization, bootstrapID, deviceID string, now time.Time) (string, error)
}

func HashSecret(raw string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(digest[:])
}

// MemoryClaimSessionStore is an in-memory implementation for isolated tests.
type MemoryClaimSessionStore struct {
	mu       sync.Mutex
	sessions map[string]ClaimSessionRecord // key: session_id
	byCode   map[string]string             // key: device_code_hash -> session_id
}

func NewMemoryClaimSessionStore() *MemoryClaimSessionStore {
	return &MemoryClaimSessionStore{
		sessions: make(map[string]ClaimSessionRecord),
		byCode:   make(map[string]string),
	}
}

func (m *MemoryClaimSessionStore) CreateSession(_ context.Context, session ClaimSessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[session.SessionID]; exists {
		return errors.New("session already exists")
	}
	if _, exists := m.byCode[session.DeviceCodeHash]; exists {
		return errors.New("device code hash collision")
	}
	m.sessions[session.SessionID] = session
	m.byCode[session.DeviceCodeHash] = session.SessionID
	return nil
}

func (m *MemoryClaimSessionStore) GetSessionByID(_ context.Context, sessionID string) (ClaimSessionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok {
		return ClaimSessionRecord{}, ErrSessionNotFound
	}
	return session, nil
}

func (m *MemoryClaimSessionStore) GetSessionByDeviceCodeHash(_ context.Context, deviceCodeHash string) (ClaimSessionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID, ok := m.byCode[deviceCodeHash]
	if !ok {
		return ClaimSessionRecord{}, ErrSessionNotFound
	}
	return m.sessions[sessionID], nil
}

func (m *MemoryClaimSessionStore) ApproveSession(_ context.Context, sessionID, ownerUserID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if !session.ExpiresAt.After(now) {
		session.Status = ClaimSessionExpired
		m.sessions[sessionID] = session
		return ErrSessionExpired
	}
	if session.Status == ClaimSessionApproved || session.Status == ClaimSessionConsumed {
		return ErrSessionAlreadyApproved
	}
	if session.Status == ClaimSessionDenied {
		return ErrSessionDenied
	}
	if session.Status != ClaimSessionPending {
		return ErrSessionNotPending
	}
	session.Status = ClaimSessionApproved
	session.OwnerUserID = strings.TrimSpace(ownerUserID)
	session.ApprovedAt = &now
	m.sessions[sessionID] = session
	return nil
}

func (m *MemoryClaimSessionStore) DenySession(_ context.Context, sessionID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if !session.ExpiresAt.After(now) {
		session.Status = ClaimSessionExpired
		m.sessions[sessionID] = session
		return ErrSessionExpired
	}
	if session.Status != ClaimSessionPending {
		return ErrSessionNotPending
	}
	session.Status = ClaimSessionDenied
	m.sessions[sessionID] = session
	return nil
}

func (m *MemoryClaimSessionStore) PollSession(
	_ context.Context,
	deviceCodeHash string,
	minInterval time.Duration,
	now time.Time,
	mintAuthFn func(bootstrapID, deviceID, ownerUserID string) (string, time.Time, error),
) (PollOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID, ok := m.byCode[deviceCodeHash]
	if !ok {
		return PollOutcome{}, ErrSessionNotFound
	}
	session := m.sessions[sessionID]

	if session.Status != ClaimSessionConsumed && !session.ExpiresAt.After(now) {
		if session.Status == ClaimSessionPending || session.Status == ClaimSessionApproved {
			session.Status = ClaimSessionExpired
			m.sessions[sessionID] = session
		}
		return PollOutcome{Status: PollOutcomeExpired}, nil
	}

	if session.LastPollAt != nil && now.Sub(*session.LastPollAt) < minInterval {
		session.LastPollAt = &now
		session.PollCount++
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomeSlowDown, IntervalSeconds: int(minInterval.Seconds()) * 2}, nil
	}

	session.LastPollAt = &now
	session.PollCount++

	switch session.Status {
	case ClaimSessionPending:
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomePending, IntervalSeconds: int(minInterval.Seconds())}, nil
	case ClaimSessionDenied:
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomeDenied}, nil
	case ClaimSessionApproved:
		token, exp, err := mintAuthFn(session.BootstrapID, session.DeviceID, session.OwnerUserID)
		if err != nil {
			m.sessions[sessionID] = session
			return PollOutcome{}, err
		}
		session.Status = ClaimSessionConsumed
		session.ConsumedAt = &now
		session.ClaimAuthorization = token
		session.ClaimAuthExpiresAt = &exp
		m.sessions[sessionID] = session
		return PollOutcome{
			Status:             PollOutcomeApproved,
			ClaimAuthorization: token,
			ExpiresAt:          exp,
		}, nil
	case ClaimSessionConsumed:
		if session.ClaimAuthExpiresAt != nil && session.ClaimAuthExpiresAt.After(now) && session.ClaimAuthorization != "" {
			m.sessions[sessionID] = session
			return PollOutcome{
				Status:             PollOutcomeApproved,
				ClaimAuthorization: session.ClaimAuthorization,
				ExpiresAt:          *session.ClaimAuthExpiresAt,
			}, nil
		}
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomeExpired}, nil
	case ClaimSessionExpired:
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomeExpired}, nil
	default:
		m.sessions[sessionID] = session
		return PollOutcome{Status: PollOutcomeExpired}, nil
	}
}

func (m *MemoryClaimSessionStore) AuthorizeClaim(_ context.Context, rawAuthorization, bootstrapID, deviceID string, now time.Time) (string, error) {
	rawAuthorization = strings.TrimSpace(rawAuthorization)
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if rawAuthorization == "" || bootstrapID == "" || deviceID == "" {
		return "", ErrInvalidClaim
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		if session.Status != ClaimSessionConsumed || session.ClaimAuthExpiresAt == nil || !session.ClaimAuthExpiresAt.After(now) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(session.ClaimAuthorization), []byte(rawAuthorization)) != 1 {
			continue
		}
		if session.BootstrapID != bootstrapID || session.DeviceID != deviceID || strings.TrimSpace(session.OwnerUserID) == "" {
			return "", ErrInvalidClaim
		}
		return session.OwnerUserID, nil
	}
	return "", ErrInvalidClaim
}

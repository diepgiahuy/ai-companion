package pairing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/idempotency"
)

const SessionTTL = 2 * time.Minute

var (
	ErrUnauthorized         = errors.New("pairing participant is not authorized")
	ErrDeviceUnavailable    = errors.New("pairing candidate device is unavailable")
	ErrSessionExpired       = errors.New("pairing session expired")
	ErrSessionClosed        = errors.New("pairing session is closed")
	ErrInvalidConfirmation  = errors.New("invalid pairing confirmation")
	ErrRelationshipNotFound = errors.New("pairing relationship not found")
	ErrRelationshipRevoked  = errors.New("pairing relationship is revoked")
)

type Participant struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

type RecipientDescriptor struct {
	RelationshipID string    `json:"relationship_id"`
	PeerDeviceID   string    `json:"peer_device_id"`
	DisplayName    string    `json:"display_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Relationship struct {
	ID        string     `json:"relationship_id"`
	DeviceAID string     `json:"device_a_id"`
	DeviceBID string     `json:"device_b_id"`
	UserAID   string     `json:"user_a_id"`
	UserBID   string     `json:"user_b_id"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type Session struct {
	ID                   string      `json:"session_id"`
	Initiator            Participant `json:"initiator"`
	Peer                 Participant `json:"peer"`
	ProximityEvidenceID  string      `json:"proximity_evidence_id"`
	InitiatorNonce       string      `json:"-"`
	PeerNonce            string      `json:"-"`
	InitiatorConfirmedAt *time.Time  `json:"initiator_confirmed_at,omitempty"`
	PeerConfirmedAt      *time.Time  `json:"peer_confirmed_at,omitempty"`
	ExpiresAt            time.Time   `json:"expires_at"`
	RelationshipID       string      `json:"relationship_id,omitempty"`
	State                string      `json:"state"`
}

type CreateMutation struct {
	Session
	IdempotencyKey string
	RequestHash    string
}

type ConfirmMutation struct {
	SessionID      string
	Participant    Participant
	Nonce          string
	ConfirmedAt    time.Time
	IdempotencyKey string
	RequestHash    string
	RelationshipID string
}

type RejectMutation struct {
	SessionID      string
	Participant    Participant
	RejectedAt     time.Time
	IdempotencyKey string
	RequestHash    string
}

type ConfirmationOutcome struct {
	Session        Session `json:"session"`
	Completed      bool    `json:"completed"`
	RelationshipID string  `json:"relationship_id,omitempty"`
	Replayed       bool    `json:"replayed"`
}

type Repository interface {
	CreatePairingSession(context.Context, CreateMutation) (Session, bool, error)
	ConfirmPairingSession(context.Context, ConfirmMutation) (ConfirmationOutcome, error)
	RejectPairingSession(context.Context, RejectMutation) (Session, bool, error)
	ListAuthorizedRecipients(context.Context, Participant) ([]RecipientDescriptor, error)
	RevokeRelationship(context.Context, Participant, string, time.Time) error
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func (s *Service) ListAuthorizedRecipients(ctx context.Context, participant Participant) ([]RecipientDescriptor, error) {
	participant = normalizeParticipant(participant)
	if participant.UserID == "" || participant.DeviceID == "" {
		return nil, ErrUnauthorized
	}
	return s.repository.ListAuthorizedRecipients(ctx, participant)
}

func (s *Service) RevokeRelationship(ctx context.Context, participant Participant, relationshipID string) error {
	participant = normalizeParticipant(participant)
	relationshipID = strings.TrimSpace(relationshipID)
	if participant.UserID == "" || participant.DeviceID == "" || relationshipID == "" {
		return ErrUnauthorized
	}
	return s.repository.RevokeRelationship(ctx, participant, relationshipID, s.now().UTC())
}

func New(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("pairing repository is required")
	}
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Create establishes a short-lived backend session. The authenticated initiator
// is authoritative; caller-supplied owner identity is never trusted.
func (s *Service) Create(ctx context.Context, initiator Participant, candidateDeviceID, proximityEvidenceID, idempotencyKey string) (Session, bool, error) {
	initiator = normalizeParticipant(initiator)
	candidateDeviceID = strings.TrimSpace(candidateDeviceID)
	proximityEvidenceID = strings.TrimSpace(proximityEvidenceID)
	if initiator.UserID == "" || initiator.DeviceID == "" || candidateDeviceID == "" ||
		candidateDeviceID == initiator.DeviceID || len(candidateDeviceID) > 128 ||
		proximityEvidenceID == "" || len(proximityEvidenceID) > 256 {
		return Session{}, false, ErrUnauthorized
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return Session{}, false, err
	}
	sessionID, err := randomOpaque(24)
	if err != nil {
		return Session{}, false, err
	}
	initiatorNonce, err := randomOpaque(24)
	if err != nil {
		return Session{}, false, err
	}
	peerNonce, err := randomOpaque(24)
	if err != nil {
		return Session{}, false, err
	}
	requestHash, err := idempotency.HashValue(struct {
		InitiatorDeviceID   string `json:"initiator_device_id"`
		CandidateDeviceID   string `json:"candidate_device_id"`
		ProximityEvidenceID string `json:"proximity_evidence_id"`
	}{initiator.DeviceID, candidateDeviceID, proximityEvidenceID})
	if err != nil {
		return Session{}, false, err
	}
	now := s.now()
	return s.repository.CreatePairingSession(ctx, CreateMutation{
		Session: Session{
			ID: sessionID, Initiator: initiator,
			Peer: Participant{DeviceID: candidateDeviceID},
			ProximityEvidenceID: proximityEvidenceID,
			InitiatorNonce: initiatorNonce, PeerNonce: peerNonce,
			ExpiresAt: now.Add(SessionTTL), State: "pending",
		},
		IdempotencyKey: strings.TrimSpace(idempotencyKey), RequestHash: requestHash,
	})
}

func (s *Service) Confirm(ctx context.Context, participant Participant, sessionID, nonce, idempotencyKey string) (ConfirmationOutcome, error) {
	participant = normalizeParticipant(participant)
	sessionID = strings.TrimSpace(sessionID)
	nonce = strings.TrimSpace(nonce)
	if participant.UserID == "" || participant.DeviceID == "" || sessionID == "" || nonce == "" {
		return ConfirmationOutcome{}, ErrInvalidConfirmation
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return ConfirmationOutcome{}, err
	}
	requestHash, err := idempotency.HashValue(struct {
		SessionID string `json:"session_id"`
		DeviceID  string `json:"device_id"`
		Nonce     string `json:"confirmation_nonce"`
	}{sessionID, participant.DeviceID, nonce})
	if err != nil {
		return ConfirmationOutcome{}, err
	}
	relationshipID, err := randomOpaque(24)
	if err != nil {
		return ConfirmationOutcome{}, err
	}
	outcome, err := s.repository.ConfirmPairingSession(ctx, ConfirmMutation{
		SessionID: sessionID, Participant: participant, Nonce: nonce,
		ConfirmedAt: s.now(), IdempotencyKey: strings.TrimSpace(idempotencyKey),
		RequestHash: requestHash, RelationshipID: relationshipID,
	})
	if err != nil {
		return ConfirmationOutcome{}, err
	}
	if outcome.Session.State == "expired" {
		return outcome, ErrSessionExpired
	}
	return outcome, nil
}

// Reject records an authenticated participant's explicit decline. The device
// protocol permits several rejection reasons, but only user_declined is accepted
// from a device; authorization/rate/nonce reasons are server-generated responses.
func (s *Service) Reject(ctx context.Context, participant Participant, sessionID, reason, idempotencyKey string) (Session, bool, error) {
	participant = normalizeParticipant(participant)
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if participant.UserID == "" || participant.DeviceID == "" || sessionID == "" || reason != "user_declined" {
		return Session{}, false, ErrUnauthorized
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return Session{}, false, err
	}
	requestHash, err := idempotency.HashValue(struct {
		SessionID string `json:"session_id"`
		DeviceID  string `json:"device_id"`
		Reason    string `json:"reason"`
	}{sessionID, participant.DeviceID, reason})
	if err != nil {
		return Session{}, false, err
	}
	session, replayed, err := s.repository.RejectPairingSession(ctx, RejectMutation{
		SessionID: sessionID, Participant: participant, RejectedAt: s.now(),
		IdempotencyKey: strings.TrimSpace(idempotencyKey), RequestHash: requestHash,
	})
	if err != nil {
		return Session{}, false, err
	}
	if session.State == "expired" {
		return session, replayed, ErrSessionExpired
	}
	return session, replayed, nil
}

func normalizeParticipant(p Participant) Participant {
	p.UserID = strings.TrimSpace(p.UserID)
	p.DeviceID = strings.TrimSpace(p.DeviceID)
	return p
}

func validateIdempotencyKey(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 128 {
		return fmt.Errorf("idempotency key must be 8..128 bytes")
	}
	return nil
}

func randomOpaque(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

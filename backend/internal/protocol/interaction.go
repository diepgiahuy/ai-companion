package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// InteractionVersion is independent from the legacy Message version. New
// interaction messages are additive; existing peers continue to use Message.
const (
	InteractionVersion               = 1
	MaximumInteractionEnvelopeBytes = 8192
	MaximumInteractionPayloadBytes  = 4096
)

var (
	ErrUnknownInteractionType        = errors.New("unknown interaction message type")
	ErrUnsupportedInteractionVersion = errors.New("unsupported interaction protocol version")
)

type InteractionType string

const (
	GestureNotificationType     InteractionType = "gesture.notification"
	VoiceMailAvailableType      InteractionType = "voice_mail.available"
	VoiceMailClaimType          InteractionType = "voice_mail.claim"
	VoiceMailClaimedType        InteractionType = "voice_mail.claimed"
	VoiceMailPlaybackResultType InteractionType = "voice_mail.playback_result"
	VoiceMailConsumedType       InteractionType = "voice_mail.consumed"
	VoiceMailExpiredType        InteractionType = "voice_mail.expired"
	PairingSessionCreateType    InteractionType = "pairing.session_create"
	PairingSessionCreatedType   InteractionType = "pairing.session_created"
	PairingConfirmationType     InteractionType = "pairing.confirmation"
	PairingSucceededType        InteractionType = "pairing.succeeded"
	PairingRejectedType         InteractionType = "pairing.rejected"
	PairingExpiredType          InteractionType = "pairing.expired"
)

// InteractionEnvelope is a versioned, transport-independent envelope. Its IDs are
// opaque identifiers, never credentials. Backend authorization remains required.
type InteractionEnvelope struct {
	Type           InteractionType `json:"type"`
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"idempotency_key"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
}

type InteractionPayload interface{ Validate() error }

func (e InteractionEnvelope) Validate() error {
	if !e.Type.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownInteractionType, e.Type)
	}
	if e.Version != InteractionVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedInteractionVersion, e.Version)
	}
	if err := validateOpaqueID("id", e.ID, 128); err != nil {
		return err
	}
	if err := validateOpaqueID("idempotency_key", e.IdempotencyKey, 128); err != nil {
		return err
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("payload is required")
	}
	if len(e.Payload) > MaximumInteractionPayloadBytes {
		return fmt.Errorf("payload exceeds %d bytes", MaximumInteractionPayloadBytes)
	}
	return nil
}

func (t InteractionType) Valid() bool {
	switch t {
	case GestureNotificationType,
		VoiceMailAvailableType, VoiceMailClaimType, VoiceMailClaimedType,
		VoiceMailPlaybackResultType, VoiceMailConsumedType, VoiceMailExpiredType,
		PairingSessionCreateType, PairingSessionCreatedType, PairingConfirmationType,
		PairingSucceededType, PairingRejectedType, PairingExpiredType:
		return true
	default:
		return false
	}
}

// EncodeInteraction validates and serializes a concrete interaction payload.
func EncodeInteraction(e InteractionEnvelope, p InteractionPayload) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("payload is required")
	}
	want, err := interactionTypeForPayload(p)
	if err != nil {
		return nil, err
	}
	if e.Type != want {
		return nil, fmt.Errorf("payload %T does not match interaction type %q", p, e.Type)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal interaction payload: %w", err)
	}
	e.Payload = raw
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// DecodeInteraction returns a concrete validated payload. Unknown optional JSON
// fields are ignored for additive evolution; unknown types and versions fail closed.
func DecodeInteraction(data []byte) (InteractionEnvelope, InteractionPayload, error) {
	if len(data) == 0 {
		return InteractionEnvelope{}, nil, fmt.Errorf("interaction envelope is required")
	}
	if len(data) > MaximumInteractionEnvelopeBytes {
		return InteractionEnvelope{}, nil, fmt.Errorf("interaction envelope exceeds %d bytes", MaximumInteractionEnvelopeBytes)
	}
	var e InteractionEnvelope
	if err := json.Unmarshal(data, &e); err != nil {
		return InteractionEnvelope{}, nil, fmt.Errorf("decode interaction envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return InteractionEnvelope{}, nil, err
	}
	p, err := interactionPayloadForType(e.Type)
	if err != nil {
		return InteractionEnvelope{}, nil, err
	}
	if err := json.Unmarshal(e.Payload, p); err != nil {
		return InteractionEnvelope{}, nil, fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	if err := p.Validate(); err != nil {
		return InteractionEnvelope{}, nil, err
	}
	return e, p, nil
}

func interactionTypeForPayload(p InteractionPayload) (InteractionType, error) {
	switch p.(type) {
	case GestureNotification:
		return GestureNotificationType, nil
	case VoiceMailAvailable:
		return VoiceMailAvailableType, nil
	case VoiceMailClaim:
		return VoiceMailClaimType, nil
	case VoiceMailClaimed:
		return VoiceMailClaimedType, nil
	case VoiceMailPlaybackResult:
		return VoiceMailPlaybackResultType, nil
	case VoiceMailConsumed:
		return VoiceMailConsumedType, nil
	case VoiceMailExpired:
		return VoiceMailExpiredType, nil
	case PairingSessionCreate:
		return PairingSessionCreateType, nil
	case PairingSessionCreated:
		return PairingSessionCreatedType, nil
	case PairingConfirmation:
		return PairingConfirmationType, nil
	case PairingSucceeded:
		return PairingSucceededType, nil
	case PairingRejected:
		return PairingRejectedType, nil
	case PairingExpired:
		return PairingExpiredType, nil
	default:
		return "", fmt.Errorf("unsupported interaction payload %T", p)
	}
}

func interactionPayloadForType(t InteractionType) (InteractionPayload, error) {
	switch t {
	case GestureNotificationType:
		return &GestureNotification{}, nil
	case VoiceMailAvailableType:
		return &VoiceMailAvailable{}, nil
	case VoiceMailClaimType:
		return &VoiceMailClaim{}, nil
	case VoiceMailClaimedType:
		return &VoiceMailClaimed{}, nil
	case VoiceMailPlaybackResultType:
		return &VoiceMailPlaybackResult{}, nil
	case VoiceMailConsumedType:
		return &VoiceMailConsumed{}, nil
	case VoiceMailExpiredType:
		return &VoiceMailExpired{}, nil
	case PairingSessionCreateType:
		return &PairingSessionCreate{}, nil
	case PairingSessionCreatedType:
		return &PairingSessionCreated{}, nil
	case PairingConfirmationType:
		return &PairingConfirmation{}, nil
	case PairingSucceededType:
		return &PairingSucceeded{}, nil
	case PairingRejectedType:
		return &PairingRejected{}, nil
	case PairingExpiredType:
		return &PairingExpired{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownInteractionType, t)
	}
}

func validateOpaqueID(name, value string, max int) error {
	if strings.TrimSpace(value) == "" || len(value) > max {
		return fmt.Errorf("%s must be 1..%d bytes", name, max)
	}
	return nil
}

// Gesture delivery is best-effort over the active device session. Idempotency lets a
// recipient suppress a duplicate visual or haptic notification.
type GestureNotification struct {
	Gesture        string `json:"gesture"`
	SenderDeviceID string `json:"sender_device_id"`
}

func (p GestureNotification) Validate() error {
	if err := validateOpaqueID("gesture", p.Gesture, 64); err != nil {
		return err
	}
	return validateOpaqueID("sender_device_id", p.SenderDeviceID, 128)
}

type VoiceMailPolicy string

const (
	VoiceMailPolicyDisabled  VoiceMailPolicy = "disabled"
	VoiceMailPolicyEphemeral VoiceMailPolicy = "ephemeral"
	VoiceMailPolicyRetained  VoiceMailPolicy = "retained"
)

func (p VoiceMailPolicy) Valid() bool {
	return p == VoiceMailPolicyDisabled || p == VoiceMailPolicyEphemeral || p == VoiceMailPolicyRetained
}

// VoiceMailAvailable is metadata only. A future media adapter resolves it through
// the authenticated backend; this protocol never carries an object URL or credential.
type VoiceMailAvailable struct {
	VoiceMailID    string          `json:"voice_mail_id"`
	FromDeviceID   string          `json:"from_device_id"`
	MediaFormat    string          `json:"media_format"`
	DurationMS     int64           `json:"duration_ms"`
	SizeBytes      int64           `json:"size_bytes"`
	ChecksumSHA256 string          `json:"checksum_sha256"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Policy         VoiceMailPolicy `json:"policy"`
}

func (p VoiceMailAvailable) Validate() error {
	if err := validateOpaqueID("voice_mail_id", p.VoiceMailID, 128); err != nil {
		return err
	}
	if err := validateOpaqueID("from_device_id", p.FromDeviceID, 128); err != nil {
		return err
	}
	if p.MediaFormat != "ogg_opus" {
		return fmt.Errorf("media_format must be ogg_opus")
	}
	if p.DurationMS <= 0 || p.DurationMS > 600000 {
		return fmt.Errorf("duration_ms must be 1..600000")
	}
	if p.SizeBytes <= 0 || p.SizeBytes > 33554432 {
		return fmt.Errorf("size_bytes must be 1..33554432")
	}
	if len(p.ChecksumSHA256) != 64 {
		return fmt.Errorf("checksum_sha256 must be a 64-character hex digest")
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	if p.Policy != VoiceMailPolicyEphemeral && p.Policy != VoiceMailPolicyRetained {
		return fmt.Errorf("policy must be ephemeral or retained")
	}
	return nil
}

type VoiceMailClaim struct {
	VoiceMailID string `json:"voice_mail_id"`
	PlaybackID  string `json:"playback_id"`
}

func (p VoiceMailClaim) Validate() error {
	if err := validateOpaqueID("voice_mail_id", p.VoiceMailID, 128); err != nil {
		return err
	}
	return validateOpaqueID("playback_id", p.PlaybackID, 128)
}

type VoiceMailClaimed struct {
	VoiceMailID    string    `json:"voice_mail_id"`
	PlaybackID     string    `json:"playback_id"`
	MediaRef       string    `json:"media_ref"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

func (p VoiceMailClaimed) Validate() error {
	if err := (VoiceMailClaim{VoiceMailID: p.VoiceMailID, PlaybackID: p.PlaybackID}).Validate(); err != nil {
		return err
	}
	if err := validateOpaqueID("media_ref", p.MediaRef, 256); err != nil {
		return err
	}
	if p.LeaseExpiresAt.IsZero() {
		return fmt.Errorf("lease_expires_at is required")
	}
	return nil
}

type PlaybackResult string

const (
	PlaybackSucceeded PlaybackResult = "succeeded"
	PlaybackFailed    PlaybackResult = "failed"
)

type VoiceMailPlaybackResult struct {
	VoiceMailID string         `json:"voice_mail_id"`
	PlaybackID  string         `json:"playback_id"`
	Result      PlaybackResult `json:"result"`
	FailureCode string         `json:"failure_code,omitempty"`
}

func (p VoiceMailPlaybackResult) Validate() error {
	if err := (VoiceMailClaim{VoiceMailID: p.VoiceMailID, PlaybackID: p.PlaybackID}).Validate(); err != nil {
		return err
	}
	if p.Result != PlaybackSucceeded && p.Result != PlaybackFailed {
		return fmt.Errorf("result must be succeeded or failed")
	}
	if p.Result == PlaybackSucceeded && p.FailureCode != "" {
		return fmt.Errorf("failure_code is only valid for failed playback")
	}
	if len(p.FailureCode) > 64 {
		return fmt.Errorf("failure_code exceeds 64 bytes")
	}
	return nil
}

type VoiceMailConsumed struct {
	VoiceMailID string `json:"voice_mail_id"`
	PlaybackID  string `json:"playback_id,omitempty"`
}

func (p VoiceMailConsumed) Validate() error {
	if err := validateOpaqueID("voice_mail_id", p.VoiceMailID, 128); err != nil {
		return err
	}
	if p.PlaybackID != "" {
		return validateOpaqueID("playback_id", p.PlaybackID, 128)
	}
	return nil
}

type VoiceMailExpired struct { VoiceMailID string `json:"voice_mail_id"` }

func (p VoiceMailExpired) Validate() error { return validateOpaqueID("voice_mail_id", p.VoiceMailID, 128) }

type VoiceMailState string
type VoiceMailEvent string

const (
	VoiceMailAvailableState VoiceMailState = "available"
	VoiceMailClaimedState   VoiceMailState = "claimed"
	VoiceMailConsumedState  VoiceMailState = "consumed"
	VoiceMailExpiredState   VoiceMailState = "expired"

	VoiceMailClaimEvent             VoiceMailEvent = "claim"
	VoiceMailPlaybackSucceededEvent VoiceMailEvent = "playback_succeeded"
	VoiceMailPlaybackFailedEvent    VoiceMailEvent = "playback_failed"
	VoiceMailLeaseExpiredEvent      VoiceMailEvent = "lease_expired"
	VoiceMailConsumeEvent           VoiceMailEvent = "consume"
	VoiceMailExpireEvent            VoiceMailEvent = "expire"
)

// NextVoiceMailState rejects duplicate and terminal transitions. A retained item's
// successful playback returns it to available; ephemeral playback consumes it.
func NextVoiceMailState(state VoiceMailState, event VoiceMailEvent, policy VoiceMailPolicy) (VoiceMailState, error) {
	if policy != VoiceMailPolicyEphemeral && policy != VoiceMailPolicyRetained {
		return "", fmt.Errorf("voice-mail state requires ephemeral or retained policy")
	}
	switch state {
	case VoiceMailConsumedState, VoiceMailExpiredState:
		return "", fmt.Errorf("voice-mail state %q is terminal", state)
	case VoiceMailAvailableState:
		switch event {
		case VoiceMailClaimEvent:
			return VoiceMailClaimedState, nil
		case VoiceMailConsumeEvent:
			return VoiceMailConsumedState, nil
		case VoiceMailExpireEvent:
			return VoiceMailExpiredState, nil
		}
	case VoiceMailClaimedState:
		switch event {
		case VoiceMailPlaybackSucceededEvent:
			if policy == VoiceMailPolicyEphemeral {
				return VoiceMailConsumedState, nil
			}
			return VoiceMailAvailableState, nil
		case VoiceMailPlaybackFailedEvent, VoiceMailLeaseExpiredEvent:
			return VoiceMailAvailableState, nil
		case VoiceMailConsumeEvent:
			return VoiceMailConsumedState, nil
		case VoiceMailExpireEvent:
			return VoiceMailExpiredState, nil
		}
	default:
		return "", fmt.Errorf("unknown voice-mail state %q", state)
	}
	return "", fmt.Errorf("voice-mail event %q is invalid from %q", event, state)
}

type PairingParticipant struct {
	OwnerUserID string `json:"owner_user_id"`
	DeviceID    string `json:"device_id"`
}

func (p PairingParticipant) Validate() error {
	if err := validateOpaqueID("owner_user_id", p.OwnerUserID, 128); err != nil {
		return err
	}
	return validateOpaqueID("device_id", p.DeviceID, 128)
}

// PairingSessionCreate references a local proximity observation without sending RSSI.
// The server must authenticate ownership and authorize both participants separately.
type PairingSessionCreate struct {
	Initiator           PairingParticipant `json:"initiator"`
	CandidateDeviceID   string             `json:"candidate_device_id"`
	ProximityEvidenceID string             `json:"proximity_evidence_id"`
}

func (p PairingSessionCreate) Validate() error {
	if err := p.Initiator.Validate(); err != nil {
		return err
	}
	if err := validateOpaqueID("candidate_device_id", p.CandidateDeviceID, 128); err != nil {
		return err
	}
	return validateOpaqueID("proximity_evidence_id", p.ProximityEvidenceID, 256)
}

type PairingSessionCreated struct {
	SessionID string             `json:"session_id"`
	Initiator PairingParticipant `json:"initiator"`
	Peer      PairingParticipant `json:"peer"`
	ExpiresAt time.Time          `json:"expires_at"`
}

func (p PairingSessionCreated) Validate() error {
	if err := validateOpaqueID("session_id", p.SessionID, 128); err != nil {
		return err
	}
	if err := p.Initiator.Validate(); err != nil {
		return err
	}
	if err := p.Peer.Validate(); err != nil {
		return err
	}
	if p.Initiator == p.Peer {
		return fmt.Errorf("pairing participants must differ")
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	return nil
}

type PairingConfirmation struct {
	SessionID         string             `json:"session_id"`
	Participant       PairingParticipant `json:"participant"`
	ConfirmationNonce string             `json:"confirmation_nonce"`
	ConfirmedAt       time.Time          `json:"confirmed_at"`
}

func (p PairingConfirmation) Validate() error {
	if err := validateOpaqueID("session_id", p.SessionID, 128); err != nil {
		return err
	}
	if err := p.Participant.Validate(); err != nil {
		return err
	}
	if err := validateOpaqueID("confirmation_nonce", p.ConfirmationNonce, 256); err != nil {
		return err
	}
	if len(p.ConfirmationNonce) < 16 {
		return fmt.Errorf("confirmation_nonce must be at least 16 bytes")
	}
	if p.ConfirmedAt.IsZero() {
		return fmt.Errorf("confirmed_at is required")
	}
	return nil
}

type PairingSucceeded struct {
	SessionID      string             `json:"session_id"`
	RelationshipID string             `json:"relationship_id"`
	Initiator      PairingParticipant `json:"initiator"`
	Peer           PairingParticipant `json:"peer"`
}

func (p PairingSucceeded) Validate() error {
	if err := validateOpaqueID("relationship_id", p.RelationshipID, 128); err != nil {
		return err
	}
	return (PairingSessionCreated{SessionID: p.SessionID, Initiator: p.Initiator, Peer: p.Peer, ExpiresAt: time.Unix(1, 0)}).Validate()
}

type PairingRejected struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

func (p PairingRejected) Validate() error {
	if err := validateOpaqueID("session_id", p.SessionID, 128); err != nil {
		return err
	}
	switch p.Reason {
	case "user_declined", "authorization_denied", "invalid_nonce", "rate_limited":
		return nil
	default:
		return fmt.Errorf("unsupported pairing rejection reason %q", p.Reason)
	}
}

type PairingExpired struct { SessionID string `json:"session_id"` }

func (p PairingExpired) Validate() error { return validateOpaqueID("session_id", p.SessionID, 128) }

type PairingState string
type PairingEvent string

const (
	PairingAwaitingConfirmation PairingState = "awaiting_confirmation"
	PairingInitiatorConfirmed   PairingState = "initiator_confirmed"
	PairingPeerConfirmed        PairingState = "peer_confirmed"
	PairingSucceededState       PairingState = "succeeded"
	PairingRejectedState        PairingState = "rejected"
	PairingExpiredState         PairingState = "expired"

	PairingConfirmInitiatorEvent PairingEvent = "confirm_initiator"
	PairingConfirmPeerEvent      PairingEvent = "confirm_peer"
	PairingRejectEvent           PairingEvent = "reject"
	PairingExpireEvent           PairingEvent = "expire"
)

// NextPairingState makes bilateral confirmation explicit and rejects duplicate
// confirmation or every transition from a terminal state.
func NextPairingState(state PairingState, event PairingEvent) (PairingState, error) {
	switch state {
	case PairingSucceededState, PairingRejectedState, PairingExpiredState:
		return "", fmt.Errorf("pairing state %q is terminal", state)
	case PairingAwaitingConfirmation:
		switch event {
		case PairingConfirmInitiatorEvent:
			return PairingInitiatorConfirmed, nil
		case PairingConfirmPeerEvent:
			return PairingPeerConfirmed, nil
		}
	case PairingInitiatorConfirmed:
		switch event {
		case PairingConfirmPeerEvent:
			return PairingSucceededState, nil
		case PairingConfirmInitiatorEvent:
			return "", fmt.Errorf("duplicate initiator confirmation")
		}
	case PairingPeerConfirmed:
		switch event {
		case PairingConfirmInitiatorEvent:
			return PairingSucceededState, nil
		case PairingConfirmPeerEvent:
			return "", fmt.Errorf("duplicate peer confirmation")
		}
	default:
		return "", fmt.Errorf("unknown pairing state %q", state)
	}
	if event == PairingRejectEvent {
		return PairingRejectedState, nil
	}
	if event == PairingExpireEvent {
		return PairingExpiredState, nil
	}
	return "", fmt.Errorf("pairing event %q is invalid from %q", event, state)
}

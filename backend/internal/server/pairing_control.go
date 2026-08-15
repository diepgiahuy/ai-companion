package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"companion-server/internal/pairing"
	"companion-server/internal/protocol"
)

// WithPairingRepository binds the authoritative pairing service to this
// Server's own session hub. The repository is injected explicitly by product
// composition; there is no package-global pairing registry.
func WithPairingRepository(repository pairing.Repository) Option {
	return func(s *Server) {
		if s == nil || s.hub == nil || repository == nil {
			return
		}
		service, err := pairing.New(repository)
		if err == nil {
			s.hub.setPairingService(service)
		}
	}
}

func protocolPairingParticipant(p pairing.Participant) protocol.PairingParticipant {
	return protocol.PairingParticipant{OwnerUserID: p.UserID, DeviceID: p.DeviceID}
}

func sessionPairingParticipant(s *session) pairing.Participant {
	return pairing.Participant{UserID: s.userID, DeviceID: s.deviceID}
}

func (s *session) handlePairingControl(ctx context.Context, data []byte) (bool, error) {
	message, err := protocol.Decode(data)
	if err != nil {
		return false, nil
	}
	switch message.Type {
	case protocol.PairingSessionCreateType, protocol.PairingConfirmationType, protocol.PairingRejectedType:
	default:
		return false, nil
	}
	if message.SessionID != s.id {
		return true, fmt.Errorf("session_id does not match")
	}
	envelope, payload, err := protocol.DecodeInteraction(data)
	if err != nil {
		return true, err
	}
	service := s.hub.pairing()
	if service == nil {
		return true, fmt.Errorf("pairing service unavailable")
	}

	switch value := payload.(type) {
	case *protocol.PairingSessionCreate:
		authenticated := protocolPairingParticipant(sessionPairingParticipant(s))
		if value.Initiator != authenticated {
			return true, pairing.ErrUnauthorized
		}
		// The peer must have an authenticated active Companion session to receive
		// its participant-specific confirmation nonce. Enrollment/ownership is
		// rechecked transactionally by the pairing repository.
		if len(s.hub.targets("", value.CandidateDeviceID)) == 0 {
			return true, pairing.ErrDeviceUnavailable
		}
		return true, s.processInbound(envelope.MessageID, data, func() error {
			created, _, err := service.Create(ctx, sessionPairingParticipant(s), value.CandidateDeviceID, value.ProximityEvidenceID, envelope.IdempotencyKey)
			if err != nil {
				return err
			}
			payload := protocol.PairingSessionCreated{
				SessionID: created.ID,
				Initiator: protocolPairingParticipant(created.Initiator),
				Peer:      protocolPairingParticipant(created.Peer),
				ExpiresAt: created.ExpiresAt,
			}
			occurredAt := time.Now().UTC().Format(time.RFC3339Nano)
			if err := s.sendJSONMeta(ctx, protocol.PairingSessionCreatedType, protocol.Metadata{
				MessageID: created.InitiatorNonce, CorrelationID: envelope.MessageID,
				IdempotencyKey: envelope.IdempotencyKey, OccurredAt: occurredAt,
			}, payload); err != nil {
				return err
			}
			peers := s.hub.targets(created.Peer.UserID, created.Peer.DeviceID)
			if len(peers) == 0 {
				return pairing.ErrDeviceUnavailable
			}
			for _, peer := range peers {
				if err := peer.sendJSONMeta(ctx, protocol.PairingSessionCreatedType, protocol.Metadata{
					MessageID: created.PeerNonce, CorrelationID: envelope.MessageID,
					IdempotencyKey: envelope.IdempotencyKey, OccurredAt: occurredAt,
				}, payload); err != nil {
					return err
				}
			}
			return nil
		})

	case *protocol.PairingConfirmation:
		if value.Participant != protocolPairingParticipant(sessionPairingParticipant(s)) {
			return true, pairing.ErrUnauthorized
		}
		return true, s.processInbound(envelope.MessageID, data, func() error {
			outcome, err := service.Confirm(ctx, sessionPairingParticipant(s), value.SessionID, value.ConfirmationNonce, envelope.IdempotencyKey)
			if err != nil {
				if errors.Is(err, pairing.ErrSessionExpired) && outcome.Session.ID != "" {
					return s.pushPairingExpired(ctx, envelope, outcome.Session)
				}
				if errors.Is(err, pairing.ErrInvalidConfirmation) {
					return s.sendPairingRejected(ctx, envelope, value.SessionID, "invalid_nonce")
				}
				if errors.Is(err, pairing.ErrUnauthorized) {
					return s.sendPairingRejected(ctx, envelope, value.SessionID, "authorization_denied")
				}
				return err
			}
			if !outcome.Completed {
				return nil
			}
			return s.pushPairingSucceeded(ctx, envelope, outcome.Session, outcome.RelationshipID)
		})

	case *protocol.PairingRejected:
		if value.Reason != "user_declined" {
			return true, pairing.ErrUnauthorized
		}
		return true, s.processInbound(envelope.MessageID, data, func() error {
			rejected, _, err := service.Reject(ctx, sessionPairingParticipant(s), value.SessionID, value.Reason, envelope.IdempotencyKey)
			if err != nil {
				if errors.Is(err, pairing.ErrSessionExpired) && rejected.ID != "" {
					return s.pushPairingExpired(ctx, envelope, rejected)
				}
				return err
			}
			return s.pushPairingRejected(ctx, envelope, rejected, "user_declined")
		})
	}
	return true, nil
}

func (s *session) terminalPairingMetadata(envelope protocol.Envelope, messageID string) protocol.Metadata {
	return protocol.Metadata{
		MessageID: messageID, CorrelationID: envelope.MessageID,
		IdempotencyKey: envelope.IdempotencyKey,
		OccurredAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (s *session) pushPairingSucceeded(ctx context.Context, envelope protocol.Envelope, paired pairing.Session, relationshipID string) error {
	payload := protocol.PairingSucceeded{
		SessionID: paired.ID, RelationshipID: relationshipID,
		Initiator: protocolPairingParticipant(paired.Initiator),
		Peer:      protocolPairingParticipant(paired.Peer),
	}
	return s.pushPairingToParticipants(ctx, paired, protocol.PairingSucceededType, s.terminalPairingMetadata(envelope, "paired-"+relationshipID), payload)
}

func (s *session) pushPairingRejected(ctx context.Context, envelope protocol.Envelope, rejected pairing.Session, reason string) error {
	payload := protocol.PairingRejected{SessionID: rejected.ID, Reason: reason}
	return s.pushPairingToParticipants(ctx, rejected, protocol.PairingRejectedType, s.terminalPairingMetadata(envelope, "rejected-"+rejected.ID), payload)
}

func (s *session) pushPairingExpired(ctx context.Context, envelope protocol.Envelope, expired pairing.Session) error {
	payload := protocol.PairingExpired{SessionID: expired.ID}
	return s.pushPairingToParticipants(ctx, expired, protocol.PairingExpiredType, s.terminalPairingMetadata(envelope, "expired-"+expired.ID), payload)
}

func (s *session) sendPairingRejected(ctx context.Context, envelope protocol.Envelope, sessionID, reason string) error {
	return s.sendJSONMeta(ctx, protocol.PairingRejectedType, s.terminalPairingMetadata(envelope, "rejected-"+sessionID), protocol.PairingRejected{SessionID: sessionID, Reason: reason})
}

func (s *session) pushPairingToParticipants(ctx context.Context, paired pairing.Session, messageType protocol.MessageType, metadata protocol.Metadata, payload any) error {
	targets := append([]*session{}, s.hub.targets(paired.Initiator.UserID, paired.Initiator.DeviceID)...)
	targets = append(targets, s.hub.targets(paired.Peer.UserID, paired.Peer.DeviceID)...)
	if len(targets) == 0 {
		return pairing.ErrDeviceUnavailable
	}
	for _, target := range targets {
		if err := target.sendJSONMeta(ctx, messageType, metadata, payload); err != nil {
			return err
		}
	}
	return nil
}

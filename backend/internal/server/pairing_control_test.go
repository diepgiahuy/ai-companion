package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"companion-server/internal/pairing"
	"companion-server/internal/protocol"
)

type pairingControlRepository struct {
	create func(pairing.CreateMutation) (pairing.Session, bool, error)
	confirm func(pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error)
	reject func(pairing.RejectMutation) (pairing.Session, bool, error)
}

func (r pairingControlRepository) CreatePairingSession(_ context.Context, m pairing.CreateMutation) (pairing.Session, bool, error) {
	if r.create != nil { return r.create(m) }
	return pairing.Session{}, false, errors.New("unexpected create")
}
func (r pairingControlRepository) ConfirmPairingSession(_ context.Context, m pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error) {
	if r.confirm != nil { return r.confirm(m) }
	return pairing.ConfirmationOutcome{}, errors.New("unexpected confirm")
}
func (r pairingControlRepository) RejectPairingSession(_ context.Context, m pairing.RejectMutation) (pairing.Session, bool, error) {
	if r.reject != nil { return r.reject(m) }
	return pairing.Session{}, false, errors.New("unexpected reject")
}

func pairingTestSession(hub *sessionHub, sessionID, userID, deviceID string) *session {
	return &session{
		id: sessionID, userID: userID, deviceID: deviceID, hub: hub,
		controlWrites: make(chan outbound, 8), mediaWrites: make(chan outbound, 1),
		seenInbound: make(map[string]inboundRecord),
	}
}

func pairingInteraction(t *testing.T, sessionID, messageID, key string, kind protocol.MessageType, payload protocol.InteractionPayload) []byte {
	t.Helper()
	data, err := protocol.EncodeInteraction(protocol.Envelope{
		Version: protocol.Version, Type: kind, MessageID: messageID,
		SessionID: sessionID, IdempotencyKey: key,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, payload)
	if err != nil { t.Fatal(err) }
	return data
}

func decodePairingOutbound(t *testing.T, out outbound) (protocol.Envelope, protocol.InteractionPayload) {
	t.Helper()
	envelope, payload, err := protocol.DecodeInteraction(out.data)
	if err != nil { t.Fatalf("decode outbound pairing interaction: %v", err) }
	return envelope, payload
}

func TestPairingControlRejectsSpoofedAuthenticatedParticipant(t *testing.T) {
	hub := newSessionHub()
	service, err := pairing.New(pairingControlRepository{})
	if err != nil { t.Fatal(err) }
	hub.setPairingService(service)
	s := pairingTestSession(hub, "session-a", "user-a", "device-a")

	data := pairingInteraction(t, s.id, "msg-create-1", "idem-create-1", protocol.PairingSessionCreateType,
		protocol.PairingSessionCreate{
			Initiator: protocol.PairingParticipant{OwnerUserID:"spoofed-user", DeviceID:s.deviceID},
			CandidateDeviceID:"session-b", ProximityEvidenceID:"rf-1",
		})
	handled, err := s.handlePairingControl(context.Background(), data)
	if !handled || !errors.Is(err, pairing.ErrUnauthorized) {
		t.Fatalf("handled=%v err=%v, want authenticated identity rejection", handled, err)
	}
}

func TestPairingControlResolvesOnlyActiveSessionDiscoveryAlias(t *testing.T) {
	hub := newSessionHub()
	repo := pairingControlRepository{create: func(m pairing.CreateMutation) (pairing.Session, bool, error) {
		if m.Peer.DeviceID != "device-b" {
			t.Fatalf("backend pairing service saw peer=%q, want stable device-b after alias resolution", m.Peer.DeviceID)
		}
		m.Peer.UserID = "user-b"
		return m.Session, false, nil
	}}
	service, err := pairing.New(repo)
	if err != nil { t.Fatal(err) }
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a-opaque-1234", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b-opaque-5678", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)

	// A stable device ID is intentionally NOT a valid BLE discovery value even
	// though that device is connected. Only its opaque active WebSocket session
	// ID may cross the discovery boundary.
	stableIDAttempt := pairingInteraction(t, initiator.id, "msg-create-stable", "idem-create-stable", protocol.PairingSessionCreateType,
		protocol.PairingSessionCreate{
			Initiator: protocol.PairingParticipant{OwnerUserID:initiator.userID, DeviceID:initiator.deviceID},
			CandidateDeviceID:peer.deviceID, ProximityEvidenceID:"rf-stable",
		})
	handled, err := initiator.handlePairingControl(context.Background(), stableIDAttempt)
	if !handled || !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("stable device id discovery handled=%v err=%v, want unavailable", handled, err)
	}

	aliasAttempt := pairingInteraction(t, initiator.id, "msg-create-alias", "idem-create-alias", protocol.PairingSessionCreateType,
		protocol.PairingSessionCreate{
			Initiator: protocol.PairingParticipant{OwnerUserID:initiator.userID, DeviceID:initiator.deviceID},
			CandidateDeviceID:peer.id, ProximityEvidenceID:"rf-alias",
		})
	handled, err = initiator.handlePairingControl(context.Background(), aliasAttempt)
	if err != nil || !handled { t.Fatalf("alias handled=%v err=%v", handled, err) }
	<-initiator.controlWrites
	<-peer.controlWrites

	hub.unregister(peer.deviceID, peer)
	staleAlias := pairingInteraction(t, initiator.id, "msg-create-stale", "idem-create-stale", protocol.PairingSessionCreateType,
		protocol.PairingSessionCreate{
			Initiator: protocol.PairingParticipant{OwnerUserID:initiator.userID, DeviceID:initiator.deviceID},
			CandidateDeviceID:peer.id, ProximityEvidenceID:"rf-stale",
		})
	handled, err = initiator.handlePairingControl(context.Background(), staleAlias)
	if !handled || !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("stale alias handled=%v err=%v, want unavailable", handled, err)
	}
}

func TestPairingControlDeliversParticipantSpecificNonceAsMessageID(t *testing.T) {
	hub := newSessionHub()
	repo := pairingControlRepository{create: func(m pairing.CreateMutation) (pairing.Session, bool, error) {
		m.Peer.UserID = "user-b"
		return m.Session, false, nil
	}}
	service, err := pairing.New(repo)
	if err != nil { t.Fatal(err) }
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a-opaque-1234", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b-opaque-5678", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)

	data := pairingInteraction(t, initiator.id, "msg-create-2", "idem-create-2", protocol.PairingSessionCreateType,
		protocol.PairingSessionCreate{
			Initiator: protocol.PairingParticipant{OwnerUserID:initiator.userID, DeviceID:initiator.deviceID},
			CandidateDeviceID:peer.id, ProximityEvidenceID:"rf-2",
		})
	handled, err := initiator.handlePairingControl(context.Background(), data)
	if err != nil || !handled { t.Fatalf("handled=%v err=%v", handled, err) }

	initiatorEnvelope, initiatorPayload := decodePairingOutbound(t, <-initiator.controlWrites)
	peerEnvelope, peerPayload := decodePairingOutbound(t, <-peer.controlWrites)
	if initiatorEnvelope.Type != protocol.PairingSessionCreatedType || peerEnvelope.Type != protocol.PairingSessionCreatedType {
		t.Fatalf("unexpected outbound types %q %q", initiatorEnvelope.Type, peerEnvelope.Type)
	}
	if initiatorEnvelope.MessageID == peerEnvelope.MessageID || len(initiatorEnvelope.MessageID) < 16 || len(peerEnvelope.MessageID) < 16 {
		t.Fatalf("participant nonce message IDs must be distinct opaque values: %q %q", initiatorEnvelope.MessageID, peerEnvelope.MessageID)
	}
	createdA := initiatorPayload.(*protocol.PairingSessionCreated)
	createdB := peerPayload.(*protocol.PairingSessionCreated)
	if createdA.SessionID == "" || createdA.SessionID != createdB.SessionID || createdA.Peer.DeviceID != peer.deviceID {
		t.Fatalf("created payloads diverged: %+v %+v", createdA, createdB)
	}
}

func TestPairingControlPublishesDeterministicSuccessToBothParticipants(t *testing.T) {
	hub := newSessionHub()
	paired := pairing.Session{
		ID:"pair-session", Initiator:pairing.Participant{UserID:"user-a",DeviceID:"device-a"},
		Peer:pairing.Participant{UserID:"user-b",DeviceID:"device-b"}, State:"paired", RelationshipID:"relationship-1",
	}
	repo := pairingControlRepository{confirm: func(m pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error) {
		if m.Participant != paired.Peer { return pairing.ConfirmationOutcome{}, pairing.ErrUnauthorized }
		return pairing.ConfirmationOutcome{Session:paired, Completed:true, RelationshipID:paired.RelationshipID}, nil
	}}
	service, err := pairing.New(repo)
	if err != nil { t.Fatal(err) }
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)

	data := pairingInteraction(t, peer.id, "msg-confirm-1", "idem-confirm-1", protocol.PairingConfirmationType,
		protocol.PairingConfirmation{
			SessionID:paired.ID,
			Participant:protocol.PairingParticipant{OwnerUserID:peer.userID,DeviceID:peer.deviceID},
			ConfirmationNonce:"confirmation-nonce-123", ConfirmedAt:time.Now().UTC(),
		})
	handled, err := peer.handlePairingControl(context.Background(), data)
	if err != nil || !handled { t.Fatalf("handled=%v err=%v", handled, err) }

	for name, target := range map[string]*session{"initiator":initiator,"peer":peer} {
		envelope, payload := decodePairingOutbound(t, <-target.controlWrites)
		if envelope.Type != protocol.PairingSucceededType || envelope.MessageID != "paired-"+paired.RelationshipID {
			t.Fatalf("%s success metadata=%+v", name, envelope)
		}
		succeeded := payload.(*protocol.PairingSucceeded)
		if succeeded.RelationshipID != paired.RelationshipID || succeeded.SessionID != paired.ID {
			t.Fatalf("%s success payload=%+v", name, succeeded)
		}
	}
}

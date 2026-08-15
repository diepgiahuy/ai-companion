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
	create  func(pairing.CreateMutation) (pairing.Session, bool, error)
	confirm func(pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error)
	reject  func(pairing.RejectMutation) (pairing.Session, bool, error)
}

func (r pairingControlRepository) CreatePairingSession(_ context.Context, m pairing.CreateMutation) (pairing.Session, bool, error) {
	if r.create != nil {
		return r.create(m)
	}
	return pairing.Session{}, false, errors.New("unexpected create")
}

func (r pairingControlRepository) ConfirmPairingSession(_ context.Context, m pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error) {
	if r.confirm != nil {
		return r.confirm(m)
	}
	return pairing.ConfirmationOutcome{}, errors.New("unexpected confirm")
}

func (r pairingControlRepository) RejectPairingSession(_ context.Context, m pairing.RejectMutation) (pairing.Session, bool, error) {
	if r.reject != nil {
		return r.reject(m)
	}
	return pairing.Session{}, false, errors.New("unexpected reject")
}

func pairingTestSession(hub *sessionHub, sessionID, userID, deviceID string) *session {
	return &session{
		id:            sessionID,
		userID:        userID,
		deviceID:      deviceID,
		hub:           hub,
		controlWrites: make(chan outbound, 8),
		mediaWrites:   make(chan outbound, 1),
		seenInbound:   make(map[string]inboundRecord),
	}
}

func pairingDeviceInteraction(t *testing.T, sessionID, messageID, key string, kind protocol.MessageType, payload any) []byte {
	t.Helper()
	data, err := protocol.Encode(kind, protocol.Metadata{
		MessageID:      messageID,
		SessionID:      sessionID,
		IdempotencyKey: key,
		OccurredAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}, payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodePairingOutbound(t *testing.T, out outbound) (protocol.Envelope, protocol.InteractionPayload) {
	t.Helper()
	envelope, payload, err := protocol.DecodeInteraction(out.data)
	if err != nil {
		t.Fatalf("decode outbound pairing interaction: %v", err)
	}
	return envelope, payload
}

func TestPairingControlRejectsClientSuppliedParticipantIdentity(t *testing.T) {
	hub := newSessionHub()
	createCalled := false
	service, err := pairing.New(pairingControlRepository{create: func(m pairing.CreateMutation) (pairing.Session, bool, error) {
		createCalled = true
		return m.Session, false, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a-opaque-1234", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b-opaque-5678", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)
	alias, err := pairingDiscoveryID(peer.id, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	// Unknown identity fields fail strict payload decoding. The product derives
	// participant identity only from the already-authenticated WebSocket session.
	data := pairingDeviceInteraction(t, initiator.id, "msg-create-spoof", "idem-create-spoof", protocol.PairingSessionCreateType,
		map[string]any{
			"candidate_discovery_id": alias,
			"proximity_evidence_id":  "rf-1",
			"initiator": map[string]any{
				"owner_user_id": "spoofed-user",
				"device_id":     "spoofed-device",
			},
		})
	handled, err := initiator.handlePairingControl(context.Background(), data)
	if !handled || err == nil {
		t.Fatalf("handled=%v err=%v, want strict identity-field rejection", handled, err)
	}
	if createCalled {
		t.Fatal("repository was called for a client-supplied participant identity")
	}
}

func TestPairingControlResolvesOnlyActiveRotatingDiscoveryAlias(t *testing.T) {
	hub := newSessionHub()
	repo := pairingControlRepository{create: func(m pairing.CreateMutation) (pairing.Session, bool, error) {
		if m.Initiator.UserID != "user-a" || m.Initiator.DeviceID != "device-a" {
			t.Fatalf("backend pairing service saw initiator=%+v, want authenticated session identity", m.Initiator)
		}
		if m.Peer.DeviceID != "device-b" {
			t.Fatalf("backend pairing service saw peer=%q, want stable device-b after alias resolution", m.Peer.DeviceID)
		}
		m.Peer.UserID = "user-b"
		return m.Session, false, nil
	}}
	service, err := pairing.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a-opaque-1234", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b-opaque-5678", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)

	// A stable device ID is not accepted at the radio-discovery boundary.
	stableIDAttempt := pairingDeviceInteraction(t, initiator.id, "msg-create-stable", "idem-create-stable", protocol.PairingSessionCreateType,
		map[string]any{
			"candidate_discovery_id": peer.deviceID,
			"proximity_evidence_id":  "rf-stable",
		})
	handled, err := initiator.handlePairingControl(context.Background(), stableIDAttempt)
	if !handled || err == nil {
		t.Fatalf("stable device id discovery handled=%v err=%v, want rejection", handled, err)
	}

	alias, err := pairingDiscoveryID(peer.id, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !validPairingDiscoveryID(alias) || alias == peer.id || alias == peer.deviceID {
		t.Fatalf("rotating alias=%q session=%q device=%q", alias, peer.id, peer.deviceID)
	}
	aliasAttempt := pairingDeviceInteraction(t, initiator.id, "msg-create-alias", "idem-create-alias", protocol.PairingSessionCreateType,
		pairingCreateRequest{CandidateDiscoveryID: alias, ProximityEvidenceID: "rf-alias"})
	handled, err = initiator.handlePairingControl(context.Background(), aliasAttempt)
	if err != nil || !handled {
		t.Fatalf("alias handled=%v err=%v", handled, err)
	}
	<-initiator.controlWrites
	<-peer.controlWrites

	hub.unregister(peer.deviceID, peer)
	staleAlias := pairingDeviceInteraction(t, initiator.id, "msg-create-stale", "idem-create-stale", protocol.PairingSessionCreateType,
		pairingCreateRequest{CandidateDiscoveryID: alias, ProximityEvidenceID: "rf-stale"})
	handled, err = initiator.handlePairingControl(context.Background(), staleAlias)
	if !handled || !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("stale alias handled=%v err=%v, want unavailable", handled, err)
	}
}

func TestPairingDiscoveryIDRotatesBySlot(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	first, err := pairingDiscoveryID("session-opaque-123456", at)
	if err != nil {
		t.Fatal(err)
	}
	same, err := pairingDiscoveryID("session-opaque-123456", at.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	next, err := pairingDiscoveryID("session-opaque-123456", at.Add(pairingDiscoverySlotDuration))
	if err != nil {
		t.Fatal(err)
	}
	if first != same || first == next || !validPairingDiscoveryID(first) || !validPairingDiscoveryID(next) {
		t.Fatalf("rotation first=%q same=%q next=%q", first, same, next)
	}
}

func TestPairingControlDeliversParticipantSpecificNonceAsMessageID(t *testing.T) {
	hub := newSessionHub()
	repo := pairingControlRepository{create: func(m pairing.CreateMutation) (pairing.Session, bool, error) {
		m.Peer.UserID = "user-b"
		return m.Session, false, nil
	}}
	service, err := pairing.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a-opaque-1234", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b-opaque-5678", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)
	alias, err := pairingDiscoveryID(peer.id, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	data := pairingDeviceInteraction(t, initiator.id, "msg-create-2", "idem-create-2", protocol.PairingSessionCreateType,
		pairingCreateRequest{CandidateDiscoveryID: alias, ProximityEvidenceID: "rf-2"})
	handled, err := initiator.handlePairingControl(context.Background(), data)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}

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
		ID:        "pair-session",
		Initiator: pairing.Participant{UserID: "user-a", DeviceID: "device-a"},
		Peer:      pairing.Participant{UserID: "user-b", DeviceID: "device-b"},
		State:     "paired",
		RelationshipID: "relationship-1",
	}
	repo := pairingControlRepository{confirm: func(m pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error) {
		if m.Participant != paired.Peer {
			return pairing.ConfirmationOutcome{}, pairing.ErrUnauthorized
		}
		return pairing.ConfirmationOutcome{Session: paired, Completed: true, RelationshipID: paired.RelationshipID}, nil
	}}
	service, err := pairing.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	hub.setPairingService(service)
	initiator := pairingTestSession(hub, "session-a", "user-a", "device-a")
	peer := pairingTestSession(hub, "session-b", "user-b", "device-b")
	hub.register(initiator.deviceID, initiator)
	hub.register(peer.deviceID, peer)

	data := pairingDeviceInteraction(t, peer.id, "msg-confirm-1", "idem-confirm-1", protocol.PairingConfirmationType,
		pairingConfirmationRequest{SessionID: paired.ID, ConfirmationNonce: "confirmation-nonce-123"})
	handled, err := peer.handlePairingControl(context.Background(), data)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}

	for name, target := range map[string]*session{"initiator": initiator, "peer": peer} {
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

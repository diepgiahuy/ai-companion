package pairing

import (
	"context"
	"testing"
	"time"
)

type memoryPairingRepo struct {
	sessions      map[string]Session
	recipients    map[string][]RecipientDescriptor
	relationships map[string]Relationship
}

func newMemoryPairingRepo() *memoryPairingRepo {
	return &memoryPairingRepo{
		sessions:      make(map[string]Session),
		recipients:    make(map[string][]RecipientDescriptor),
		relationships: make(map[string]Relationship),
	}
}

func (m *memoryPairingRepo) CreatePairingSession(_ context.Context, mutation CreateMutation) (Session, bool, error) {
	m.sessions[mutation.ID] = mutation.Session
	return mutation.Session, false, nil
}

func (m *memoryPairingRepo) ConfirmPairingSession(_ context.Context, mutation ConfirmMutation) (ConfirmationOutcome, error) {
	sess, ok := m.sessions[mutation.SessionID]
	if !ok {
		return ConfirmationOutcome{}, ErrSessionExpired
	}
	if mutation.Participant == sess.Initiator {
		sess.InitiatorConfirmedAt = &mutation.ConfirmedAt
	} else if mutation.Participant == sess.Peer {
		sess.PeerConfirmedAt = &mutation.ConfirmedAt
	}
	m.sessions[mutation.SessionID] = sess
	if sess.InitiatorConfirmedAt != nil && sess.PeerConfirmedAt != nil {
		sess.State = "paired"
		sess.RelationshipID = mutation.RelationshipID
		m.sessions[mutation.SessionID] = sess
		return ConfirmationOutcome{Session: sess, Completed: true, RelationshipID: mutation.RelationshipID}, nil
	}
	return ConfirmationOutcome{Session: sess, Completed: false}, nil
}

func (m *memoryPairingRepo) RejectPairingSession(_ context.Context, mutation RejectMutation) (Session, bool, error) {
	sess, ok := m.sessions[mutation.SessionID]
	if !ok {
		return Session{}, false, ErrSessionExpired
	}
	sess.State = "rejected"
	m.sessions[mutation.SessionID] = sess
	return sess, false, nil
}

func (m *memoryPairingRepo) ListAuthorizedRecipients(_ context.Context, participant Participant) ([]RecipientDescriptor, error) {
	return m.recipients[participant.UserID+":"+participant.DeviceID], nil
}

func (m *memoryPairingRepo) RevokeRelationship(_ context.Context, participant Participant, relationshipID string, now time.Time) error {
	rel, ok := m.relationships[relationshipID]
	if !ok {
		return ErrRelationshipNotFound
	}
	if (rel.UserAID == participant.UserID && rel.DeviceAID == participant.DeviceID) ||
		(rel.UserBID == participant.UserID && rel.DeviceBID == participant.DeviceID) {
		t := now.UTC()
		rel.RevokedAt = &t
		m.relationships[relationshipID] = rel
		return nil
	}
	return ErrUnauthorized
}

func TestPairingServiceLifecycle(t *testing.T) {
	repo := newMemoryPairingRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}

	initiator := Participant{UserID: "alice", DeviceID: "device-alice"}
	peer := Participant{UserID: "bob", DeviceID: "device-bob"}

	// 1. Create session
	sess, replayed, err := svc.Create(context.Background(), initiator, "device-bob", "evidence-123", "idemp-create-1")
	if err != nil || replayed {
		t.Fatalf("Create failed: sess=%+v replayed=%v err=%v", sess, replayed, err)
	}
	if sess.ID == "" || sess.State != "pending" {
		t.Fatalf("unexpected session state: %+v", sess)
	}

	// 2. Reject session
	rejected, replayed, err := svc.Reject(context.Background(), peer, sess.ID, "user_declined", "idemp-reject-1")
	if err != nil || replayed || rejected.State != "rejected" {
		t.Fatalf("Reject failed: rejected=%+v err=%v", rejected, err)
	}

	// 3. Recipient listing
	repo.recipients["alice:device-alice"] = []RecipientDescriptor{{
		RelationshipID: "rel-1",
		PeerDeviceID:   "device-bob",
		CreatedAt:      time.Now().UTC(),
	}}
	recipients, err := svc.ListAuthorizedRecipients(context.Background(), initiator)
	if err != nil || len(recipients) != 1 || recipients[0].RelationshipID != "rel-1" {
		t.Fatalf("ListAuthorizedRecipients failed: %+v err=%v", recipients, err)
	}

	// 4. Revocation
	repo.relationships["rel-1"] = Relationship{
		ID:        "rel-1",
		DeviceAID: "device-alice",
		UserAID:   "alice",
		DeviceBID: "device-bob",
		UserBID:   "bob",
	}
	if err := svc.RevokeRelationship(context.Background(), initiator, "rel-1"); err != nil {
		t.Fatalf("RevokeRelationship failed: %v", err)
	}
	if repo.relationships["rel-1"].RevokedAt == nil {
		t.Fatal("expected relationship to have RevokedAt set")
	}
}

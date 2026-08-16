package voicemail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"
)

type memoryVoicemailRepo struct {
	items      map[string]Item
	recipients map[string][]pairing.RecipientDescriptor
}

func newMemoryVoicemailRepo() *memoryVoicemailRepo {
	return &memoryVoicemailRepo{
		items:      make(map[string]Item),
		recipients: make(map[string][]pairing.RecipientDescriptor),
	}
}

func (m *memoryVoicemailRepo) ListRecipients(_ context.Context, userID, deviceID string) ([]pairing.RecipientDescriptor, error) {
	return m.recipients[userID+":"+deviceID], nil
}

func (m *memoryVoicemailRepo) CreateUpload(_ context.Context, _ idempotency.Request, create Create, now time.Time) (Item, error) {
	item := Item{
		ID:                "vm-1",
		RelationshipID:    create.RelationshipID,
		SenderUserID:      create.SenderUserID,
		SenderDeviceID:    create.SenderDeviceID,
		RecipientUserID:   "user-bob",
		RecipientDeviceID: "device-bob",
		ObjectKey:         "blob-vm-1",
		MediaFormat:       MediaFormat,
		DurationMS:        create.DurationMS,
		SizeBytes:         create.SizeBytes,
		ChecksumSHA256:    create.ChecksumSHA256,
		Policy:            create.Policy,
		State:             PendingUpload,
		ExpiresAt:         create.ExpiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	m.items[item.ID] = item
	return item, nil
}

func (m *memoryVoicemailRepo) ItemForSender(_ context.Context, userID, deviceID, id string) (Item, bool, error) {
	item, ok := m.items[id]
	if !ok || item.SenderUserID != userID || item.SenderDeviceID != deviceID {
		return Item{}, false, nil
	}
	return item, true, nil
}

func (m *memoryVoicemailRepo) CompleteUpload(_ context.Context, _ idempotency.Request, userID, deviceID, id string, now time.Time) (Item, error) {
	item, ok := m.items[id]
	if !ok || item.SenderUserID != userID || item.SenderDeviceID != deviceID {
		return Item{}, ErrItemNotFound
	}
	item.State = Unread
	item.UpdatedAt = now
	m.items[id] = item
	return item, nil
}

func (m *memoryVoicemailRepo) ListUnread(_ context.Context, recipientUserID, deviceID string, now time.Time, limit int) ([]Item, error) {
	var out []Item
	for _, item := range m.items {
		if item.RecipientUserID == recipientUserID && item.State == Unread && item.ExpiresAt.After(now) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *memoryVoicemailRepo) ClaimVoiceMail(_ context.Context, _ idempotency.Request, recipientUserID, deviceID, id, playbackID string, now, lease time.Time) (Item, error) {
	item, ok := m.items[id]
	if !ok || item.RecipientUserID != recipientUserID {
		return Item{}, ErrItemNotFound
	}
	item.State = Claimed
	item.PlaybackID = playbackID
	item.LeaseExpiresAt = &lease
	item.UpdatedAt = now
	m.items[id] = item
	return item, nil
}

func (m *memoryVoicemailRepo) CompleteVoiceMailPlayback(_ context.Context, _ idempotency.Request, recipientUserID, recipientDeviceID, id, playbackID string, succeeded bool, now time.Time) (Item, error) {
	item, ok := m.items[id]
	if !ok || item.RecipientUserID != recipientUserID || item.PlaybackID != playbackID {
		return Item{}, ErrItemNotFound
	}
	item.State = Deleted
	item.UpdatedAt = now
	m.items[id] = item
	return item, nil
}

func (m *memoryVoicemailRepo) ItemForPlayback(_ context.Context, recipientUserID, recipientDeviceID, id, playbackID string, now time.Time) (Item, bool, error) {
	item, ok := m.items[id]
	if !ok || item.RecipientUserID != recipientUserID || item.PlaybackID != playbackID {
		return Item{}, false, nil
	}
	return item, true, nil
}

func (m *memoryVoicemailRepo) RequestDelete(_ context.Context, _ idempotency.Request, recipientUserID, id string, now time.Time) (Item, error) {
	item, ok := m.items[id]
	if !ok || item.RecipientUserID != recipientUserID {
		return Item{}, ErrItemNotFound
	}
	item.State = DeletePending
	item.UpdatedAt = now
	m.items[id] = item
	return item, nil
}

func (m *memoryVoicemailRepo) MarkDeleted(_ context.Context, id string, now time.Time) error {
	delete(m.items, id)
	return nil
}

func (m *memoryVoicemailRepo) ClaimCleanup(_ context.Context, now time.Time, limit int) ([]Item, error) {
	return nil, nil
}

func TestVoiceMailCreateValidation(t *testing.T) {
	now := time.Now().UTC()
	validSum := sha256.Sum256([]byte("payload"))
	validHex := hex.EncodeToString(validSum[:])

	tests := []struct {
		name    string
		create  Create
		wantErr string
	}{
		{
			name: "missing relationship_id",
			create: Create{
				SenderUserID:   "user-a",
				SenderDeviceID: "dev-a",
				DurationMS:     1000,
				SizeBytes:      100,
				ChecksumSHA256: validHex,
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(time.Hour),
			},
			wantErr: "relationship_id is required",
		},
		{
			name: "missing sender identity",
			create: Create{
				RelationshipID: "rel-1",
				SenderUserID:   "",
				SenderDeviceID: "dev-a",
				DurationMS:     1000,
				SizeBytes:      100,
				ChecksumSHA256: validHex,
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(time.Hour),
			},
			wantErr: "sender user and device are required",
		},
		{
			name: "invalid duration",
			create: Create{
				RelationshipID: "rel-1",
				SenderUserID:   "user-a",
				SenderDeviceID: "dev-a",
				DurationMS:     0,
				SizeBytes:      100,
				ChecksumSHA256: validHex,
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(time.Hour),
			},
			wantErr: "duration_ms must be 1..",
		},
		{
			name: "invalid checksum length",
			create: Create{
				RelationshipID: "rel-1",
				SenderUserID:   "user-a",
				SenderDeviceID: "dev-a",
				DurationMS:     1000,
				SizeBytes:      100,
				ChecksumSHA256: "abcd",
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(time.Hour),
			},
			wantErr: "checksum_sha256 must be sha256 hex",
		},
		{
			name: "past expires_at",
			create: Create{
				RelationshipID: "rel-1",
				SenderUserID:   "user-a",
				SenderDeviceID: "dev-a",
				DurationMS:     1000,
				SizeBytes:      100,
				ChecksumSHA256: validHex,
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(-time.Minute),
			},
			wantErr: "expires_at must be in the future",
		},
		{
			name: "valid create",
			create: Create{
				RelationshipID: "rel-1",
				SenderUserID:   "user-a",
				SenderDeviceID: "dev-a",
				DurationMS:     2500,
				SizeBytes:      1024,
				ChecksumSHA256: validHex,
				Policy:         Ephemeral,
				ExpiresAt:      now.Add(time.Hour),
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.create.Validate(now)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestVoiceMailServiceUploadAndBlobVerification(t *testing.T) {
	repo := newMemoryVoicemailRepo()
	blobs, err := NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}

	media := []byte("audio payload content")
	sum := sha256.Sum256(media)
	validHex := hex.EncodeToString(sum[:])

	create := Create{
		RelationshipID: "rel-1",
		SenderUserID:   "user-a",
		SenderDeviceID: "dev-a",
		DurationMS:     1500,
		SizeBytes:      int64(len(media)),
		ChecksumSHA256: validHex,
		Policy:         Ephemeral,
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	}

	// 1. Create upload
	item, err := svc.CreateUpload(context.Background(), idempotency.Request{Actor: "user-a:device:dev-a", Key: "k1"}, create)
	if err != nil {
		t.Fatalf("CreateUpload failed: %v", err)
	}

	// 2. Put media
	if err := svc.PutMedia(context.Background(), "user-a", "dev-a", item.ID, bytes.NewReader(media)); err != nil {
		t.Fatalf("PutMedia failed: %v", err)
	}

	// 3. Complete upload
	completed, err := svc.CompleteUpload(context.Background(), idempotency.Request{Actor: "user-a:device:dev-a", Key: "k2"}, "user-a", "dev-a", item.ID)
	if err != nil {
		t.Fatalf("CompleteUpload failed: %v", err)
	}
	if completed.State != Unread {
		t.Fatalf("expected state unread, got %s", completed.State)
	}
}

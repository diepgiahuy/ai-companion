package voicemail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"companion-server/internal/idempotency"
)

const (
	MediaFormat = "ogg_opus"
	MaxSize     = int64(32 << 20)
	MaxDuration = 10 * time.Minute
)

type Policy string

const (
	Disabled  Policy = "disabled"
	Ephemeral Policy = "ephemeral"
	Retained  Policy = "retained"
)

type State string

const (
	PendingUpload State = "pending_upload"
	Unread        State = "unread"
	Claimed       State = "claimed"
	Consumed      State = "consumed"
	DeletePending State = "delete_pending"
	Deleted       State = "deleted"
	Expired       State = "expired"
	Rejected      State = "rejected"
)

type Item struct {
	ID                string     `json:"id"`
	SenderUserID      string     `json:"sender_user_id,omitempty"`
	SenderDeviceID    string     `json:"sender_device_id,omitempty"`
	RecipientUserID   string     `json:"recipient_user_id,omitempty"`
	RecipientDeviceID string     `json:"recipient_device_id,omitempty"`
	ObjectKey         string     `json:"-"`
	MediaFormat       string     `json:"media_format"`
	DurationMS        int64      `json:"duration_ms"`
	SizeBytes         int64      `json:"size_bytes"`
	ChecksumSHA256    string     `json:"checksum_sha256"`
	Policy            Policy     `json:"policy"`
	State             State      `json:"state"`
	PlaybackID        string     `json:"playback_id,omitempty"`
	LeaseExpiresAt    *time.Time `json:"lease_expires_at,omitempty"`
	ExpiresAt         time.Time  `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Create struct {
	SenderUserID      string
	SenderDeviceID    string
	RecipientUserID   string
	RecipientDeviceID string
	DurationMS        int64
	SizeBytes         int64
	ChecksumSHA256    string
	Policy            Policy
	ExpiresAt         time.Time
}

func (c Create) Validate(now time.Time) error {
	if strings.TrimSpace(c.SenderUserID) == "" || strings.TrimSpace(c.SenderDeviceID) == "" || strings.TrimSpace(c.RecipientUserID) == "" {
		return fmt.Errorf("sender user/device and recipient user are required")
	}
	if c.Policy == Disabled || (c.Policy != Ephemeral && c.Policy != Retained) {
		return fmt.Errorf("voice mail policy must be ephemeral or retained")
	}
	if c.DurationMS <= 0 || time.Duration(c.DurationMS)*time.Millisecond > MaxDuration {
		return fmt.Errorf("duration_ms must be 1..%d", MaxDuration.Milliseconds())
	}
	if c.SizeBytes <= 0 || c.SizeBytes > MaxSize {
		return fmt.Errorf("size_bytes must be 1..%d", MaxSize)
	}
	if len(c.ChecksumSHA256) != sha256.Size*2 {
		return fmt.Errorf("checksum_sha256 must be sha256 hex")
	}
	if _, err := hex.DecodeString(c.ChecksumSHA256); err != nil {
		return fmt.Errorf("checksum_sha256 must be sha256 hex")
	}
	if c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now) {
		return fmt.Errorf("expires_at must be in the future")
	}
	return nil
}

type Repository interface {
	CreateUpload(context.Context, idempotency.Request, Create, time.Time) (Item, error)
	ItemForSender(context.Context, string, string) (Item, bool, error)
	CompleteUpload(context.Context, idempotency.Request, string, string, time.Time) (Item, error)
	ListUnread(context.Context, string, string, time.Time, int) ([]Item, error)
	ClaimVoiceMail(context.Context, idempotency.Request, string, string, string, string, time.Time, time.Time) (Item, error)
	CompleteVoiceMailPlayback(context.Context, idempotency.Request, string, string, string, bool, time.Time) (Item, error)
	ItemForPlayback(context.Context, string, string, string, time.Time) (Item, bool, error)
	RequestDelete(context.Context, idempotency.Request, string, string, time.Time) (Item, error)
	MarkDeleted(context.Context, string, time.Time) error
	ClaimCleanup(context.Context, time.Time, int) ([]Item, error)
}

type BlobStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

type Service struct {
	repo  Repository
	blobs BlobStore
	now   func() time.Time
	lease time.Duration
}

func New(repo Repository, blobs BlobStore) (*Service, error) {
	if repo == nil || blobs == nil {
		return nil, fmt.Errorf("voice mail repository and blob store are required")
	}
	return &Service{repo: repo, blobs: blobs, now: time.Now, lease: 2 * time.Minute}, nil
}

func (s *Service) CreateUpload(ctx context.Context, request idempotency.Request, create Create) (Item, error) {
	now := s.now().UTC()
	if err := create.Validate(now); err != nil {
		return Item{}, err
	}
	return s.repo.CreateUpload(ctx, request, create, now)
}

func (s *Service) PutMedia(ctx context.Context, senderUserID, id string, body io.Reader) error {
	item, ok, err := s.repo.ItemForSender(ctx, senderUserID, id)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("voice mail not found")
	}
	if item.State != PendingUpload {
		return fmt.Errorf("voice mail upload is not pending")
	}
	return s.blobs.Put(ctx, item.ObjectKey, body, item.SizeBytes, item.ChecksumSHA256)
}

func (s *Service) CompleteUpload(ctx context.Context, request idempotency.Request, senderUserID, id string) (Item, error) {
	item, ok, err := s.repo.ItemForSender(ctx, senderUserID, id)
	if err != nil {
		return Item{}, err
	}
	if !ok {
		return Item{}, fmt.Errorf("voice mail not found")
	}
	if item.State == PendingUpload {
		reader, err := s.blobs.Open(ctx, item.ObjectKey)
		if err != nil {
			return Item{}, fmt.Errorf("voice mail media is unavailable: %w", err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, io.LimitReader(reader, item.SizeBytes+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return Item{}, copyErr
		}
		if closeErr != nil {
			return Item{}, closeErr
		}
		if size != item.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), item.ChecksumSHA256) {
			return Item{}, fmt.Errorf("voice mail media verification failed")
		}
	}
	return s.repo.CompleteUpload(ctx, request, senderUserID, id, s.now().UTC())
}

func (s *Service) ListUnread(ctx context.Context, recipientUserID, deviceID string, limit int) ([]Item, error) {
	return s.repo.ListUnread(ctx, recipientUserID, deviceID, s.now().UTC(), limit)
}

func (s *Service) Claim(ctx context.Context, request idempotency.Request, recipientUserID, deviceID, id, playbackID string) (Item, error) {
	now := s.now().UTC()
	return s.repo.ClaimVoiceMail(ctx, request, recipientUserID, deviceID, id, playbackID, now, now.Add(s.lease))
}

func (s *Service) OpenMedia(ctx context.Context, recipientUserID, id, playbackID string) (io.ReadCloser, error) {
	item, ok, err := s.repo.ItemForPlayback(ctx, recipientUserID, id, playbackID, s.now().UTC())
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("voice mail is not claimed")
	}
	return s.blobs.Open(ctx, item.ObjectKey)
}

func (s *Service) Playback(ctx context.Context, request idempotency.Request, recipientUserID, id, playbackID string, succeeded bool) (Item, error) {
	item, err := s.repo.CompleteVoiceMailPlayback(ctx, request, recipientUserID, id, playbackID, succeeded, s.now().UTC())
	if err != nil {
		return Item{}, err
	}
	if item.State == DeletePending {
		if err := s.blobs.Delete(ctx, item.ObjectKey); err != nil {
			return item, nil
		}
		if err := s.repo.MarkDeleted(ctx, item.ID, s.now().UTC()); err != nil {
			return item, err
		}
		item.State = Deleted
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, request idempotency.Request, ownerUserID, id string) (Item, error) {
	item, err := s.repo.RequestDelete(ctx, request, ownerUserID, id, s.now().UTC())
	if err != nil {
		return Item{}, err
	}
	if err := s.blobs.Delete(ctx, item.ObjectKey); err != nil {
		return item, nil
	}
	if err := s.repo.MarkDeleted(ctx, item.ID, s.now().UTC()); err != nil {
		return item, err
	}
	item.State = Deleted
	return item, nil
}

func (s *Service) Cleanup(ctx context.Context, limit int) (int, error) {
	items, err := s.repo.ClaimCleanup(ctx, s.now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	deleted := 0
	failed := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if err := s.blobs.Delete(ctx, item.ObjectKey); err != nil {
			failed++
			continue
		}
		if err := s.repo.MarkDeleted(ctx, item.ID, s.now().UTC()); err != nil {
			return deleted, err
		}
		deleted++
	}
	if failed != 0 {
		return deleted, fmt.Errorf("delete %d voice mail blobs", failed)
	}
	return deleted, nil
}

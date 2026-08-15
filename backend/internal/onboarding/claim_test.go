package onboarding

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"companion-server/internal/controlplane"
)

type fakeClaimAuthorizer struct {
	userID      string
	wantRaw     string
	wantBoot    string
	wantDevice  string
	forceError  error
}

func (f fakeClaimAuthorizer) AuthorizeDeviceClaim(raw, bootstrapID, deviceID string) (string, error) {
	if f.forceError != nil {
		return "", f.forceError
	}
	if raw != f.wantRaw || bootstrapID != f.wantBoot || deviceID != f.wantDevice {
		return "", errors.New("pair mismatch")
	}
	return f.userID, nil
}

type fakeClaimRepository struct {
	called      int
	key         string
	requestHash string
	outcome     controlplane.DeviceClaimOutcome
	delivery    controlplane.DeviceClaimDelivery
}

func (f *fakeClaimRepository) ClaimDevice(_ context.Context, mutation controlplane.DeviceClaimMutation) (controlplane.DeviceClaimOutcome, error) {
	f.called++
	if f.key != "" {
		if mutation.IdempotencyKey != f.key || mutation.RequestHash != f.requestHash {
			return controlplane.DeviceClaimOutcome{}, errors.New("unexpected replay identity")
		}
		out := f.outcome
		out.Replayed = true
		return out, nil
	}
	f.key = mutation.IdempotencyKey
	f.requestHash = mutation.RequestHash
	f.outcome = controlplane.DeviceClaimOutcome{DeliveryID: mutation.DeliveryID, DeviceID: mutation.DeviceID}
	f.delivery = controlplane.DeviceClaimDelivery{
		DeliveryID:           mutation.DeliveryID,
		DeviceID:             mutation.DeviceID,
		UserID:               mutation.UserID,
		CredentialCiphertext: append([]byte(nil), mutation.CredentialCiphertext...),
		CredentialNonce:      append([]byte(nil), mutation.CredentialNonce...),
		ExpiresAt:            mutation.ExpiresAt,
	}
	return f.outcome, nil
}

func (f *fakeClaimRepository) DeviceClaimDelivery(_ context.Context, userID, deliveryID string) (controlplane.DeviceClaimDelivery, error) {
	if userID != f.delivery.UserID || deliveryID != f.delivery.DeliveryID {
		return controlplane.DeviceClaimDelivery{}, controlplane.ErrClaimDeliveryUnavailable
	}
	return f.delivery, nil
}

func TestClaimReplayReturnsSameCredentialWithoutPlaintextPersistence(t *testing.T) {
	repository := &fakeClaimRepository{}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	service, err := NewClaimService(repository, fakeClaimAuthorizer{}, key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	first, err := service.claim(context.Background(), "owner-1", "device-1", "bootstrap-1", "idem-0001")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.claim(context.Background(), "owner-1", "device-1", "bootstrap-1", "idem-0001")
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceCredential == "" || first.DeviceCredential != second.DeviceCredential {
		t.Fatalf("credential replay mismatch first=%q second=%q", first.DeviceCredential, second.DeviceCredential)
	}
	if first.DeliveryID != second.DeliveryID || !second.Replayed {
		t.Fatalf("replay outcome first=%+v second=%+v", first, second)
	}
	if strings.Contains(string(repository.delivery.CredentialCiphertext), first.DeviceCredential) {
		t.Fatal("plaintext credential appeared in persisted ciphertext")
	}
	if got := repository.called; got != 2 {
		t.Fatalf("repository claim calls=%d, want 2", got)
	}
}

func TestClaimHTTPRejectsMismatchedAuthorizationBeforeRepositoryMutation(t *testing.T) {
	repository := &fakeClaimRepository{}
	service, err := NewClaimService(repository, fakeClaimAuthorizer{
		userID: "owner-1", wantRaw: "auth-token", wantBoot: "bootstrap-1", wantDevice: "device-1",
	}, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claims", strings.NewReader(`{"device_id":"device-2","bootstrap_id":"bootstrap-1"}`))
	req.Header.Set("Authorization", "Bearer auth-token")
	req.Header.Set("Idempotency-Key", "idem-0001")
	res := httptest.NewRecorder()
	service.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", res.Code)
	}
	if repository.called != 0 {
		t.Fatalf("repository mutated %d times before authorization", repository.called)
	}
}

func TestDecodeEncryptionKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(255 - i)
	}
	raw := base64.RawURLEncoding.EncodeToString(key)
	decoded, err := DecodeEncryptionKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(key) {
		t.Fatal("decoded encryption key mismatch")
	}
	if _, err := DecodeEncryptionKey("too-short"); err == nil {
		t.Fatal("invalid encryption key was accepted")
	}
}

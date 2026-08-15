package ownerauth

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizeDeviceClaimBindsBootstrapAndDevice(t *testing.T) {
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	service := &Service{
		now:    func() time.Time { return now },
		claims: make(map[string]ClaimAuthorization),
	}
	raw := "claim-token"
	service.claims[tokenKey(raw)] = ClaimAuthorization{
		UserID:      "owner-1",
		BootstrapID: claimBinding("bootstrap-1", "device-1"),
		ExpiresAt:   now.Add(time.Minute),
	}

	userID, err := service.AuthorizeDeviceClaim(raw, "bootstrap-1", "device-1")
	if err != nil || userID != "owner-1" {
		t.Fatalf("exact pair authorization user=%q err=%v", userID, err)
	}
	if _, err := service.AuthorizeDeviceClaim(raw, "bootstrap-1", "device-2"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("device substitution err=%v, want ErrInvalidClaim", err)
	}
	if _, err := service.AuthorizeDeviceClaim(raw, "bootstrap-2", "device-1"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("bootstrap substitution err=%v, want ErrInvalidClaim", err)
	}
}

func TestAuthorizeDeviceClaimRejectsExpiredAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	service := &Service{
		now:    func() time.Time { return now },
		claims: make(map[string]ClaimAuthorization),
	}
	raw := "expired-claim"
	key := tokenKey(raw)
	service.claims[key] = ClaimAuthorization{
		UserID:      "owner-1",
		BootstrapID: claimBinding("bootstrap-1", "device-1"),
		ExpiresAt:   now.Add(-time.Second),
	}

	if _, err := service.AuthorizeDeviceClaim(raw, "bootstrap-1", "device-1"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expired authorization err=%v, want ErrInvalidClaim", err)
	}
	if _, ok := service.claims[key]; ok {
		t.Fatal("expired authorization was not pruned")
	}
}

func TestClaimBindingSeparatesPairs(t *testing.T) {
	base := claimBinding("bootstrap-1", "device-1")
	if base == claimBinding("bootstrap-1", "device-2") {
		t.Fatal("different device produced identical claim binding")
	}
	if base == claimBinding("bootstrap-2", "device-1") {
		t.Fatal("different bootstrap produced identical claim binding")
	}
}

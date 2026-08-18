package pgstore

import (
	"context"
	"testing"
	"time"

	"companion-server/internal/ownerauth"
)

func TestPgClaimCodeStoreInterface(t *testing.T) {
	var store ownerauth.ClaimCodeStore = NewPgClaimCodeStore(nil)
	ctx := context.Background()

	// Rate limiting test
	allowed, err := store.AllowAttempt(ctx, "test-host", 3, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("first attempt should be allowed: %v", err)
	}

	// Redemption storage test
	claim := ownerauth.ClaimAuthorization{
		BootstrapID: "test-boot",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := store.PutRedemption(ctx, "code1", "red1", "raw-auth", claim); err != nil {
		t.Fatal(err)
	}

	raw, fetchedClaim, found, err := store.GetRedemption(ctx, "code1", "test-boot", "red1")
	if err != nil || !found || raw != "raw-auth" || fetchedClaim.BootstrapID != "test-boot" {
		t.Fatalf("get redemption mismatch: raw=%s found=%v err=%v", raw, found, err)
	}
}

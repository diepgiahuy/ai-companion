package pgstore

import (
	"context"
	"testing"
	"time"

	"companion-server/internal/ownerauth"
)

func TestPgClaimCodeStoreFailsClosedWithoutPostgres(t *testing.T) {
	var store ownerauth.ClaimCodeStore = NewPgClaimCodeStore(nil)
	ctx := context.Background()

	if allowed, err := store.AllowAttempt(ctx, "test-host", 3, time.Minute); err == nil || allowed {
		t.Fatalf("missing PostgreSQL must fail closed: allowed=%v err=%v", allowed, err)
	}

	claim := ownerauth.ClaimAuthorization{
		UserID:      "test-owner",
		BootstrapID: "test-boot",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := store.PutRedemption(ctx, "code1", "red1", "raw-auth", claim); err == nil {
		t.Fatal("missing PostgreSQL must reject redemption persistence")
	}

	if raw, fetchedClaim, found, err := store.GetRedemption(ctx, "code1", "test-boot", "red1"); err == nil || found || raw != "" || fetchedClaim.BootstrapID != "" {
		t.Fatalf("missing PostgreSQL must not synthesize a redemption: raw=%q claim=%+v found=%v err=%v", raw, fetchedClaim, found, err)
	}
}

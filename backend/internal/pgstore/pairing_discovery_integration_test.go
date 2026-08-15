package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/pairing"
)

func TestPostgresPairingDiscoveryResolverIsRotatingBoundedAndFailClosed(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-pair-disc-%d", time.Now().UnixNano())
	now := time.Unix(1_800_000_000, 0).UTC()
	secret := prefix + "-secret"
	digest := sha256.Sum256([]byte(secret))
	verifier := hex.EncodeToString(digest[:])
	deviceID := prefix + "-device"
	userID := prefix + "-owner"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_credentials(device_id,user_id,token_sha256,status,created_at,rotated_at)
		VALUES($1,$2,$3,'active',now(),now())`, deviceID, userID, verifier); err != nil {
		t.Fatal(err)
	}

	currentID, err := pairing.DiscoveryIDFromCredentialHash(verifier, now)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.ResolvePairingCandidate(ctx, currentID, now)
	if err != nil || candidate.DeviceID != deviceID || candidate.UserID != userID {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}

	oldAccepted, err := pairing.DiscoveryIDFromCredentialHash(verifier, now.Add(-time.Duration(pairing.DiscoveryPastSlots)*pairing.DiscoverySlotDuration))
	if err != nil {
		t.Fatal(err)
	}
	if candidate, err = store.ResolvePairingCandidate(ctx, oldAccepted, now); err != nil || candidate.DeviceID != deviceID {
		t.Fatalf("bounded replay candidate=%+v err=%v", candidate, err)
	}
	stale, err := pairing.DiscoveryIDFromCredentialHash(verifier, now.Add(-time.Duration(pairing.DiscoveryPastSlots+1)*pairing.DiscoverySlotDuration))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePairingCandidate(ctx, stale, now); !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("stale discovery err=%v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE device_credentials SET status='revoked' WHERE device_id=$1`, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePairingCandidate(ctx, currentID, now); !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("revoked discovery err=%v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE device_credentials SET status='active' WHERE device_id=$1`, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_credentials(device_id,user_id,token_sha256,status,created_at,rotated_at)
		VALUES($1,$2,$3,'active',now(),now())`, prefix+"-collision", prefix+"-other-owner", verifier); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePairingCandidate(ctx, currentID, now); !errors.Is(err, pairing.ErrDeviceUnavailable) {
		t.Fatalf("duplicate-verifier discovery err=%v", err)
	}
}

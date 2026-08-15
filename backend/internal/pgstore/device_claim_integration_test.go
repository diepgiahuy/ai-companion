package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/idempotency"
)

func deviceClaimMutation(t *testing.T, prefix, userID, deviceID, rawCredential, idemKey, requestVariant string) controlplane.DeviceClaimMutation {
	t.Helper()
	digest := sha256.Sum256([]byte(rawCredential))
	requestHash, err := idempotency.HashValue(map[string]string{
		"device_id":    deviceID,
		"bootstrap_id": requestVariant,
	})
	if err != nil {
		t.Fatal(err)
	}
	return controlplane.DeviceClaimMutation{
		UserID:               userID,
		DeviceID:             deviceID,
		CredentialHash:       hex.EncodeToString(digest[:]),
		DeliveryID:           prefix + "-delivery-" + idemKey,
		CredentialCiphertext: []byte("ciphertext-" + idemKey),
		CredentialNonce:      []byte("nonce-" + idemKey),
		ExpiresAt:            time.Now().UTC().Add(10 * time.Minute),
		IdempotencyKey:       idemKey,
		RequestHash:          requestHash,
	}
}

func TestPostgresDeviceClaimReplayConflictAndCanonicalAuthentication(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-claim-%d", time.Now().UnixNano())
	userID := prefix + "-owner"
	deviceID := prefix + "-device"
	rawCredential := prefix + "-credential-secret"
	idemKey := prefix + "-idem"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation='device.claim'`, prefix+"%")
	})

	firstMutation := deviceClaimMutation(t, prefix, userID, deviceID, rawCredential, idemKey, "bootstrap-1")
	first, err := store.ClaimDevice(ctx, firstMutation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.DeviceID != deviceID || first.DeliveryID != firstMutation.DeliveryID {
		t.Fatalf("first outcome=%+v", first)
	}

	identity, ok, err := store.AuthenticateDevice(ctx, deviceID, rawCredential)
	if err != nil || !ok || identity.UserID != userID {
		t.Fatalf("canonical device auth identity=%+v ok=%v err=%v", identity, ok, err)
	}

	replayMutation := firstMutation
	replayMutation.DeliveryID = prefix + "-delivery-retry"
	replayMutation.CredentialCiphertext = []byte("different-generated-ciphertext")
	replayMutation.CredentialNonce = []byte("different-generated-nonce")
	replayed, err := store.ClaimDevice(ctx, replayMutation)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.DeliveryID != first.DeliveryID || replayed.DeviceID != first.DeviceID {
		t.Fatalf("replayed outcome=%+v first=%+v", replayed, first)
	}

	conflict := firstMutation
	conflict.RequestHash, err = idempotency.HashValue(map[string]string{
		"device_id": deviceID, "bootstrap_id": "bootstrap-conflict",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimDevice(ctx, conflict); !idempotency.IsConflict(err) {
		t.Fatalf("same idempotency key with different request err=%v, want conflict", err)
	}

	alreadyOwned := firstMutation
	alreadyOwned.IdempotencyKey = prefix + "-other-idem"
	if _, err := store.ClaimDevice(ctx, alreadyOwned); !errors.Is(err, controlplane.ErrDeviceAlreadyClaimed) {
		t.Fatalf("already-owned device err=%v, want ErrDeviceAlreadyClaimed", err)
	}

	delivery, err := store.DeviceClaimDelivery(ctx, userID, first.DeliveryID)
	if err != nil || delivery.DeviceID != deviceID || delivery.UserID != userID {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	if _, err := store.DeviceClaimDelivery(ctx, userID+"-other", first.DeliveryID); !errors.Is(err, controlplane.ErrClaimDeliveryUnavailable) {
		t.Fatalf("cross-owner delivery err=%v, want unavailable", err)
	}
}

func TestPostgresDeviceClaimConcurrentDifferentActorsCommitOneOwner(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-claim-race-%d", time.Now().UnixNano())
	deviceID := prefix + "-device"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation='device.claim'`, prefix+"%")
	})

	type result struct {
		user string
		err  error
	}
	results := make(chan result, 2)
	for _, suffix := range []string{"a", "b"} {
		suffix := suffix
		go func() {
			userID := prefix + "-owner-" + suffix
			mutation := deviceClaimMutation(
				t, prefix+"-"+suffix, userID, deviceID,
				prefix+"-credential-"+suffix, prefix+"-idem-"+suffix, "bootstrap-"+suffix,
			)
			_, claimErr := store.ClaimDevice(ctx, mutation)
			results <- result{user: userID, err: claimErr}
		}()
	}

	var winner string
	var successes, claimedConflicts int
	for i := 0; i < 2; i++ {
		got := <-results
		switch {
		case got.err == nil:
			successes++
			winner = got.user
		case errors.Is(got.err, controlplane.ErrDeviceAlreadyClaimed):
			claimedConflicts++
		default:
			t.Fatalf("unexpected concurrent claim error for %s: %v", got.user, got.err)
		}
	}
	if successes != 1 || claimedConflicts != 1 {
		t.Fatalf("successes=%d claimed_conflicts=%d", successes, claimedConflicts)
	}

	var storedOwner string
	if err := pool.QueryRow(ctx, `SELECT user_id FROM device_credentials WHERE device_id=$1`, deviceID).Scan(&storedOwner); err != nil {
		t.Fatal(err)
	}
	if storedOwner != winner {
		t.Fatalf("stored owner=%q winner=%q", storedOwner, winner)
	}
}

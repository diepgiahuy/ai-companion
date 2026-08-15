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

func TestPostgresDeviceClaimReplayRecoveryConflictAndCanonicalAuthentication(t *testing.T) {
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

	// A fresh authorization for the same authoritative owner is recovery, not
	// ownership transfer. It rotates the verifier and creates one new delivery.
	recoveredCredential := prefix + "-credential-recovered"
	recoveryKey := prefix + "-recovery-idem"
	recoveryMutation := deviceClaimMutation(t, prefix+"-recovery", userID, deviceID, recoveredCredential, recoveryKey, "bootstrap-recovery")
	recovery, err := store.ClaimDevice(ctx, recoveryMutation)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Replayed || recovery.DeliveryID != recoveryMutation.DeliveryID {
		t.Fatalf("recovery outcome=%+v", recovery)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, deviceID, rawCredential); err != nil || ok {
		t.Fatalf("old credential remained valid after recovery: ok=%v err=%v", ok, err)
	}
	identity, ok, err = store.AuthenticateDevice(ctx, deviceID, recoveredCredential)
	if err != nil || !ok || identity.UserID != userID {
		t.Fatalf("recovered credential identity=%+v ok=%v err=%v", identity, ok, err)
	}

	// A retry generates fresh random delivery material at the service layer, but
	// durable idempotency must return the committed delivery without rotating a
	// second time.
	retryGeneratedCredential := prefix + "-credential-must-not-win"
	retryDigest := sha256.Sum256([]byte(retryGeneratedCredential))
	recoveryReplayMutation := recoveryMutation
	recoveryReplayMutation.CredentialHash = hex.EncodeToString(retryDigest[:])
	recoveryReplayMutation.DeliveryID = prefix + "-recovery-generated-retry"
	recoveryReplayMutation.CredentialCiphertext = []byte("recovery-generated-ciphertext")
	recoveryReplayMutation.CredentialNonce = []byte("recovery-generated-nonce")
	recoveryReplay, err := store.ClaimDevice(ctx, recoveryReplayMutation)
	if err != nil {
		t.Fatal(err)
	}
	if !recoveryReplay.Replayed || recoveryReplay.DeliveryID != recovery.DeliveryID {
		t.Fatalf("recovery replay=%+v recovery=%+v", recoveryReplay, recovery)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, deviceID, retryGeneratedCredential); err != nil || ok {
		t.Fatalf("retry-generated credential unexpectedly became valid: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, deviceID, recoveredCredential); err != nil || !ok {
		t.Fatalf("committed recovery credential lost after replay: ok=%v err=%v", ok, err)
	}

	// Process restart preserves the same recovery outcome and verifier.
	restartedStore, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	restartReplay, err := restartedStore.ClaimDevice(ctx, recoveryReplayMutation)
	if err != nil || !restartReplay.Replayed || restartReplay.DeliveryID != recovery.DeliveryID {
		t.Fatalf("restart replay=%+v err=%v recovery=%+v", restartReplay, err, recovery)
	}
	if _, ok, err := restartedStore.AuthenticateDevice(ctx, deviceID, recoveredCredential); err != nil || !ok {
		t.Fatalf("recovered credential invalid after restart replay: ok=%v err=%v", ok, err)
	}

	// A different owner can never use recovery to transfer ownership.
	otherOwnerMutation := deviceClaimMutation(t, prefix+"-other", userID+"-other", deviceID, prefix+"-other-secret", prefix+"-other-idem", "bootstrap-other")
	if _, err := store.ClaimDevice(ctx, otherOwnerMutation); !errors.Is(err, controlplane.ErrDeviceAlreadyClaimed) {
		t.Fatalf("different-owner recovery err=%v, want ErrDeviceAlreadyClaimed", err)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, deviceID, recoveredCredential); err != nil || !ok {
		t.Fatalf("different-owner attempt changed current credential: ok=%v err=%v", ok, err)
	}

	delivery, err := store.DeviceClaimDelivery(ctx, userID, recovery.DeliveryID)
	if err != nil || delivery.DeviceID != deviceID || delivery.UserID != userID {
		t.Fatalf("recovery delivery=%+v err=%v", delivery, err)
	}
	if _, err := store.DeviceClaimDelivery(ctx, userID+"-other", recovery.DeliveryID); !errors.Is(err, controlplane.ErrClaimDeliveryUnavailable) {
		t.Fatalf("cross-owner delivery err=%v, want unavailable", err)
	}
}

func TestPostgresDeviceClaimConcurrentSameOwnerRecoveryRotatesExactlyOnce(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-recovery-race-%d", time.Now().UnixNano())
	userID := prefix + "-owner"
	deviceID := prefix + "-device"
	initialCredential := prefix + "-initial"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation='device.claim'`, prefix+"%")
	})

	if _, err := store.ClaimDevice(ctx, deviceClaimMutation(t, prefix+"-initial", userID, deviceID, initialCredential, prefix+"-initial-idem", "bootstrap-initial")); err != nil {
		t.Fatal(err)
	}

	idemKey := prefix + "-recovery-idem"
	requestVariant := "bootstrap-recovery"
	credentialA := prefix + "-recovery-a"
	credentialB := prefix + "-recovery-b"
	mutationA := deviceClaimMutation(t, prefix+"-a", userID, deviceID, credentialA, idemKey, requestVariant)
	mutationB := deviceClaimMutation(t, prefix+"-b", userID, deviceID, credentialB, idemKey, requestVariant)

	type result struct {
		out controlplane.DeviceClaimOutcome
		err error
	}
	results := make(chan result, 2)
	go func() {
		out, claimErr := store.ClaimDevice(ctx, mutationA)
		results <- result{out: out, err: claimErr}
	}()
	go func() {
		out, claimErr := store.ClaimDevice(ctx, mutationB)
		results <- result{out: out, err: claimErr}
	}()
	r1, r2 := <-results, <-results
	if r1.err != nil || r2.err != nil {
		t.Fatalf("same-owner recovery errors: %v / %v", r1.err, r2.err)
	}
	if r1.out.DeliveryID == "" || r1.out.DeliveryID != r2.out.DeliveryID {
		t.Fatalf("concurrent recovery outcomes diverged: %+v / %+v", r1.out, r2.out)
	}
	if r1.out.Replayed == r2.out.Replayed {
		t.Fatalf("want one first execution and one replay: %+v / %+v", r1.out, r2.out)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, deviceID, initialCredential); err != nil || ok {
		t.Fatalf("initial credential survived recovery: ok=%v err=%v", ok, err)
	}
	_, aOK, aErr := store.AuthenticateDevice(ctx, deviceID, credentialA)
	_, bOK, bErr := store.AuthenticateDevice(ctx, deviceID, credentialB)
	if aErr != nil || bErr != nil || aOK == bOK {
		t.Fatalf("exactly one generated recovery credential must win: a=%v/%v b=%v/%v", aOK, aErr, bOK, bErr)
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

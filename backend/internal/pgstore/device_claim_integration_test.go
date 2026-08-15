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

func TestPostgresDeviceClaimReplayRecoveryAndCanonicalAuthentication(t *testing.T) {
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

	// A fresh owner-authenticated claim by the canonical owner is the recovery
	// operation. It rotates the canonical credential instead of creating a second
	// auth path or a durable recovery secret.
	recoveryCredential := prefix + "-recovery-credential"
	recoveryMutation := deviceClaimMutation(
		t,
		prefix+"-recovery",
		userID,
		deviceID,
		recoveryCredential,
		prefix+"-recovery-idem",
		"bootstrap-recovery",
	)
	recovered, err := store.ClaimDevice(ctx, recoveryMutation)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Replayed || recovered.DeviceID != deviceID || recovered.DeliveryID != recoveryMutation.DeliveryID {
		t.Fatalf("recovery outcome=%+v", recovered)
	}

	if _, oldOK, err := store.AuthenticateDevice(ctx, deviceID, rawCredential); err != nil || oldOK {
		t.Fatalf("old credential survived recovery ok=%v err=%v", oldOK, err)
	}
	identity, ok, err = store.AuthenticateDevice(ctx, deviceID, recoveryCredential)
	if err != nil || !ok || identity.UserID != userID {
		t.Fatalf("recovered auth identity=%+v ok=%v err=%v", identity, ok, err)
	}

	recoveryReplay := recoveryMutation
	recoveryReplay.DeliveryID = prefix + "-recovery-generated-retry"
	recoveryReplay.CredentialHash = firstMutation.CredentialHash
	recoveryReplay.CredentialCiphertext = []byte("discarded-recovery-ciphertext")
	recoveryReplay.CredentialNonce = []byte("discarded-recovery-nonce")
	replayedRecovery, err := store.ClaimDevice(ctx, recoveryReplay)
	if err != nil {
		t.Fatal(err)
	}
	if !replayedRecovery.Replayed || replayedRecovery.DeliveryID != recovered.DeliveryID {
		t.Fatalf("replayed recovery=%+v recovered=%+v", replayedRecovery, recovered)
	}
	if _, oldOK, err := store.AuthenticateDevice(ctx, deviceID, rawCredential); err != nil || oldOK {
		t.Fatalf("replayed recovery restored old credential ok=%v err=%v", oldOK, err)
	}
	if _, newOK, err := store.AuthenticateDevice(ctx, deviceID, recoveryCredential); err != nil || !newOK {
		t.Fatalf("replayed recovery lost new credential ok=%v err=%v", newOK, err)
	}

	otherOwner := prefix + "-other-owner"
	crossOwner := deviceClaimMutation(
		t,
		prefix+"-cross-owner",
		otherOwner,
		deviceID,
		prefix+"-other-credential",
		prefix+"-other-idem",
		"bootstrap-other",
	)
	if _, err := store.ClaimDevice(ctx, crossOwner); !errors.Is(err, controlplane.ErrDeviceAlreadyClaimed) {
		t.Fatalf("cross-owner recovery err=%v, want ErrDeviceAlreadyClaimed", err)
	}
	if _, newOK, err := store.AuthenticateDevice(ctx, deviceID, recoveryCredential); err != nil || !newOK {
		t.Fatalf("cross-owner attempt changed canonical credential ok=%v err=%v", newOK, err)
	}

	delivery, err := store.DeviceClaimDelivery(ctx, userID, recovered.DeliveryID)
	if err != nil || delivery.DeviceID != deviceID || delivery.UserID != userID {
		t.Fatalf("recovery delivery=%+v err=%v", delivery, err)
	}
	if _, err := store.DeviceClaimDelivery(ctx, otherOwner, recovered.DeliveryID); !errors.Is(err, controlplane.ErrClaimDeliveryUnavailable) {
		t.Fatalf("cross-owner delivery err=%v, want unavailable", err)
	}
}

func TestPostgresDeviceClaimConcurrentSameOwnerRecoveryCommitsOneCredential(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-claim-recovery-race-%d", time.Now().UnixNano())
	userID := prefix + "-owner"
	deviceID := prefix + "-device"
	oldCredential := prefix + "-old"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation='device.claim'`, prefix+"%")
	})

	if _, err := store.ClaimDevice(ctx, deviceClaimMutation(
		t, prefix+"-initial", userID, deviceID, oldCredential, prefix+"-initial-idem", "bootstrap-initial",
	)); err != nil {
		t.Fatal(err)
	}

	credentialA := prefix + "-new-a"
	credentialB := prefix + "-new-b"
	idemKey := prefix + "-recovery-idem"
	mutationA := deviceClaimMutation(t, prefix+"-a", userID, deviceID, credentialA, idemKey, "bootstrap-recovery")
	mutationB := deviceClaimMutation(t, prefix+"-b", userID, deviceID, credentialB, idemKey, "bootstrap-recovery")

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

	gotA := <-results
	gotB := <-results
	if gotA.err != nil || gotB.err != nil {
		t.Fatalf("concurrent recovery errors: %v / %v", gotA.err, gotB.err)
	}
	if gotA.out.DeliveryID == "" || gotA.out.DeliveryID != gotB.out.DeliveryID {
		t.Fatalf("concurrent outcomes differ: %+v / %+v", gotA.out, gotB.out)
	}
	if gotA.out.Replayed == gotB.out.Replayed {
		t.Fatalf("want exactly one committed recovery and one replay: %+v / %+v", gotA.out, gotB.out)
	}
	if _, oldOK, err := store.AuthenticateDevice(ctx, deviceID, oldCredential); err != nil || oldOK {
		t.Fatalf("old credential survived concurrent recovery ok=%v err=%v", oldOK, err)
	}
	_, okA, err := store.AuthenticateDevice(ctx, deviceID, credentialA)
	if err != nil {
		t.Fatal(err)
	}
	_, okB, err := store.AuthenticateDevice(ctx, deviceID, credentialB)
	if err != nil {
		t.Fatal(err)
	}
	if okA == okB {
		t.Fatalf("want exactly one generated recovery credential active, a=%v b=%v", okA, okB)
	}

	// Reconstruct the store to prove replay state is database-owned rather than
	// an in-memory process property.
	reopened, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	restartedReplay, err := reopened.ClaimDevice(ctx, mutationA)
	if err != nil {
		t.Fatal(err)
	}
	if !restartedReplay.Replayed || restartedReplay.DeliveryID != gotA.out.DeliveryID {
		t.Fatalf("restart replay=%+v prior=%+v", restartedReplay, gotA.out)
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

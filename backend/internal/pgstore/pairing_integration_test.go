package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"
)

func enrollPairingDevice(t *testing.T, store *Store, userID, deviceID, secret string) {
	t.Helper()
	digest := sha256.Sum256([]byte(secret))
	_, err := store.pool.Exec(context.Background(), `
		INSERT INTO device_credentials(device_id,user_id,token_sha256,status,created_at,rotated_at)
		VALUES($1,$2,$3,'active',now(),now())`, deviceID, userID, hex.EncodeToString(digest[:]))
	if err != nil { t.Fatal(err) }
}

func cleanupPairingPrefix(t *testing.T, store *Store, prefix string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation LIKE 'pairing.%'`, prefix+"%")
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM pairing_sessions WHERE initiator_device_id LIKE $1 OR peer_device_id LIKE $1`, prefix+"%")
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM device_relationships WHERE device_a_id LIKE $1 OR device_b_id LIKE $1`, prefix+"%")
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM outbox WHERE subject LIKE 'relationship/%' AND data_json->>'device_a_id' LIKE $1`, prefix+"%")
	})
}

func TestPostgresPairingBilateralConfirmationCreatesOneRelationshipAndOutbox(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	service, err := pairing.New(store)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pair-%d", time.Now().UnixNano())
	userA, deviceA := prefix+"-ua", prefix+"-da"
	userB, deviceB := prefix+"-ub", prefix+"-db"
	enrollPairingDevice(t, store, userA, deviceA, "secret-a")
	enrollPairingDevice(t, store, userB, deviceB, "secret-b")
	cleanupPairingPrefix(t, store, prefix)

	created, replayed, err := service.Create(ctx, pairing.Participant{UserID:userA, DeviceID:deviceA}, deviceB, "rf-sample-1", prefix+"-create")
	if err != nil || replayed { t.Fatalf("create replayed=%v err=%v", replayed, err) }
	createdReplay, replayed, err := service.Create(ctx, pairing.Participant{UserID:userA, DeviceID:deviceA}, deviceB, "rf-sample-1", prefix+"-create")
	if err != nil || !replayed || createdReplay.ID != created.ID || createdReplay.InitiatorNonce != created.InitiatorNonce || createdReplay.PeerNonce != created.PeerNonce {
		t.Fatalf("create replay=%+v replayed=%v err=%v original=%+v", createdReplay, replayed, err, created)
	}

	first, err := service.Confirm(ctx, pairing.Participant{UserID:userA, DeviceID:deviceA}, created.ID, created.InitiatorNonce, prefix+"-confirm-a")
	if err != nil || first.Completed { t.Fatalf("first confirm=%+v err=%v", first, err) }
	second, err := service.Confirm(ctx, pairing.Participant{UserID:userB, DeviceID:deviceB}, created.ID, created.PeerNonce, prefix+"-confirm-b")
	if err != nil || !second.Completed || second.RelationshipID == "" { t.Fatalf("second confirm=%+v err=%v", second, err) }
	secondReplay, err := service.Confirm(ctx, pairing.Participant{UserID:userB, DeviceID:deviceB}, created.ID, created.PeerNonce, prefix+"-confirm-b")
	if err != nil || !secondReplay.Replayed || secondReplay.RelationshipID != second.RelationshipID {
		t.Fatalf("confirmation replay=%+v err=%v", secondReplay, err)
	}

	// Reconstruct the store/service as a process restart would. Durable
	// idempotency must return the already-committed relationship, not rotate or
	// duplicate it.
	restartedStore, err := New(pool)
	if err != nil { t.Fatal(err) }
	restartedService, err := pairing.New(restartedStore)
	if err != nil { t.Fatal(err) }
	restartReplay, err := restartedService.Confirm(ctx, pairing.Participant{UserID:userB, DeviceID:deviceB}, created.ID, created.PeerNonce, prefix+"-confirm-b")
	if err != nil || !restartReplay.Replayed || restartReplay.RelationshipID != second.RelationshipID {
		t.Fatalf("restart replay=%+v err=%v, want relationship %q", restartReplay, err, second.RelationshipID)
	}

	var relationships, events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM device_relationships WHERE relationship_id=$1`, second.RelationshipID).Scan(&relationships); err != nil { t.Fatal(err) }
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='pairing.relationship.created' AND subject=$1`, "relationship/"+second.RelationshipID).Scan(&events); err != nil { t.Fatal(err) }
	if relationships != 1 || events != 1 { t.Fatalf("relationships=%d events=%d", relationships, events) }
}

func TestPostgresPairingReversedSessionsConvergeOnOneCanonicalRelationship(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	service, err := pairing.New(store)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pair-reverse-%d", time.Now().UnixNano())
	userA, deviceA := prefix+"-ua", prefix+"-da"
	userB, deviceB := prefix+"-ub", prefix+"-db"
	enrollPairingDevice(t, store, userA, deviceA, "secret-a")
	enrollPairingDevice(t, store, userB, deviceB, "secret-b")
	cleanupPairingPrefix(t, store, prefix)

	s1, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-a", prefix+"-c1")
	if err != nil { t.Fatal(err) }
	s2, _, err := service.Create(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, deviceA, "rf-b", prefix+"-c2")
	if err != nil { t.Fatal(err) }
	if _, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, s1.ID, s1.InitiatorNonce, prefix+"-s1a"); err != nil { t.Fatal(err) }
	if _, err := service.Confirm(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, s2.ID, s2.InitiatorNonce, prefix+"-s2b"); err != nil { t.Fatal(err) }

	type confirmResult struct {
		outcome pairing.ConfirmationOutcome
		err     error
	}
	results := make(chan confirmResult, 2)
	go func() {
		outcome, err := service.Confirm(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, s1.ID, s1.PeerNonce, prefix+"-s1b")
		results <- confirmResult{outcome: outcome, err: err}
	}()
	go func() {
		outcome, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, s2.ID, s2.PeerNonce, prefix+"-s2a")
		results <- confirmResult{outcome: outcome, err: err}
	}()
	r1, r2 := <-results, <-results
	if r1.err != nil || r2.err != nil {
		t.Fatalf("concurrent reversed confirmation errors: %v / %v", r1.err, r2.err)
	}
	if !r1.outcome.Completed || !r2.outcome.Completed || r1.outcome.RelationshipID == "" || r1.outcome.RelationshipID != r2.outcome.RelationshipID {
		t.Fatalf("concurrent relationships diverged: %+v / %+v", r1.outcome, r2.outcome)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM device_relationships WHERE (device_a_id=$1 AND device_b_id=$2) OR (device_a_id=$2 AND device_b_id=$1)`, deviceA, deviceB).Scan(&count); err != nil { t.Fatal(err) }
	if count != 1 { t.Fatalf("canonical relationship count=%d, want 1", count) }
}

func TestPostgresPairingConflictingCreateIdempotencyIsRejected(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	service, err := pairing.New(store)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pair-conflict-%d", time.Now().UnixNano())
	userA, deviceA := prefix+"-ua", prefix+"-da"
	userB, deviceB := prefix+"-ub", prefix+"-db"
	enrollPairingDevice(t, store, userA, deviceA, "secret-a")
	enrollPairingDevice(t, store, userB, deviceB, "secret-b")
	cleanupPairingPrefix(t, store, prefix)

	key := prefix+"-create"
	if _, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-original", key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-conflicting", key); !idempotency.IsConflict(err) {
		t.Fatalf("conflicting create err=%v, want %s", err, idempotency.ConflictCode)
	}
}

func TestPostgresPairingRejectIsDurableAndIdempotent(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	service, err := pairing.New(store)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pair-reject-%d", time.Now().UnixNano())
	userA, deviceA := prefix+"-ua", prefix+"-da"
	userB, deviceB := prefix+"-ub", prefix+"-db"
	enrollPairingDevice(t, store, userA, deviceA, "secret-a")
	enrollPairingDevice(t, store, userB, deviceB, "secret-b")
	cleanupPairingPrefix(t, store, prefix)

	created, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-reject", prefix+"-create")
	if err != nil { t.Fatal(err) }
	rejected, replayed, err := service.Reject(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, created.ID, "user_declined", prefix+"-reject")
	if err != nil || replayed || rejected.State != "cancelled" { t.Fatalf("reject=%+v replayed=%v err=%v", rejected, replayed, err) }
	replayedReject, replayed, err := service.Reject(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, created.ID, "user_declined", prefix+"-reject")
	if err != nil || !replayed || replayedReject.State != "cancelled" { t.Fatalf("reject replay=%+v replayed=%v err=%v", replayedReject, replayed, err) }
	if _, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, created.ID, created.InitiatorNonce, prefix+"-confirm-after-reject"); !errors.Is(err, pairing.ErrSessionClosed) {
		t.Fatalf("confirm after reject err=%v, want session closed", err)
	}
}

func TestPostgresPairingExpiryPersistsBeforeReturningExpired(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	service, err := pairing.New(store)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pair-expire-%d", time.Now().UnixNano())
	userA, deviceA := prefix+"-ua", prefix+"-da"
	userB, deviceB := prefix+"-ub", prefix+"-db"
	enrollPairingDevice(t, store, userA, deviceA, "secret-a")
	enrollPairingDevice(t, store, userB, deviceB, "secret-b")
	cleanupPairingPrefix(t, store, prefix)

	created, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-expire", prefix+"-create")
	if err != nil { t.Fatal(err) }
	if _, err := pool.Exec(ctx, `UPDATE pairing_sessions SET expires_at=now()-interval '1 second' WHERE session_id=$1`, created.ID); err != nil { t.Fatal(err) }
	outcome, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, created.ID, created.InitiatorNonce, prefix+"-confirm-expired")
	if !errors.Is(err, pairing.ErrSessionExpired) || outcome.Session.State != "expired" {
		t.Fatalf("expired outcome=%+v err=%v", outcome, err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM pairing_sessions WHERE session_id=$1`, created.ID).Scan(&state); err != nil { t.Fatal(err) }
	if state != "expired" { t.Fatalf("persisted state=%q, want expired", state) }
}

package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation LIKE 'pairing.%'`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM pairing_sessions WHERE initiator_device_id LIKE $1 OR peer_device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_relationships WHERE device_a_id LIKE $1 OR device_b_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox WHERE subject LIKE 'relationship/%' AND data_json->>'device_a_id' LIKE $1`, prefix+"%")
	})

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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM idempotency_records WHERE actor_id LIKE $1 AND operation LIKE 'pairing.%'`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM pairing_sessions WHERE initiator_device_id LIKE $1 OR peer_device_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_relationships WHERE device_a_id LIKE $1 OR device_b_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id LIKE $1`, prefix+"%")
	})

	s1, _, err := service.Create(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, deviceB, "rf-a", prefix+"-c1")
	if err != nil { t.Fatal(err) }
	s2, _, err := service.Create(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, deviceA, "rf-b", prefix+"-c2")
	if err != nil { t.Fatal(err) }
	if _, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, s1.ID, s1.InitiatorNonce, prefix+"-s1a"); err != nil { t.Fatal(err) }
	r1, err := service.Confirm(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, s1.ID, s1.PeerNonce, prefix+"-s1b")
	if err != nil { t.Fatal(err) }
	if _, err := service.Confirm(ctx, pairing.Participant{UserID:userB,DeviceID:deviceB}, s2.ID, s2.InitiatorNonce, prefix+"-s2b"); err != nil { t.Fatal(err) }
	r2, err := service.Confirm(ctx, pairing.Participant{UserID:userA,DeviceID:deviceA}, s2.ID, s2.PeerNonce, prefix+"-s2a")
	if err != nil { t.Fatal(err) }
	if r1.RelationshipID != r2.RelationshipID { t.Fatalf("relationship IDs diverged: %q != %q", r1.RelationshipID, r2.RelationshipID) }
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM device_relationships WHERE (device_a_id=$1 AND device_b_id=$2) OR (device_a_id=$2 AND device_b_id=$1)`, deviceA, deviceB).Scan(&count); err != nil { t.Fatal(err) }
	if count != 1 { t.Fatalf("canonical relationship count=%d, want 1", count) }
}

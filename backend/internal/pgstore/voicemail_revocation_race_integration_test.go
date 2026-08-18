package pgstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/privacy"
	"companion-server/internal/voicemail"
)

// TestPostgresVoiceMailCompleteVsRevokeSerializes proves the real PostgreSQL
// lock boundary rather than only checking a sequential revoke-then-complete
// flow. An uncommitted relationship revocation holds the row update lock while
// CompleteUpload starts concurrently. Complete must wait, then observe the
// committed revocation and durably record a terminal rejected state.
func TestPostgresVoiceMailCompleteVsRevokeSerializes(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("vm-race-%d", time.Now().UnixNano())
	sender, recipient := prefix+"-sender", prefix+"-recipient"
	senderDevice, recipientDevice := prefix+"-sender-device", prefix+"-recipient-device"
	now := time.Now().UTC()

	for _, d := range []struct{ user, device string }{
		{sender, senderDevice},
		{recipient, recipientDevice},
	} {
		if err := store.EnrollDevice(ctx, domain.Identity{UserID: d.user, DeviceID: d.device}, "token-"+d.device+"-sufficiently-long"); err != nil {
			t.Fatal(err)
		}
		if err := store.SetPrivacyPolicy(ctx, privacy.Policy{UserID: d.user, VoiceMailPolicy: "ephemeral", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	devA, userA, devB, userB := senderDevice, sender, recipientDevice, recipient
	if devB < devA {
		devA, devB = devB, devA
		userA, userB = userB, userA
	}
	relationshipID := prefix + "-relationship"
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1,$2,$3,$4,$5,$6)`, relationshipID, devA, devB, userA, userB, now); err != nil {
		t.Fatal(err)
	}

	create := voicemail.Create{
		RelationshipID: relationshipID,
		SenderUserID:   sender,
		SenderDeviceID: senderDevice,
		DurationMS:     1000,
		SizeBytes:      20,
		ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Policy:         voicemail.Ephemeral,
		ExpiresAt:      now.Add(time.Hour),
	}
	actor := sender + ":device:" + senderDevice
	createReq := voiceMailMutation(t, actor, "voice_mail.create", prefix+"-create", create)
	item, err := store.CreateUpload(ctx, createReq, create, now)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the revocation transaction open. CompleteUpload's FOR SHARE on this
	// relationship must wait for this UPDATE transaction to resolve.
	revokeTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer revokeTx.Rollback(ctx)
	if _, err := revokeTx.Exec(ctx, `UPDATE device_relationships SET revoked_at=$1 WHERE relationship_id=$2`, now.Add(time.Second), relationshipID); err != nil {
		t.Fatal(err)
	}

	completeReq := voiceMailMutation(t, actor, "voice_mail.complete", prefix+"-complete", map[string]string{"id": item.ID})
	type result struct {
		item voicemail.Item
		err  error
	}
	started := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		close(started)
		completed, completeErr := store.CompleteUpload(ctx, completeReq, sender, senderDevice, item.ID, now.Add(2*time.Second))
		done <- result{item: completed, err: completeErr}
	}()
	<-started

	select {
	case r := <-done:
		t.Fatalf("complete escaped relationship lock before revoke commit: item=%+v err=%v", r.item, r.err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := revokeTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var completed result
	select {
	case completed = <-done:
	case <-ctx.Done():
		t.Fatalf("complete did not resume after revoke commit: %v", ctx.Err())
	}
	if !errors.Is(completed.err, voicemail.ErrRelationshipRevoked) {
		t.Fatalf("complete err=%v want=%v", completed.err, voicemail.ErrRelationshipRevoked)
	}
	if completed.item.State != voicemail.Rejected {
		t.Fatalf("complete state=%q want=%q", completed.item.State, voicemail.Rejected)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM voice_mail_items WHERE id=$1`, item.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(voicemail.Rejected) {
		t.Fatalf("durable state=%q want=%q", state, voicemail.Rejected)
	}

	replayed, err := store.CompleteUpload(ctx, completeReq, sender, senderDevice, item.ID, now.Add(3*time.Second))
	if !errors.Is(err, voicemail.ErrRelationshipRevoked) {
		t.Fatalf("replay err=%v want=%v", err, voicemail.ErrRelationshipRevoked)
	}
	if replayed.State != voicemail.Rejected || replayed.ID != item.ID {
		t.Fatalf("replay=%+v", replayed)
	}

	var available int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.available' AND subject=$1`, "voice-mail/"+item.ID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatalf("rejected send emitted %d availability events", available)
	}

	cleanup, err := store.ClaimCleanup(ctx, now.Add(4*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range cleanup {
		if candidate.ID == item.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rejected item %s was not claimable for cleanup: %+v", item.ID, cleanup)
	}
}

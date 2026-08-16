package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"
	"companion-server/internal/privacy"
	"companion-server/internal/voicemail"
)

func voiceMailMutation(t *testing.T, actor, operation, key string, value any) idempotency.Request {
	t.Helper()
	hash, err := idempotency.HashValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return idempotency.Request{Actor: actor, Operation: operation, Key: key, RequestHash: hash}
}

func TestPostgresVoiceMailLifecycleIdempotencyPrivacyAndOwnership(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("vm-%d", time.Now().UnixNano())
	sender, recipient, outsider := prefix+"-sender", prefix+"-recipient", prefix+"-outsider"
	senderDevice, recipientDevice := "sender-device-"+prefix, "recipient-device-"+prefix
	outsiderDevice := "outsider-device-"+prefix
	now := time.Now().UTC()

	// Register device credentials and paired relationship
	for _, d := range []struct{ user, dev string }{
		{sender, senderDevice}, {recipient, recipientDevice}, {outsider, outsiderDevice},
	} {
		if err := store.EnrollDevice(ctx, domain.Identity{UserID: d.user, DeviceID: d.dev}, "token-"+d.dev+"-sufficiently-long"); err != nil {
			t.Fatal(err)
		}
	}
	relID := prefix + "-rel"
	devA, userA, devB, userB := senderDevice, sender, recipientDevice, recipient
	if devB < devA {
		devA, devB = devB, devA
		userA, userB = userB, userA
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1, $2, $3, $4, $5, now())`, relID, devA, devB, userA, userB); err != nil {
		t.Fatal(err)
	}

	create := voicemail.Create{
		RelationshipID: relID,
		SenderUserID:   sender,
		SenderDeviceID: senderDevice,
		DurationMS:     1000,
		SizeBytes:      20,
		ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Policy:         voicemail.Ephemeral,
		ExpiresAt:      now.Add(time.Hour),
	}
	senderActor := sender + ":device:" + senderDevice
	createReq := voiceMailMutation(t, senderActor, "voice_mail.create", prefix+"-create", create)

	// Disabled policy is a pre-commit failure: the same request remains retryable.
	if _, err := store.CreateUpload(ctx, createReq, create, now); err == nil {
		t.Fatal("disabled policy accepted")
	}
	for _, user := range []string{sender, recipient} {
		if err := store.SetPrivacyPolicy(ctx, privacy.Policy{UserID: user, VoiceMailPolicy: "ephemeral", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	created, err := store.CreateUpload(ctx, createReq, create, now)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateUpload(ctx, createReq, create, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID || replayed.ObjectKey != created.ObjectKey {
		t.Fatalf("replay changed outcome: first=%+v replay=%+v", created, replayed)
	}
	conflict := createReq
	conflict.RequestHash, _ = idempotency.HashValue(map[string]any{"different": true})
	if _, err := store.CreateUpload(ctx, conflict, create, now); !idempotency.IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}

	completeReq := voiceMailMutation(t, senderActor, "voice_mail.complete", prefix+"-complete", map[string]string{"id": created.ID})
	completed, err := store.CompleteUpload(ctx, completeReq, sender, senderDevice, created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != voicemail.Unread {
		t.Fatalf("completed state=%s", completed.State)
	}
	completedReplay, err := store.CompleteUpload(ctx, completeReq, sender, senderDevice, created.ID, now.Add(time.Second))
	if err != nil || completedReplay.State != voicemail.Unread {
		t.Fatalf("complete replay=%+v err=%v", completedReplay, err)
	}
	var availableEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.available' AND subject=$1`, "voice-mail/"+created.ID).Scan(&availableEvents); err != nil || availableEvents != 1 {
		t.Fatalf("available events=%d err=%v", availableEvents, err)
	}

	items, err := store.ListUnread(ctx, recipient, recipientDevice, now, 10)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("unread=%+v err=%v", items, err)
	}
	wrongClaim := voiceMailMutation(t, outsider+":device:"+outsiderDevice, "voice_mail.claim", prefix+"-wrong-claim", map[string]string{"id": created.ID})
	if _, err := store.ClaimVoiceMail(ctx, wrongClaim, outsider, outsiderDevice, created.ID, "play-wrong", now, now.Add(time.Minute)); err == nil {
		t.Fatal("cross-owner claim succeeded")
	}

	recipientActor := recipient + ":device:" + recipientDevice
	claimReq := voiceMailMutation(t, recipientActor, "voice_mail.claim", prefix+"-claim", map[string]string{"id": created.ID, "playback": "play-1"})
	claimed, err := store.ClaimVoiceMail(ctx, claimReq, recipient, recipientDevice, created.ID, "play-1", now, now.Add(time.Minute))
	if err != nil || claimed.State != voicemail.Claimed {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	failReq := voiceMailMutation(t, recipientActor, "voice_mail.playback", prefix+"-failed", map[string]any{"id": created.ID, "succeeded": false})
	released, err := store.CompleteVoiceMailPlayback(ctx, failReq, recipient, recipientDevice, created.ID, "play-1", false, now.Add(time.Second))
	if err != nil || released.State != voicemail.Unread {
		t.Fatalf("released=%+v err=%v", released, err)
	}

	claimReq2 := voiceMailMutation(t, recipientActor, "voice_mail.claim", prefix+"-claim-2", map[string]string{"id": created.ID, "playback": "play-2"})
	if _, err := store.ClaimVoiceMail(ctx, claimReq2, recipient, recipientDevice, "", "play-2", now.Add(2*time.Second), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	successReq := voiceMailMutation(t, recipientActor, "voice_mail.playback", prefix+"-success", map[string]any{"id": created.ID, "succeeded": true})
	consumed, err := store.CompleteVoiceMailPlayback(ctx, successReq, recipient, recipientDevice, created.ID, "play-2", true, now.Add(3*time.Second))
	if err != nil || consumed.State != voicemail.DeletePending {
		t.Fatalf("consumed=%+v err=%v", consumed, err)
	}
	if _, ok, err := store.ItemForPlayback(ctx, recipient, recipientDevice, created.ID, "play-2", now); err != nil || ok {
		t.Fatalf("consumed media remained accessible: ok=%v err=%v", ok, err)
	}
	if err := store.MarkDeleted(ctx, created.ID, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeleted(ctx, created.ID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	expiredID := prefix + "-expired"
	if _, err := pool.Exec(ctx, `INSERT INTO voice_mail_items(id,relationship_id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'ogg_opus',1000,20,$8,'ephemeral','unread',$9,$10,$10)`, expiredID, relID, sender, senderDevice, recipient, recipientDevice, prefix+"-expired-object", create.ChecksumSHA256, now.Add(-time.Minute), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	pendingExpiredID := prefix + "-pending-expired"
	if _, err := pool.Exec(ctx, `INSERT INTO voice_mail_items(id,relationship_id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'ogg_opus',1000,20,$8,'ephemeral','pending_upload',$9,$10,$10)`, pendingExpiredID, relID, sender, senderDevice, recipient, recipientDevice, prefix+"-pending-expired-object", create.ChecksumSHA256, now.Add(-time.Minute), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.ClaimCleanup(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundExpired := false
	for _, item := range cleanup {
		if item.ID == expiredID && item.State == voicemail.DeletePending {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("expired item was not claimed for cleanup: %+v", cleanup)
	}
	var expiredEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.expired' AND subject=$1`, "voice-mail/"+expiredID).Scan(&expiredEvents); err != nil || expiredEvents != 1 {
		t.Fatalf("expired events=%d err=%v", expiredEvents, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.expired' AND subject=$1`, "voice-mail/"+pendingExpiredID).Scan(&expiredEvents); err != nil || expiredEvents != 0 {
		t.Fatalf("pending upload leaked expiry event: events=%d err=%v", expiredEvents, err)
	}
	retry, err := store.ClaimCleanup(ctx, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	foundRetry := false
	for _, item := range retry {
		if item.ID == expiredID {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatal("failed deletion would not be retried")
	}

	retainedSender, retainedRecipient := prefix+"-retained-sender", prefix+"-retained-recipient"
	retainedSenderDevice, retainedRecipientDevice := "retained-s-"+prefix, "retained-r-"+prefix
	for _, d := range []struct{ user, dev string }{
		{retainedSender, retainedSenderDevice}, {retainedRecipient, retainedRecipientDevice},
	} {
		if err := store.EnrollDevice(ctx, domain.Identity{UserID: d.user, DeviceID: d.dev}, "token-"+d.dev+"-sufficiently-long"); err != nil {
			t.Fatal(err)
		}
	}
	retainedRelID := prefix + "-retained-rel"
	devA, userA, devB, userB = retainedSenderDevice, retainedSender, retainedRecipientDevice, retainedRecipient
	if devB < devA {
		devA, devB = devB, devA
		userA, userB = userB, userA
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1, $2, $3, $4, $5, now())`, retainedRelID, devA, devB, userA, userB); err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{retainedSender, retainedRecipient} {
		if err := store.SetPrivacyPolicy(ctx, privacy.Policy{UserID: user, VoiceMailPolicy: "retained", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	retainedCreate := voicemail.Create{
		RelationshipID: retainedRelID,
		SenderUserID:   retainedSender,
		SenderDeviceID: retainedSenderDevice,
		DurationMS:     1000,
		SizeBytes:      20,
		ChecksumSHA256: create.ChecksumSHA256,
		Policy:         voicemail.Retained,
		ExpiresAt:      now.Add(time.Hour),
	}
	retainedActor := retainedSender + ":device:" + retainedSenderDevice
	retainedCreateReq := voiceMailMutation(t, retainedActor, "voice_mail.create", prefix+"-retained-create", retainedCreate)
	retained, err := store.CreateUpload(ctx, retainedCreateReq, retainedCreate, now)
	if err != nil {
		t.Fatal(err)
	}
	retainedCompleteReq := voiceMailMutation(t, retainedActor, "voice_mail.complete", prefix+"-retained-complete", map[string]string{"id": retained.ID})
	if _, err := store.CompleteUpload(ctx, retainedCompleteReq, retainedSender, retainedSenderDevice, retained.ID, now); err != nil {
		t.Fatal(err)
	}
	retainedClaimActor := retainedRecipient + ":device:" + retainedRecipientDevice
	retainedClaimReq := voiceMailMutation(t, retainedClaimActor, "voice_mail.claim", prefix+"-retained-claim", map[string]string{"id": retained.ID})
	if _, err := store.ClaimVoiceMail(ctx, retainedClaimReq, retainedRecipient, retainedRecipientDevice, retained.ID, "retained-play", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	retainedPlayReq := voiceMailMutation(t, retainedClaimActor, "voice_mail.playback", prefix+"-retained-play", map[string]string{"id": retained.ID})
	retainedPlayed, err := store.CompleteVoiceMailPlayback(ctx, retainedPlayReq, retainedRecipient, retainedRecipientDevice, retained.ID, "retained-play", true, now.Add(time.Second))
	if err != nil || retainedPlayed.State != voicemail.Unread {
		t.Fatalf("retained playback=%+v err=%v", retainedPlayed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.available' AND subject=$1`, "voice-mail/"+retained.ID).Scan(&availableEvents); err != nil || availableEvents != 2 {
		t.Fatalf("retained available events=%d err=%v", availableEvents, err)
	}
}

func TestPostgresVoiceMailRevocationAndRePairGenerationIsolation(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("vm-rev-%d", time.Now().UnixNano())
	userA, userB := prefix+"-alice", prefix+"-bob"
	devA, devB := "dev-a-"+prefix, "dev-b-"+prefix
	now := time.Now().UTC()

	// 1. Register device credentials
	for _, d := range []struct{ user, dev string }{
		{userA, devA}, {userB, devB},
	} {
		if err := store.EnrollDevice(ctx, domain.Identity{UserID: d.user, DeviceID: d.dev}, "token-"+d.dev+"-sufficiently-long"); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{userA, userB} {
		if err := store.SetPrivacyPolicy(ctx, privacy.Policy{UserID: u, VoiceMailPolicy: "ephemeral", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	// 2. Pair generation 1 (rel-1)
	rel1ID := prefix + "-rel-1"
	canonDevA, canonDevB := devA, devB
	canonUserA, canonUserB := userA, userB
	if canonDevB < canonDevA {
		canonDevA, canonDevB = canonDevB, canonDevA
		canonUserA, canonUserB = canonUserB, canonUserA
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1, $2, $3, $4, $5, now())`, rel1ID, canonDevA, canonDevB, canonUserA, canonUserB); err != nil {
		t.Fatal(err)
	}

	// List recipients should show rel1
	recipients, err := store.ListAuthorizedRecipients(ctx, pairing.Participant{UserID: userA, DeviceID: devA})
	if err != nil || len(recipients) != 1 || recipients[0].RelationshipID != rel1ID || recipients[0].PeerDeviceID != devB {
		t.Fatalf("unexpected recipients: %+v err=%v", recipients, err)
	}

	// Create pending upload under rel-1
	create1 := voicemail.Create{
		RelationshipID: rel1ID,
		SenderUserID:   userA,
		SenderDeviceID: devA,
		DurationMS:     2000,
		SizeBytes:      50,
		ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Policy:         voicemail.Ephemeral,
		ExpiresAt:      now.Add(time.Hour),
	}
	actorA := userA + ":device:" + devA
	create1Req := voiceMailMutation(t, actorA, "voice_mail.create", prefix+"-c1", create1)
	item1, err := store.CreateUpload(ctx, create1Req, create1, now)
	if err != nil {
		t.Fatalf("CreateUpload failed: %v", err)
	}

	// 3. Revoke relationship rel-1
	if err := store.RevokeRelationship(ctx, pairing.Participant{UserID: userB, DeviceID: devB}, rel1ID, now.Add(time.Second)); err != nil {
		t.Fatalf("RevokeRelationship failed: %v", err)
	}

	// Active recipients should now be empty
	recipientsAfterRevoke, err := store.ListAuthorizedRecipients(ctx, pairing.Participant{UserID: userA, DeviceID: devA})
	if err != nil || len(recipientsAfterRevoke) != 0 {
		t.Fatalf("expected 0 active recipients after revoke, got %+v", recipientsAfterRevoke)
	}

	// Creating a new upload under revoked rel-1 must fail
	createRevokedReq := voiceMailMutation(t, actorA, "voice_mail.create", prefix+"-c-rev", create1)
	if _, err := store.CreateUpload(ctx, createRevokedReq, create1, now.Add(2*time.Second)); err == nil {
		t.Fatal("expected CreateUpload to fail under revoked relationship")
	}

	// Completing pending upload under revoked rel-1 must fail closed
	complete1Req := voiceMailMutation(t, actorA, "voice_mail.complete", prefix+"-comp1", map[string]string{"id": item1.ID})
	if _, err := store.CompleteUpload(ctx, complete1Req, userA, devA, item1.ID, now.Add(2*time.Second)); err == nil {
		t.Fatal("expected CompleteUpload to fail under revoked relationship")
	}

	// Staged item1 must NOT become unread
	unread, err := store.ListUnread(ctx, userB, devB, now.Add(3*time.Second), 10)
	if err != nil || len(unread) != 0 {
		t.Fatalf("expected 0 unread messages, got %+v", unread)
	}

	// 4. Re-pair creates generation 2 (rel-2)
	rel2ID := prefix + "-rel-2"
	if _, err := pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1, $2, $3, $4, $5, now())`, rel2ID, canonDevA, canonDevB, canonUserA, canonUserB); err != nil {
		t.Fatal(err)
	}

	// List recipients should now show rel2
	recipientsGen2, err := store.ListAuthorizedRecipients(ctx, pairing.Participant{UserID: userA, DeviceID: devA})
	if err != nil || len(recipientsGen2) != 1 || recipientsGen2[0].RelationshipID != rel2ID {
		t.Fatalf("expected rel2 in recipients, got %+v", recipientsGen2)
	}

	// Old item1 from rel-1 still CANNOT be completed
	if _, err := store.CompleteUpload(ctx, complete1Req, userA, devA, item1.ID, now.Add(4*time.Second)); err == nil {
		t.Fatal("expected old generation upload to remain rejected even after re-pair")
	}

	// New upload under rel-2 succeeds
	create2 := create1
	create2.RelationshipID = rel2ID
	create2Req := voiceMailMutation(t, actorA, "voice_mail.create", prefix+"-c2", create2)
	item2, err := store.CreateUpload(ctx, create2Req, create2, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("CreateUpload under rel-2 failed: %v", err)
	}
	complete2Req := voiceMailMutation(t, actorA, "voice_mail.complete", prefix+"-comp2", map[string]string{"id": item2.ID})
	completed2, err := store.CompleteUpload(ctx, complete2Req, userA, devA, item2.ID, now.Add(6*time.Second))
	if err != nil || completed2.State != voicemail.Unread {
		t.Fatalf("CompleteUpload under rel-2 failed: %+v err=%v", completed2, err)
	}
}

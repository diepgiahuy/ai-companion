package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/idempotency"
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
	now := time.Now().UTC()
	create := voicemail.Create{SenderUserID: sender, SenderDeviceID: "sender-device", RecipientUserID: recipient, RecipientDeviceID: "recipient-device", DurationMS: 1000, SizeBytes: 20, ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Policy: voicemail.Ephemeral, ExpiresAt: now.Add(time.Hour)}
	createReq := voiceMailMutation(t, sender, "voice_mail.create", prefix+"-create", create)

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

	completeReq := voiceMailMutation(t, sender, "voice_mail.complete", prefix+"-complete", map[string]string{"id": created.ID})
	completed, err := store.CompleteUpload(ctx, completeReq, sender, created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != voicemail.Unread {
		t.Fatalf("completed state=%s", completed.State)
	}
	completedReplay, err := store.CompleteUpload(ctx, completeReq, sender, created.ID, now.Add(time.Second))
	if err != nil || completedReplay.State != voicemail.Unread {
		t.Fatalf("complete replay=%+v err=%v", completedReplay, err)
	}
	var availableEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.available' AND subject=$1`, "voice-mail/"+created.ID).Scan(&availableEvents); err != nil || availableEvents != 1 {
		t.Fatalf("available events=%d err=%v", availableEvents, err)
	}

	items, err := store.ListUnread(ctx, recipient, "recipient-device", now, 10)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("unread=%+v err=%v", items, err)
	}
	wrongClaim := voiceMailMutation(t, outsider, "voice_mail.claim", prefix+"-wrong-claim", map[string]string{"id": created.ID})
	if _, err := store.ClaimVoiceMail(ctx, wrongClaim, outsider, "outsider-device", created.ID, "play-wrong", now, now.Add(time.Minute)); err == nil {
		t.Fatal("cross-owner claim succeeded")
	}

	claimReq := voiceMailMutation(t, recipient, "voice_mail.claim", prefix+"-claim", map[string]string{"id": created.ID, "playback": "play-1"})
	claimed, err := store.ClaimVoiceMail(ctx, claimReq, recipient, "recipient-device", created.ID, "play-1", now, now.Add(time.Minute))
	if err != nil || claimed.State != voicemail.Claimed {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	failReq := voiceMailMutation(t, recipient, "voice_mail.playback", prefix+"-failed", map[string]any{"id": created.ID, "succeeded": false})
	released, err := store.CompleteVoiceMailPlayback(ctx, failReq, recipient, created.ID, "play-1", false, now.Add(time.Second))
	if err != nil || released.State != voicemail.Unread {
		t.Fatalf("released=%+v err=%v", released, err)
	}

	claimReq2 := voiceMailMutation(t, recipient, "voice_mail.claim", prefix+"-claim-2", map[string]string{"id": created.ID, "playback": "play-2"})
	if _, err := store.ClaimVoiceMail(ctx, claimReq2, recipient, "recipient-device", "", "play-2", now.Add(2*time.Second), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	successReq := voiceMailMutation(t, recipient, "voice_mail.playback", prefix+"-success", map[string]any{"id": created.ID, "succeeded": true})
	consumed, err := store.CompleteVoiceMailPlayback(ctx, successReq, recipient, created.ID, "play-2", true, now.Add(3*time.Second))
	if err != nil || consumed.State != voicemail.DeletePending {
		t.Fatalf("consumed=%+v err=%v", consumed, err)
	}
	if _, ok, err := store.ItemForPlayback(ctx, recipient, created.ID, "play-2", now); err != nil || ok {
		t.Fatalf("consumed media remained accessible: ok=%v err=%v", ok, err)
	}
	if err := store.MarkDeleted(ctx, created.ID, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeleted(ctx, created.ID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	expiredID := prefix + "-expired"
	if _, err := pool.Exec(ctx, `INSERT INTO voice_mail_items(id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,expires_at,created_at,updated_at) VALUES($1,$2,'sender-device',$3,'recipient-device',$4,'ogg_opus',1000,20,$5,'ephemeral','unread',$6,$7,$7)`, expiredID, sender, recipient, prefix+"-expired-object", create.ChecksumSHA256, now.Add(-time.Minute), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	pendingExpiredID := prefix + "-pending-expired"
	if _, err := pool.Exec(ctx, `INSERT INTO voice_mail_items(id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,expires_at,created_at,updated_at) VALUES($1,$2,'sender-device',$3,'recipient-device',$4,'ogg_opus',1000,20,$5,'ephemeral','pending_upload',$6,$7,$7)`, pendingExpiredID, sender, recipient, prefix+"-pending-expired-object", create.ChecksumSHA256, now.Add(-time.Minute), now.Add(-time.Hour)); err != nil {
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
	for _, user := range []string{retainedSender, retainedRecipient} {
		if err := store.SetPrivacyPolicy(ctx, privacy.Policy{UserID: user, VoiceMailPolicy: "retained", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	retainedCreate := create
	retainedCreate.SenderUserID = retainedSender
	retainedCreate.RecipientUserID = retainedRecipient
	retainedCreate.Policy = voicemail.Retained
	retainedCreateReq := voiceMailMutation(t, retainedSender, "voice_mail.create", prefix+"-retained-create", retainedCreate)
	retained, err := store.CreateUpload(ctx, retainedCreateReq, retainedCreate, now)
	if err != nil {
		t.Fatal(err)
	}
	retainedCompleteReq := voiceMailMutation(t, retainedSender, "voice_mail.complete", prefix+"-retained-complete", map[string]string{"id": retained.ID})
	if _, err := store.CompleteUpload(ctx, retainedCompleteReq, retainedSender, retained.ID, now); err != nil {
		t.Fatal(err)
	}
	retainedClaimReq := voiceMailMutation(t, retainedRecipient, "voice_mail.claim", prefix+"-retained-claim", map[string]string{"id": retained.ID})
	if _, err := store.ClaimVoiceMail(ctx, retainedClaimReq, retainedRecipient, "recipient-device", retained.ID, "retained-play", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	retainedPlayReq := voiceMailMutation(t, retainedRecipient, "voice_mail.playback", prefix+"-retained-play", map[string]string{"id": retained.ID})
	retainedPlayed, err := store.CompleteVoiceMailPlayback(ctx, retainedPlayReq, retainedRecipient, retained.ID, "retained-play", true, now.Add(time.Second))
	if err != nil || retainedPlayed.State != voicemail.Unread {
		t.Fatalf("retained playback=%+v err=%v", retainedPlayed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='voice_mail.available' AND subject=$1`, "voice-mail/"+retained.ID).Scan(&availableEvents); err != nil || availableEvents != 2 {
		t.Fatalf("retained available events=%d err=%v", availableEvents, err)
	}
}

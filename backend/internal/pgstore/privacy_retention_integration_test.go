package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/memory"
	"companion-server/internal/privacy"
)

func TestPostgresPrivacyRetentionDeletesOnlyExpiredOwnerData(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-privacy-%d", time.Now().UnixNano())
	user := prefix + "-user"
	other := prefix + "-other"
	device := prefix + "-device"
	now := time.Now().UTC().Truncate(time.Second)
	old := now.AddDate(0, 0, -30)
	recent := now.AddDate(0, 0, -1)

	if err := store.SetPrivacyPolicy(ctx, privacy.Policy{
		UserID:                    user,
		SaveVoiceAudio:            true,
		VoiceMailPolicy:           "retained",
		LongTermMemoryEnabled:     true,
		ConversationRetentionDays: 7,
		VoiceMemoRetentionDays:    7,
		MemoryRetentionDays:       7,
		UpdatedAt:                 now,
	}); err != nil {
		t.Fatal(err)
	}

	scope := conversationctx.Scope{UserID: user, ThreadID: prefix + "-thread"}
	if err := store.Append(ctx, prefix+"-conversation-old", scope, "user", "expired conversation"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, prefix+"-conversation-recent", scope, "assistant", "recent conversation"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversation_messages SET created_at=$1 WHERE turn_key=$2 AND user_id=$3`, old, prefix+"-conversation-old", user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversation_messages SET created_at=$1 WHERE turn_key=$2 AND user_id=$3`, recent, prefix+"-conversation-recent", user); err != nil {
		t.Fatal(err)
	}

	if _, err := store.UpsertMemory(ctx, memory.Item{UserID: user, Key: prefix + "-memory-old", Kind: memory.Semantic, Value: "expired memory", ValidFrom: old, CreatedAt: old, Source: "test", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMemory(ctx, memory.Item{UserID: user, Key: prefix + "-memory-recent", Kind: memory.Semantic, Value: "recent memory", ValidFrom: recent, CreatedAt: recent, Source: "test", Confidence: 1}); err != nil {
		t.Fatal(err)
	}

	oldPath := "/tmp/" + prefix + "-old.opus"
	recentPath := "/tmp/" + prefix + "-recent.opus"
	if err := store.CreateVoiceMemo(ctx, user, prefix+"-voice-old", device, oldPath, "expired voice", 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateVoiceMemo(ctx, user, prefix+"-voice-recent", device, recentPath, "recent voice", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE voice_memos SET created_at=$1 WHERE user_id=$2 AND idempotency_key=$3`, old, user, prefix+"-voice-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE voice_memos SET created_at=$1 WHERE user_id=$2 AND idempotency_key=$3`, recent, user, prefix+"-voice-recent"); err != nil {
		t.Fatal(err)
	}

	otherScope := conversationctx.Scope{UserID: other, ThreadID: prefix + "-other-thread"}
	if err := store.Append(ctx, prefix+"-other-conversation", otherScope, "user", "must survive"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversation_messages SET created_at=$1 WHERE turn_key=$2 AND user_id=$3`, old, prefix+"-other-conversation", other); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMemory(ctx, memory.Item{UserID: other, Key: prefix + "-other-memory", Kind: memory.Semantic, Value: "must survive", ValidFrom: old, CreatedAt: old, Source: "test", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateVoiceMemo(ctx, other, prefix+"-other-voice", device, "/tmp/"+prefix+"-other.opus", "must survive", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE voice_memos SET created_at=$1 WHERE user_id=$2 AND idempotency_key=$3`, old, other, prefix+"-other-voice"); err != nil {
		t.Fatal(err)
	}

	report, err := store.ApplyRetention(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.ConversationRows != 1 || report.MemoryRows != 1 || report.VoiceMemoRows != 1 {
		t.Fatalf("unexpected retention report: %+v", report)
	}
	if len(report.OrphanPaths) != 1 || report.OrphanPaths[0] != oldPath {
		t.Fatalf("orphan paths = %v, want [%s]", report.OrphanPaths, oldPath)
	}

	assertCount := func(query string, args []any, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("count for %q = %d, want %d", query, got, want)
		}
	}
	assertCount(`SELECT count(*) FROM conversation_messages WHERE user_id=$1 AND turn_key IN ($2,$3)`, []any{user, prefix + "-conversation-old", prefix + "-conversation-recent"}, 1)
	assertCount(`SELECT count(*) FROM memories WHERE user_id=$1 AND memory_key IN ($2,$3)`, []any{user, prefix + "-memory-old", prefix + "-memory-recent"}, 1)
	assertCount(`SELECT count(*) FROM voice_memos WHERE user_id=$1 AND idempotency_key IN ($2,$3)`, []any{user, prefix + "-voice-old", prefix + "-voice-recent"}, 1)
	assertCount(`SELECT count(*) FROM conversation_messages WHERE user_id=$1 AND turn_key=$2`, []any{other, prefix + "-other-conversation"}, 1)
	assertCount(`SELECT count(*) FROM memories WHERE user_id=$1 AND memory_key=$2`, []any{other, prefix + "-other-memory"}, 1)
	assertCount(`SELECT count(*) FROM voice_memos WHERE user_id=$1 AND idempotency_key=$2`, []any{other, prefix + "-other-voice"}, 1)
}

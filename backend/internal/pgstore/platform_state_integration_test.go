package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/events"
	"companion-server/internal/idempotency"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/privacy"
	"companion-server/internal/usage"
)

func TestPostgresMemoryPrivacyAndUsageParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-platform-%d", time.Now().UnixNano())
	user := prefix + "-user"
	now := time.Now().UTC().Truncate(time.Second)

	item, err := store.UpsertMemory(ctx, memory.Item{
		UserID: user, Key: "language", Kind: memory.Semantic, Value: "Vietnamese",
		ValidFrom: now.Add(-2 * time.Hour), Source: "test", Confidence: 0.9,
		Embedding: []float32{0.1, 0.2}, CreatedAt: now.Add(-2 * time.Hour),
	})
	if err != nil || item.ID < 1 {
		t.Fatalf("memory=%+v err=%v", item, err)
	}
	if err := store.UpsertVector(ctx, user, item.ID, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchVectors(ctx, user, []float32{1, 0}, 10)
	if err != nil || len(hits) != 1 || hits[0].ID != item.ID || hits[0].Score < 0.99 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	current, err := store.CurrentMemories(ctx, user, now, 10)
	if err != nil || len(current) != 1 || current[0].Value != "Vietnamese" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	if err := store.ForgetMemory(ctx, user, "language"); err != nil {
		t.Fatal(err)
	}
	current, err = store.CurrentMemories(ctx, user, now.Add(time.Second), 10)
	if err != nil || len(current) != 0 {
		t.Fatalf("forgotten=%+v err=%v", current, err)
	}
	if err := store.DeleteVector(ctx, user, item.ID); err != nil {
		t.Fatal(err)
	}

	oldMemory, err := store.UpsertMemory(ctx, memory.Item{
		UserID: user, Key: "old", Kind: memory.Episodic, Value: "old memory",
		ValidFrom: now.Add(-72 * time.Hour), Source: "test", Confidence: 1,
		CreatedAt: now.Add(-48 * time.Hour),
	})
	if err != nil || oldMemory.ID < 1 {
		t.Fatalf("old memory=%+v err=%v", oldMemory, err)
	}

	policy := privacy.Policy{UserID: user, SaveVoiceAudio: false, LongTermMemoryEnabled: true, ConversationRetentionDays: 1, VoiceMemoRetentionDays: 1, MemoryRetentionDays: 1, UpdatedAt: now}
	if err := store.SetPrivacyPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetPrivacyPolicy(ctx, user)
	if err != nil || !ok || !got.LongTermMemoryEnabled || got.SaveVoiceAudio {
		t.Fatalf("policy=%+v ok=%v err=%v", got, ok, err)
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO conversation_messages(turn_key,user_id,thread_id,role,content,created_at) VALUES($1,$2,'default','user','old',$3)`, prefix+"-old-turn", user, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES($1,$2,'','/tmp/old-pg.wav','',10,$3)`, prefix+"-old-voice", user, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	report, err := store.ApplyRetention(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.ConversationRows != 1 || report.MemoryRows != 1 || report.VoiceMemoRows != 1 || len(report.OrphanPaths) != 1 || report.OrphanPaths[0] != "/tmp/old-pg.wav" {
		t.Fatalf("retention=%+v", report)
	}
	paths, err := store.ReferencedVoiceMemoPaths(ctx)
	if err != nil || len(paths) != 0 {
		t.Fatalf("referenced paths=%+v err=%v", paths, err)
	}

	record := usage.Record{UserID: user, DeviceID: "device", Provider: "test", Model: "model", PromptVersion: "v1", PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
	store.RecordUsage(ctx, record)
	total, err := store.TotalTokensSince(ctx, user, now.Add(-time.Minute))
	if err != nil || total != 8 {
		t.Fatalf("usage=%d err=%v", total, err)
	}
}

func TestPostgresDurableMemoryMutationParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-memory-mutation-%d", time.Now().UnixNano())
	user := prefix + "-user"
	now := time.Now().UTC().Truncate(time.Second)
	request := mutationRequest(t, prefix+"-actor", "memory.remember", prefix+"-remember", map[string]any{"key": "language", "value": "Vietnamese"})
	item := memory.Item{UserID: user, Key: " language ", Kind: memory.Semantic, Value: " Vietnamese ", ValidFrom: now, Source: "test", Confidence: 1, CreatedAt: now}

	first, err := store.UpsertMemoryMutation(ctx, request, item)
	if err != nil || first.ID < 1 || first.Key != "language" || first.Value != "Vietnamese" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replayed, err := store.UpsertMemoryMutation(ctx, request, item)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	current, err := store.CurrentMemories(ctx, user, now, 10)
	if err != nil || len(current) != 1 || current[0].ID != first.ID {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	conflict := request
	conflict.RequestHash, err = idempotency.HashValue(map[string]any{"key": "language", "value": "English"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMemoryMutation(ctx, conflict, item); !idempotency.IsConflict(err) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	forget := mutationRequest(t, prefix+"-actor", "memory.forget", prefix+"-forget", map[string]any{"key": "language"})
	if err := store.ForgetMemoryMutation(ctx, forget, user, " language "); err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetMemoryMutation(ctx, forget, user, " language "); err != nil {
		t.Fatalf("forget replay: %v", err)
	}
}

func TestPostgresDurableMarketWatchMutationParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-market-mutation-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"
	request := mutationRequest(t, prefix+"-actor", "market.watch.create", prefix+"-create", map[string]any{"provider": "test", "symbol": "XAU/USD", "currency": "USD", "operator": ">", "threshold": 3000.0})

	first, err := store.CreateMarketWatchMutation(ctx, request, user, device, "test", "XAU/USD", "usd", ">", 3000)
	if err != nil || first.ID < 1 || first.Currency != "USD" || !first.Enabled {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replayed, err := store.CreateMarketWatchMutation(ctx, request, user, device, "test", "XAU/USD", "usd", ">", 3000)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	watches, err := store.ListMarketWatches(ctx, user, device, 10)
	if err != nil || len(watches) != 1 || watches[0].ID != first.ID {
		t.Fatalf("watches=%+v err=%v", watches, err)
	}
	conflict := request
	conflict.RequestHash, err = idempotency.HashValue(map[string]any{"provider": "test", "symbol": "XAU/USD", "currency": "USD", "operator": ">", "threshold": 4000.0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMarketWatchMutation(ctx, conflict, user, device, "test", "XAU/USD", "USD", ">", 4000); !idempotency.IsConflict(err) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	deleteRequest := mutationRequest(t, prefix+"-actor", "market.watch.delete", prefix+"-delete", map[string]any{"id": first.ID})
	if err := store.DeleteMarketWatchMutation(ctx, deleteRequest, user, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMarketWatchMutation(ctx, deleteRequest, user, first.ID); err != nil {
		t.Fatalf("delete replay: %v", err)
	}
}

func TestPostgresOutboxSchedulerAndMarketParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Outbox and reminder claims are intentionally process-global. Earlier
	// integration cases and schema invariants create trigger/worker rows in this
	// same disposable DB, so isolate this lifecycle fixture rather than weakening
	// the production global claim semantics.
	for _, table := range []string{"outbox", "reminders", "market_watches"} {
		if _, err := store.pool.Exec(ctx, `DELETE FROM `+table); err != nil {
			t.Fatal(err)
		}
	}
	prefix := fmt.Sprintf("pg-worker-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"
	now := time.Now().UTC().Truncate(time.Second)

	event := events.Event{ID: prefix + "-event", Source: "/test", Type: "test.event", Subject: "subject", UserID: user, Data: json.RawMessage(`{"ok":true}`), Time: now}
	if err := store.Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, now.Add(time.Second), 10)
	if err != nil || len(claimed) != 1 || claimed[0].Event.ID != event.ID || claimed[0].Attempts != 0 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if err := store.Retry(ctx, claimed[0].RowID, "retry", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.Claim(ctx, now.Add(3*time.Second), 10)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("reclaimed=%+v err=%v", claimed, err)
	}
	if err := store.MarkSent(ctx, claimed[0].RowID); err != nil {
		t.Fatal(err)
	}

	if err := store.CreateReminderForDevice(ctx, user, prefix+"-reminder", device, "wake", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	due, err := store.ClaimDueReminders(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].Status != "pending" {
		t.Fatalf("due=%+v err=%v", due, err)
	}
	if err := store.ReleaseReminder(ctx, due[0].ID); err != nil {
		t.Fatal(err)
	}
	due, err = store.ClaimDueReminders(ctx, now, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("reclaimed reminder=%+v err=%v", due, err)
	}
	if err := store.MarkReminderSent(ctx, due[0].ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgeReminder(ctx, user, device, due[0].ID); err != nil {
		t.Fatal(err)
	}

	watch, err := store.CreateMarketWatch(ctx, user, device, prefix+"-watch", "coingecko", "bitcoin", "usd", ">=", 100.0)
	if err != nil {
		t.Fatal(err)
	}
	triggered, err := store.TriggerMarketWatch(ctx, watch, "BTC threshold", now)
	if err != nil || !triggered {
		t.Fatalf("triggered=%v err=%v", triggered, err)
	}
	triggered, err = store.TriggerMarketWatch(ctx, watch, "BTC threshold", now.Add(time.Second))
	if err != nil || triggered {
		t.Fatalf("duplicate trigger=%v err=%v", triggered, err)
	}
	watches, err := store.ListMarketWatches(ctx, user, device, 10)
	if err != nil || len(watches) != 1 || !watches[0].LastState {
		t.Fatalf("watches=%+v err=%v", watches, err)
	}
	if !market.Matches(watches[0], 101.0) {
		t.Fatal("market watch predicate drift")
	}
}

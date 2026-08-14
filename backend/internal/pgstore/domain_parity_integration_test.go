package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
)

func mutationRequest(t *testing.T, actor, operation, key string, payload any) idempotency.Request {
	t.Helper()
	hash, err := idempotency.HashValue(payload)
	if err != nil { t.Fatal(err) }
	return idempotency.Request{Actor: actor, Operation: operation, Key: key, RequestHash: hash}
}

func TestPostgresDomainRepositoriesParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-domain-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.CreateExpenses(ctx, user, prefix+"-expense", []domain.ExpenseInput{
		{AmountVND: 50_000, Category: "food", Description: "lunch", OccurredAt: now.Add(-time.Hour)},
		{AmountVND: 20_000, Category: "", Description: "bus", OccurredAt: now.Add(-30*time.Minute)},
	}); err != nil { t.Fatal(err) }
	// Legacy repository creates remain idempotent by user/key under concurrency-safe advisory locking.
	if err := store.CreateExpenses(ctx, user, prefix+"-expense", []domain.ExpenseInput{
		{AmountVND: 50_000, Category: "food", Description: "lunch", OccurredAt: now.Add(-time.Hour)},
		{AmountVND: 20_000, Category: "", Description: "bus", OccurredAt: now.Add(-30*time.Minute)},
	}); err != nil { t.Fatal(err) }
	expenses, err := store.ListExpenses(ctx, user, now.Add(-2*time.Hour), now.Add(time.Hour), "", 10)
	if err != nil { t.Fatal(err) }
	if len(expenses) != 2 || expenses[0].AmountVND+expenses[1].AmountVND != 70_000 {
		t.Fatalf("expenses=%+v", expenses)
	}
	total, err := store.ExpenseTotal(ctx, user, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || total != 70_000 { t.Fatalf("total=%d err=%v", total, err) }

	if err := store.SetBudget(ctx, user, "weekly", 1_000_000); err != nil { t.Fatal(err) }
	limit, found, err := store.BudgetLimit(ctx, user, "weekly")
	if err != nil || !found || limit != 1_000_000 { t.Fatalf("budget=%d found=%v err=%v", limit, found, err) }

	if err := store.CreateNote(ctx, user, prefix+"-note", "hello"); err != nil { t.Fatal(err) }
	notes, err := store.ListNotes(ctx, user, 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "hello" { t.Fatalf("notes=%+v err=%v", notes, err) }
	if err := store.UpdateNote(ctx, user, notes[0].ID, "updated"); err != nil { t.Fatal(err) }

	if err := store.CreateJournal(ctx, user, prefix+"-journal", "journal", now); err != nil { t.Fatal(err) }
	journal, err := store.ListJournal(ctx, user, now.Add(-time.Minute), now.Add(time.Minute), 10)
	if err != nil || len(journal) != 1 { t.Fatalf("journal=%+v err=%v", journal, err) }

	fireAt := now.Add(5 * time.Minute)
	if err := store.CreateTimerForDevice(ctx, user, prefix+"-timer", device, "tea", fireAt); err != nil { t.Fatal(err) }
	timers, err := store.ListTimers(ctx, user, device, "active", 10)
	if err != nil || len(timers) != 1 { t.Fatalf("timers=%+v err=%v", timers, err) }
	if err := store.PauseTimer(ctx, user, timers[0].ID, now); err != nil { t.Fatal(err) }
	paused, err := store.ListTimers(ctx, user, device, "paused", 10)
	if err != nil || len(paused) != 1 || paused[0].PausedRemainingSeconds < 299 { t.Fatalf("paused=%+v err=%v", paused, err) }
	if err := store.ResumeTimer(ctx, user, timers[0].ID, now.Add(time.Minute)); err != nil { t.Fatal(err) }

	if err := store.CreateVoiceMemo(ctx, user, prefix+"-memo", device, "/tmp/pg-domain.wav", "voice", 1234); err != nil { t.Fatal(err) }
	memo, found, err := store.VoiceMemoByKey(ctx, user, prefix+"-memo")
	if err != nil || !found || memo.DurationMS != 1234 { t.Fatalf("memo=%+v found=%v err=%v", memo, found, err) }
	if err := store.DeleteVoiceMemo(ctx, user, memo.ID); err != nil { t.Fatal(err) }
}

func TestPostgresDurableDomainMutationParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-durable-%d", time.Now().UnixNano())
	actor := prefix + "-actor"
	user := prefix + "-user"
	device := prefix + "-device"
	now := time.Now().UTC().Truncate(time.Second)

	notePayload := map[string]any{"content": "durable note"}
	noteReq := mutationRequest(t, actor, "note.create", prefix+"-note", notePayload)
	if err := store.CreateNoteMutation(ctx, noteReq, user, "durable note"); err != nil { t.Fatal(err) }
	if err := store.CreateNoteMutation(ctx, noteReq, user, "durable note"); err != nil { t.Fatalf("note replay: %v", err) }
	notes, err := store.ListNotes(ctx, user, 10)
	if err != nil || len(notes) != 1 { t.Fatalf("durable notes=%+v err=%v", notes, err) }
	conflict := mutationRequest(t, actor, "note.create", prefix+"-note", map[string]any{"content":"different"})
	if err := store.CreateNoteMutation(ctx, conflict, user, "different"); !idempotency.IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}

	reminderPayload := map[string]any{"title":"call mom","fire_at":now.Add(time.Hour)}
	reminderReq := mutationRequest(t, actor, "reminder.create", prefix+"-reminder", reminderPayload)
	first, err := store.CreateReminderMutation(ctx, reminderReq, user, device, "call mom", now.Add(time.Hour))
	if err != nil { t.Fatal(err) }
	second, err := store.CreateReminderMutation(ctx, reminderReq, user, device, "call mom", now.Add(time.Hour))
	if err != nil || second.ID != first.ID { t.Fatalf("reminder first=%+v second=%+v err=%v", first, second, err) }

	memoPayload := map[string]any{"path":"/tmp/durable.wav","duration_ms":250}
	memoReq := mutationRequest(t, actor, "voice_memo.save", prefix+"-memo", memoPayload)
	created, err := store.CreateVoiceMemoMutation(ctx, memoReq, user, device, "/tmp/durable.wav", "memo", 250)
	if err != nil { t.Fatal(err) }
	replayed, ok, err := store.ReplayVoiceMemoMutation(ctx, memoReq)
	if err != nil || !ok || replayed.ID != created.ID { t.Fatalf("voice replay created=%+v replay=%+v ok=%v err=%v", created, replayed, ok, err) }
	deleteReq := mutationRequest(t, actor, "voice_memo.delete", prefix+"-memo-delete", map[string]any{"id":created.ID})
	deleted, err := store.DeleteVoiceMemoMutation(ctx, deleteReq, user, created.ID)
	if err != nil || deleted.ID != created.ID { t.Fatalf("deleted=%+v err=%v", deleted, err) }
	deletedAgain, err := store.DeleteVoiceMemoMutation(ctx, deleteReq, user, created.ID)
	if err != nil || deletedAgain.ID != created.ID { t.Fatalf("delete replay=%+v err=%v", deletedAgain, err) }
}

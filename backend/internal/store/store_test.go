package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestExpenseIdempotency(t *testing.T) {
	data, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	for range 2 {
		if err := data.CreateExpense(ctx, "u1", "turn-1:0:expense.create", 50_000, "food", "đi chợ", now); err != nil {
			t.Fatal(err)
		}
	}
	total, err := data.ExpenseTotal(ctx, "u1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if total != 50_000 {
		t.Fatalf("duplicate turn changed total: got %d", total)
	}
}

func TestBatchExpensesAreAtomicAndIdempotent(t *testing.T) {
	data, err := Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	when := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	items := []ExpenseInput{
		{AmountVND: 30_000, Category: "food", Description: "lunch", OccurredAt: when},
		{AmountVND: 20_000, Category: "transport", Description: "taxi", OccurredAt: when},
	}
	for range 2 {
		if err := data.CreateExpenses(ctx, "u1", "turn-batch:expense.log", items); err != nil {
			t.Fatal(err)
		}
	}
	total, err := data.ExpenseTotal(ctx, "u1", when.Add(-time.Hour), when.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if total != 50_000 {
		t.Fatalf("total = %d; want 50000", total)
	}
	listed, err := data.ListExpenses(ctx, "u1", when.Add(-time.Hour), when.Add(time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("len = %d; want 2", len(listed))
	}
}

func TestReminderClaimReleaseAndFire(t *testing.T) {
	data, err := Open(filepath.Join(t.TempDir(), "reminder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := data.CreateReminderForDevice(ctx, "u1", "r1", "device-a", "timer done", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	claimed, err := data.ClaimDueReminders(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Status != "dispatching" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if recovered, err := data.RecoverDispatchingReminders(ctx); err != nil {
		t.Fatal(err)
	} else if recovered != 1 {
		t.Fatalf("recovered = %d; want 1", recovered)
	}
	claimed, err = data.ClaimDueReminders(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("reclaimed = %+v", claimed)
	}
	if err := data.AcknowledgeReminder(ctx, "other-user", "device-a", claimed[0].ID); err == nil {
		t.Fatal("cross-user alarm acknowledgement unexpectedly succeeded")
	}
	if err := data.AcknowledgeReminder(ctx, "u1", "other-device", claimed[0].ID); err == nil {
		t.Fatal("cross-device alarm acknowledgement unexpectedly succeeded")
	}
	if err := data.AcknowledgeReminder(ctx, "u1", "device-a", claimed[0].ID); err != nil {
		t.Fatal(err)
	}
	items, err := data.ListReminders(ctx, "u1", "device-a", "fired", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "fired" {
		t.Fatalf("fired = %+v", items)
	}
}

func TestTimerPauseResumePreservesRemainingDuration(t *testing.T) {
	data, err := Open(filepath.Join(t.TempDir(), "timer-pause.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
	if err := data.CreateTimerForDevice(ctx, "u1", "timer-pause", "device-a", "tea", now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	timers, err := data.ListTimers(ctx, "u1", "device-a", "active", 10)
	if err != nil || len(timers) != 1 {
		t.Fatalf("timers before pause = %+v err=%v", timers, err)
	}
	id := timers[0].ID
	if err := data.PauseTimer(ctx, "u1", id, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	paused, err := data.ListTimers(ctx, "u1", "device-a", "paused", 10)
	if err != nil || len(paused) != 1 || paused[0].PausedRemainingSeconds != 25*60 {
		t.Fatalf("paused = %+v err=%v", paused, err)
	}
	resumeAt := now.Add(20 * time.Minute)
	if err := data.ResumeTimer(ctx, "u1", id, resumeAt); err != nil {
		t.Fatal(err)
	}
	active, err := data.ListTimers(ctx, "u1", "device-a", "active", 10)
	if err != nil || len(active) != 1 {
		t.Fatalf("active after resume = %+v err=%v", active, err)
	}
	if got, want := active[0].FireAt, resumeAt.Add(25*time.Minute); !got.Equal(want) {
		t.Fatalf("resumed fire_at = %v; want %v", got, want)
	}
}

func TestVoiceMemoMetadataIdempotency(t *testing.T) {
	data, err := Open(filepath.Join(t.TempDir(), "memo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	for range 2 {
		if err := data.CreateVoiceMemo(ctx, "u1", "memo-key", "device-a", "/data/memo.wav", "hello", 1200); err != nil {
			t.Fatal(err)
		}
	}
	items, err := data.ListVoiceMemos(ctx, "u1", "device-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DurationMS != 1200 {
		t.Fatalf("items = %+v", items)
	}
}

func TestMigrationNormalizesLegacyTimestampsToUTC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-time.db")
	data, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.db.Exec(`INSERT INTO expenses(idempotency_key, amount_vnd, category, description, occurred_at, created_at)
		VALUES('legacy', 1000, 'other', 'legacy', '2026-08-10T17:00:00+07:00', '2026-08-10T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	var stored string
	if err := data.db.QueryRow(`SELECT occurred_at FROM expenses WHERE idempotency_key='legacy'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "2026-08-10T10:00:00Z" {
		t.Fatalf("stored time = %q", stored)
	}
}

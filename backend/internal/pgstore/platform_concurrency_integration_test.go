package pgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"companion-server/internal/events"
)

func TestPostgresOutboxClaimIsExclusiveAndRecoverable(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `DELETE FROM outbox`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	prefix := fmt.Sprintf("pg-outbox-concurrency-%d", time.Now().UnixNano())
	if err := store.Enqueue(ctx, events.Event{ID: prefix, Source: "/test", Type: "test.concurrent", UserID: prefix + "-user", Time: now}); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		items []events.Pending
		err   error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := store.Claim(ctx, now.Add(time.Second), 1)
			results <- claimResult{items: items, err: err}
		}()
	}
	wg.Wait()
	close(results)

	claimed := make([]events.Pending, 0, 1)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claimed = append(claimed, result.items...)
	}
	if len(claimed) != 1 || claimed[0].Event.ID != prefix || claimed[0].Attempts != 0 {
		t.Fatalf("exclusive claim=%+v", claimed)
	}

	if err := store.RecoverOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Claim(ctx, now.Add(2*time.Second), 1)
	if err != nil || len(recovered) != 1 || recovered[0].RowID != claimed[0].RowID || recovered[0].Attempts != 1 {
		t.Fatalf("recovered claim=%+v err=%v", recovered, err)
	}
	if err := store.MarkSent(ctx, recovered[0].RowID); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresReminderClaimIsExclusiveAndRecoverable(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `DELETE FROM reminders`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	prefix := fmt.Sprintf("pg-reminder-concurrency-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"
	if err := store.CreateReminderForDevice(ctx, user, prefix+"-key", device, "exclusive", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	type reminderResult struct {
		count int
		id    int64
		err   error
	}
	results := make(chan reminderResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := store.ClaimDueReminders(ctx, now, 1)
			result := reminderResult{count: len(items), err: err}
			if len(items) == 1 {
				result.id = items[0].ID
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)

	claims := 0
	var reminderID int64
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claims += result.count
		if result.id != 0 {
			reminderID = result.id
		}
	}
	if claims != 1 || reminderID == 0 {
		t.Fatalf("exclusive reminder claims=%d id=%d", claims, reminderID)
	}

	recoveredCount, err := store.RecoverDispatchingReminders(ctx)
	if err != nil || recoveredCount != 1 {
		t.Fatalf("recovery count=%d err=%v", recoveredCount, err)
	}
	recovered, err := store.ClaimDueReminders(ctx, now, 1)
	if err != nil || len(recovered) != 1 || recovered[0].ID != reminderID {
		t.Fatalf("recovered reminder=%+v err=%v", recovered, err)
	}
	if err := store.MarkReminderSent(ctx, reminderID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgeReminder(ctx, user, device, reminderID); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMarketTriggerIsAtomicUnderConcurrency(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `DELETE FROM reminders`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM market_watches`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	prefix := fmt.Sprintf("pg-market-concurrency-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"
	watch, err := store.CreateMarketWatch(ctx, user, device, prefix+"-key", "coingecko", "bitcoin", "usd", ">=", 100)
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			triggered, err := store.TriggerMarketWatch(ctx, watch, "BTC threshold", now)
			results <- triggered
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	triggeredCount := 0
	for triggered := range results {
		if triggered {
			triggeredCount++
		}
	}
	if triggeredCount != 1 {
		t.Fatalf("triggered count=%d", triggeredCount)
	}
	var reminders int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE user_id=$1 AND title='BTC threshold'`, user).Scan(&reminders); err != nil {
		t.Fatal(err)
	}
	if reminders != 1 {
		t.Fatalf("atomic trigger reminders=%d", reminders)
	}
}

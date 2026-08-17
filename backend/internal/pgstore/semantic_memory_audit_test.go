package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/memory"
)

func TestPostgresMemoryOwnerIsolationAudit(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-audit-iso-%d", time.Now().UnixNano())
	userA := prefix + "-userA"
	userB := prefix + "-userB"
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Notes isolation
	if err := store.CreateNote(ctx, userA, prefix+"-note-a", "User A confidential note"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNote(ctx, userB, prefix+"-note-b", "User B confidential note"); err != nil {
		t.Fatal(err)
	}
	notesA, err := store.ListNotes(ctx, userA, 10)
	if err != nil || len(notesA) != 1 || notesA[0].Content != "User A confidential note" {
		t.Fatalf("userA notes leak/failure: %+v err=%v", notesA, err)
	}
	notesB, err := store.ListNotes(ctx, userB, 10)
	if err != nil || len(notesB) != 1 || notesB[0].Content != "User B confidential note" {
		t.Fatalf("userB notes leak/failure: %+v err=%v", notesB, err)
	}

	// 2. Budget & Expenses isolation
	if err := store.SetBudget(ctx, userA, "monthly", 5_000_000); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBudget(ctx, userB, "monthly", 10_000_000); err != nil {
		t.Fatal(err)
	}
	limitA, foundA, err := store.BudgetLimit(ctx, userA, "monthly")
	if err != nil || !foundA || limitA != 5_000_000 {
		t.Fatalf("userA budget: limit=%d found=%v err=%v", limitA, foundA, err)
	}
	limitB, foundB, err := store.BudgetLimit(ctx, userB, "monthly")
	if err != nil || !foundB || limitB != 10_000_000 {
		t.Fatalf("userB budget: limit=%d found=%v err=%v", limitB, foundB, err)
	}

	// 3. Savings Goals isolation
	if err := store.SetSavingsGoal(ctx, userA, "monthly", 2_000_000, "Trip to Da Lat", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSavingsGoal(ctx, userB, "monthly", 4_000_000, "Buy laptop", now); err != nil {
		t.Fatal(err)
	}
	goalA, foundGA, err := store.GetSavingsGoal(ctx, userA, "monthly")
	if err != nil || !foundGA || goalA.TargetVND != 2_000_000 || goalA.Description != "Trip to Da Lat" {
		t.Fatalf("userA goal: %+v found=%v err=%v", goalA, foundGA, err)
	}
	goalB, foundGB, err := store.GetSavingsGoal(ctx, userB, "monthly")
	if err != nil || !foundGB || goalB.TargetVND != 4_000_000 || goalB.Description != "Buy laptop" {
		t.Fatalf("userB goal: %+v found=%v err=%v", goalB, foundGB, err)
	}

	// 4. Reminders isolation
	if err := store.CreateReminderForDevice(ctx, userA, prefix+"-rem-a", "dev-a", "User A medicine", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateReminderForDevice(ctx, userB, prefix+"-rem-b", "dev-b", "User B flight", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	remA, err := store.ListReminders(ctx, userA, "dev-a", "active", 10)
	if err != nil || len(remA) != 1 || remA[0].Title != "User A medicine" {
		t.Fatalf("userA reminder leak: %+v err=%v", remA, err)
	}
	remB, err := store.ListReminders(ctx, userB, "dev-b", "active", 10)
	if err != nil || len(remB) != 1 || remB[0].Title != "User B flight" {
		t.Fatalf("userB reminder leak: %+v err=%v", remB, err)
	}

	// 5. Semantic / Episodic Memories isolation
	memA, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    userA,
		Key:       "coffee_preference",
		Kind:      memory.Semantic,
		Value:     "cà phê sữa đá ít đường",
		ValidFrom: now,
		Source:    "user",
		Confidence: 1.0,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	memB, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    userB,
		Key:       "coffee_preference",
		Kind:      memory.Semantic,
		Value:     "Americano nóng không đường",
		ValidFrom: now,
		Source:    "user",
		Confidence: 1.0,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify vector isolation
	if err := store.UpsertVector(ctx, userA, memA.ID, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertVector(ctx, userB, memB.ID, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	vecHitsA, err := store.SearchVectors(ctx, userA, []float32{1, 0, 0}, 10)
	if err != nil || len(vecHitsA) != 1 || vecHitsA[0].ID != memA.ID {
		t.Fatalf("userA vector search leaked: %+v err=%v", vecHitsA, err)
	}
	vecHitsB, err := store.SearchVectors(ctx, userB, []float32{1, 0, 0}, 10)
	if err != nil || len(vecHitsB) != 1 || vecHitsB[0].ID != memB.ID {
		t.Fatalf("userB vector search leaked: %+v err=%v", vecHitsB, err)
	}

	// Verify CurrentMemories isolation
	curA, err := store.CurrentMemories(ctx, userA, now, 10)
	if err != nil || len(curA) != 1 || curA[0].Value != "cà phê sữa đá ít đường" {
		t.Fatalf("userA CurrentMemories: %+v err=%v", curA, err)
	}
	curB, err := store.CurrentMemories(ctx, userB, now, 10)
	if err != nil || len(curB) != 1 || curB[0].Value != "Americano nóng không đường" {
		t.Fatalf("userB CurrentMemories: %+v err=%v", curB, err)
	}

	// Deletion/forget on userA must not touch userB
	if err := store.ForgetMemory(ctx, userA, "coffee_preference"); err != nil {
		t.Fatal(err)
	}
	curAAfter, err := store.CurrentMemories(ctx, userA, now.Add(time.Second), 10)
	if err != nil || len(curAAfter) != 0 {
		t.Fatalf("userA memories after forget: %+v", curAAfter)
	}
	curBAfter, err := store.CurrentMemories(ctx, userB, now.Add(time.Second), 10)
	if err != nil || len(curBAfter) != 1 || curBAfter[0].Value != "Americano nóng không đường" {
		t.Fatalf("userB memories after userA forget: %+v", curBAfter)
	}
}

func TestPostgresMemoryTemporalResolutionAndSupersession(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-audit-temp-%d", time.Now().UnixNano())
	user := prefix + "-user"

	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	// Step 1: Fact 1 active from t0
	v1, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    user,
		Key:       "lunch_budget",
		Kind:      memory.Temporal,
		Value:     "50000 VND",
		ValidFrom: t0,
		Source:    "user",
		Confidence: 1,
		CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Query at T0: returns v1
	memAtT0, err := store.CurrentMemories(ctx, user, t0.Add(time.Hour), 10)
	if err != nil || len(memAtT0) != 1 || memAtT0[0].Value != "50000 VND" {
		t.Fatalf("at T0: got %+v err=%v", memAtT0, err)
	}

	// Step 2: Supersede fact at t1 (new budget 70000 VND)
	v2, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    user,
		Key:       "lunch_budget",
		Kind:      memory.Temporal,
		Value:     "70000 VND",
		ValidFrom: t1,
		Source:    "user",
		Confidence: 1,
		CreatedAt: t1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify v1 in DB now has valid_to = t1
	var v1ValidTo *time.Time
	if err := pool.QueryRow(ctx, `SELECT valid_to FROM memories WHERE id=$1`, v1.ID).Scan(&v1ValidTo); err != nil {
		t.Fatal(err)
	}
	if v1ValidTo == nil || !v1ValidTo.Equal(t1) {
		t.Fatalf("v1 valid_to not set to t1: got %v want %v", v1ValidTo, t1)
	}

	// Query at T0+2 days (before t1): returns v1
	memBeforeT1, err := store.CurrentMemories(ctx, user, t0.Add(48*time.Hour), 10)
	if err != nil || len(memBeforeT1) != 1 || memBeforeT1[0].Value != "50000 VND" {
		t.Fatalf("before T1: got %+v", memBeforeT1)
	}

	// Query at T1+1 hour: returns v2
	memAtT1, err := store.CurrentMemories(ctx, user, t1.Add(time.Hour), 10)
	if err != nil || len(memAtT1) != 1 || memAtT1[0].Value != "70000 VND" || memAtT1[0].ID != v2.ID {
		t.Fatalf("at T1: got %+v", memAtT1)
	}

	// Step 3: Supersede fact at t2 (new budget 90000 VND)
	v3, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    user,
		Key:       "lunch_budget",
		Kind:      memory.Temporal,
		Value:     "90000 VND",
		ValidFrom: t2,
		Source:    "user",
		Confidence: 1,
		CreatedAt: t2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Query at current time (after t2): returns only v3
	memCurrent, err := store.CurrentMemories(ctx, user, t2.Add(time.Hour), 10)
	if err != nil || len(memCurrent) != 1 || memCurrent[0].Value != "90000 VND" || memCurrent[0].ID != v3.ID {
		t.Fatalf("after T2: got %+v", memCurrent)
	}
}

func TestPostgresMemoryExplicitForgetDurability(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-audit-forget-%d", time.Now().UnixNano())
	user := prefix + "-user"
	actor := prefix + "-actor"
	now := time.Now().UTC().Truncate(time.Second)

	// Direct Forget
	mem, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    user,
		Key:       "sensitive_allergy",
		Kind:      memory.Semantic,
		Value:     "allergic to penicillin",
		ValidFrom: now,
		Source:    "user",
		Confidence: 1,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetMemory(ctx, user, "sensitive_allergy"); err != nil {
		t.Fatal(err)
	}
	current, err := store.CurrentMemories(ctx, user, now.Add(time.Second), 10)
	if err != nil || len(current) != 0 {
		t.Fatalf("memory still present after ForgetMemory: %+v", current)
	}

	// Verify deleted_at and valid_to are set in PostgreSQL
	var deletedAt, validTo *time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at, valid_to FROM memories WHERE id=$1`, mem.ID).Scan(&deletedAt, &validTo); err != nil {
		t.Fatal(err)
	}
	if deletedAt == nil || validTo == nil {
		t.Fatalf("deleted_at or valid_to is nil: deleted_at=%v valid_to=%v", deletedAt, validTo)
	}

	// Durable Mutation Forget with Idempotency
	mem2, err := store.UpsertMemory(ctx, memory.Item{
		UserID:    user,
		Key:       "temporary_pin",
		Kind:      memory.Semantic,
		Value:     "1234",
		ValidFrom: now,
		Source:    "user",
		Confidence: 1,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	forgetReq := mutationRequest(t, actor, "memory.forget", prefix+"-forget-idem", map[string]any{"key": "temporary_pin"})
	if err := store.ForgetMemoryMutation(ctx, forgetReq, user, "temporary_pin"); err != nil {
		t.Fatal(err)
	}
	// Replay must succeed idempotently
	if err := store.ForgetMemoryMutation(ctx, forgetReq, user, "temporary_pin"); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}

	current2, err := store.CurrentMemories(ctx, user, now.Add(time.Second), 10)
	if err != nil || len(current2) != 0 {
		t.Fatalf("memory still present after ForgetMemoryMutation: %+v", current2)
	}
	var deletedAt2 *time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM memories WHERE id=$1`, mem2.ID).Scan(&deletedAt2); err != nil {
		t.Fatal(err)
	}
	if deletedAt2 == nil {
		t.Fatalf("deleted_at is nil on mutated memory")
	}
}

func TestPostgresMemoryHybridRecallEndToEnd(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-audit-recall-%d", time.Now().UnixNano())
	user := prefix + "-user"
	now := time.Now().UTC().Truncate(time.Second)

	memSvc := memory.NewWithVector(store, store, memory.HashEmbedding{Dimensions: 64})

	// Add facts
	facts := []struct {
		key, val string
		kind     memory.Kind
	}{
		{"coffee_order", "cà phê sữa đá không đường", memory.Semantic},
		{"daily_lunch", "ngân sách bữa trưa 90000 VND", memory.Temporal},
		{"emergency_contact", "vợ: 0901234567", memory.Semantic},
		{"gym_schedule", "tập gym lúc 18h thứ 2 4 6", memory.Episodic},
	}

	for _, f := range facts {
		_, err := memSvc.Remember(ctx, user, f.key, f.kind, f.val, "user", 1.0, now)
		if err != nil {
			t.Fatal(err)
		}
	}

	// 1. Recall Vietnamese coffee preference
	coffeeHits, err := memSvc.Recall(ctx, user, "cà phê", 5)
	if err != nil || len(coffeeHits) == 0 {
		t.Fatalf("recall coffee: got %+v err=%v", coffeeHits, err)
	}
	if coffeeHits[0].Item.Key != "coffee_order" {
		t.Fatalf("top hit coffee want coffee_order, got %+v", coffeeHits[0])
	}

	// 2. Recall budget
	budgetHits, err := memSvc.Recall(ctx, user, "ngân sách trưa", 5)
	if err != nil || len(budgetHits) == 0 {
		t.Fatalf("recall budget: got %+v err=%v", budgetHits, err)
	}
	if budgetHits[0].Item.Key != "daily_lunch" {
		t.Fatalf("top hit budget want daily_lunch, got %+v", budgetHits[0])
	}

	// 3. Unrelated query returns nothing (relevance gating)
	unrelatedHits, err := memSvc.Recall(ctx, user, "lịch bay đi Tokyo Nhật Bản", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(unrelatedHits) != 0 {
		t.Fatalf("expected 0 hits for unrelated query, got %+v", unrelatedHits)
	}
}

func BenchmarkPostgresDeterministicMemoryRecall(b *testing.B) {
	pool := postgresTestPool(&testing.T{})
	store, err := New(pool)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-bench-mem-%d", time.Now().UnixNano())
	user := prefix + "-user"
	now := time.Now().UTC().Truncate(time.Second)

	memSvc := memory.NewWithVector(store, store, memory.HashEmbedding{Dimensions: 64})

	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("pref_%d", i)
		val := fmt.Sprintf("chi tiêu phân loại %d giá trị 100000 VND", i%10)
		_, _ = memSvc.Remember(ctx, user, key, memory.Semantic, val, "user", 1.0, now.Add(time.Duration(-i)*time.Minute))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memSvc.Recall(ctx, user, "chi tiêu phân loại 5", 5)
		if err != nil {
			b.Fatal(err)
		}
	}
}

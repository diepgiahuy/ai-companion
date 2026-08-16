package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
)

func TestPostgresSavingsGoalsAndOutboxIntegration(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := store.VerifySchema(ctx); err != nil {
		t.Fatalf("schema verification failed: %v", err)
	}

	prefix := fmt.Sprintf("pg-savings-%d", time.Now().UnixNano())
	userID := prefix + "-user"
	period := "monthly"

	// 1. Initial State: No goal -> GetSavingsGoal returns found = false
	_, found, err := store.GetSavingsGoal(ctx, userID, period)
	if err != nil {
		t.Fatalf("unexpected error getting initial goal: %v", err)
	}
	if found {
		t.Fatalf("expected no savings goal initially")
	}

	// 2. Set Savings Goal with Durable Mutation
	payload1 := map[string]any{"period": period, "target_vnd": int64(5000000), "description": "Initial Savings Target"}
	hash1, err := idempotency.HashValue(payload1)
	if err != nil {
		t.Fatal(err)
	}
	setReq := idempotency.Request{
		Actor:       userID,
		Operation:   "saving.goal_set",
		Key:         prefix + "-mut-1",
		RequestHash: hash1,
	}
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	if err := store.SetSavingsGoalMutation(ctx, setReq, userID, period, 5000000, "Initial Savings Target", t0); err != nil {
		t.Fatalf("SetSavingsGoalMutation failed: %v", err)
	}

	goal1, found, err := store.GetSavingsGoal(ctx, userID, period)
	if err != nil || !found {
		t.Fatalf("failed to retrieve set goal: %v, found: %v", err, found)
	}
	if goal1.TargetVND != 5000000 || goal1.Description != "Initial Savings Target" {
		t.Fatalf("goal mismatch: %+v", goal1)
	}
	initialCreatedAt := goal1.CreatedAt

	// Verify Outbox received "savings_goal.updated" event
	var outboxCount int
	err = store.pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE user_id=$1 AND event_type='savings_goal.updated'", userID).Scan(&outboxCount)
	if err != nil || outboxCount < 1 {
		t.Fatalf("expected outbox event savings_goal.updated, count: %d, err: %v", outboxCount, err)
	}

	// 3. Mid-Month Target Replacement: Replace 5M target with 8M target
	// Must update effective_from and preserve created_at
	t1 := time.Now().UTC()
	payload2 := map[string]any{"period": period, "target_vnd": int64(8000000), "description": "Upgraded Target"}
	hash2, err := idempotency.HashValue(payload2)
	if err != nil {
		t.Fatal(err)
	}
	setReq2 := idempotency.Request{
		Actor:       userID,
		Operation:   "saving.goal_set",
		Key:         prefix + "-mut-2",
		RequestHash: hash2,
	}
	if err := store.SetSavingsGoalMutation(ctx, setReq2, userID, period, 8000000, "Upgraded Target", t1); err != nil {
		t.Fatalf("replace savings goal failed: %v", err)
	}

	goal2, found, err := store.GetSavingsGoal(ctx, userID, period)
	if err != nil || !found {
		t.Fatalf("failed to retrieve replaced goal: %v", err)
	}
	if goal2.TargetVND != 8000000 || goal2.Description != "Upgraded Target" {
		t.Fatalf("replaced goal mismatch: %+v", goal2)
	}
	if !goal2.CreatedAt.Equal(initialCreatedAt) {
		t.Fatalf("created_at was mutated on replacement: got %v, want %v", goal2.CreatedAt, initialCreatedAt)
	}
	if goal2.EffectiveFrom.Before(t0) {
		t.Fatalf("effective_from was not updated on replacement: %v", goal2.EffectiveFrom)
	}

	// 4. L3 Product Flow: Budget (20M) + Expense (10M) + Goal (8M)
	// Remaining: 10M >= 8M -> budget_headroom_covers_target
	if err := store.SetBudget(ctx, userID, period, 20000000); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := startOfMonth.AddDate(0, 1, 0)
	if err := store.CreateExpense(ctx, userID, prefix+"-exp-1", 10000000, "shopping", "work station", now); err != nil {
		t.Fatal(err)
	}

	spent, err := store.ExpenseTotal(ctx, userID, startOfMonth, endOfMonth)
	if err != nil {
		t.Fatal(err)
	}
	budgetLimit, budgetFound, err := store.BudgetLimit(ctx, userID, period)
	if err != nil || !budgetFound {
		t.Fatalf("budget lookup failed: %v", err)
	}

	bPtr := &budgetLimit
	p := domain.CalculateSavingsProgress(&goal2, period, startOfMonth, endOfMonth, spent, bPtr)
	if p.Status != domain.StatusBudgetHeadroomCoversTarget || *p.BudgetRemainingVND != 10000000 || *p.HeadroomVsTargetVND != 2000000 {
		t.Fatalf("progress mismatch: %+v", p)
	}

	// 5. Durability across store restart / new store instance on same DB
	store2, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}

	goalPersisted, found, err := store2.GetSavingsGoal(ctx, userID, period)
	if err != nil || !found || goalPersisted.TargetVND != 8000000 {
		t.Fatalf("persisted goal after restart mismatch: %+v, found: %v", goalPersisted, found)
	}
	spent2, _ := store2.ExpenseTotal(ctx, userID, startOfMonth, endOfMonth)
	bLimit2, _, _ := store2.BudgetLimit(ctx, userID, period)
	p2 := domain.CalculateSavingsProgress(&goalPersisted, period, startOfMonth, endOfMonth, spent2, &bLimit2)
	if p2.Status != domain.StatusBudgetHeadroomCoversTarget || *p2.BudgetRemainingVND != 10000000 {
		t.Fatalf("reopened progress mismatch: %+v", p2)
	}

	// 6. Delete Savings Goal with Durable Mutation and Verify Outbox Delete Trigger
	payload3 := map[string]any{"period": period}
	hash3, err := idempotency.HashValue(payload3)
	if err != nil {
		t.Fatal(err)
	}
	delReq := idempotency.Request{
		Actor:       userID,
		Operation:   "saving.goal_delete",
		Key:         prefix + "-del-1",
		RequestHash: hash3,
	}
	if err := store2.DeleteSavingsGoalMutation(ctx, delReq, userID, period); err != nil {
		t.Fatalf("DeleteSavingsGoalMutation failed: %v", err)
	}

	_, foundAfterDel, err := store2.GetSavingsGoal(ctx, userID, period)
	if err != nil || foundAfterDel {
		t.Fatalf("expected goal deleted, found: %v, err: %v", foundAfterDel, err)
	}

	// Verify "savings_goal.deleted" event was written to outbox by trg_savings_goals_ad
	var delEventCount int
	err = store2.pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE user_id=$1 AND event_type='savings_goal.deleted'", userID).Scan(&delEventCount)
	if err != nil || delEventCount < 1 {
		t.Fatalf("expected outbox event savings_goal.deleted from trg_savings_goals_ad, count: %d, err: %v", delEventCount, err)
	}
}

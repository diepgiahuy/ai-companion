package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/contextengine"
	"companion-server/internal/pipeline"
	resourceprovider "companion-server/internal/providers/resources"
	"companion-server/internal/store"
)

func TestSavingsScenariosWithRouterAndTools(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "savings_scenarios.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	location, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	resources := capability.NewResourceRegistry()
	if err := resources.Register(resourceprovider.NewNative(data, nil, location)); err != nil {
		t.Fatal(err)
	}

	registry := capability.NewToolRegistry()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, location)
	if err := RegisterNative(registry, NativeDependencies{
		Store:     data,
		Resources: resources,
		Now:       func() time.Time { return now },
	}); err != nil {
		t.Fatal(err)
	}

	router := contextengine.New(resources)
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{
		UserID:   "owner-scenario-1",
		DeviceID: "companion-s3",
		Timezone: "Asia/Ho_Chi_Minh",
	})

	// Scenario 1: Vietnamese "đặt mục tiêu tiết kiệm 5 triệu tháng này"
	// -> Router selects "saving" pack
	// -> Invokes "saving.goal_set"
	// -> Exact 1 durable mutation executed
	planVN := router.Plan(ctx, "đặt mục tiêu tiết kiệm 5 triệu tháng này")
	hasSavingPack := false
	for _, p := range planVN.Packs {
		if p == "saving" {
			hasSavingPack = true
		}
	}
	if !hasSavingPack {
		t.Fatalf("expected saving pack in plan for VN query, got: %v", planVN.Packs)
	}

	resSetVN := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{
		Key:       "turn-mut-1",
		Arguments: `{"period":"monthly","target_vnd":5000000,"description":"Mục tiêu tháng 8"}`,
	})
	if !strings.Contains(resSetVN.Content, `"ok":true`) || !strings.Contains(resSetVN.Content, `"saved":"savings_goal"`) {
		t.Fatalf("VN saving.goal_set failed: %s", resSetVN.Content)
	}

	// Verify goal persisted in store
	goal, ok, err := data.GetSavingsGoal(ctx, "owner-scenario-1", "monthly")
	if err != nil || !ok || goal.TargetVND != 5000000 {
		t.Fatalf("persisted goal = %+v, ok = %v, err = %v", goal, ok, err)
	}

	// Scenario 2: Accentless VN "dat muc tieu tiet kiem 3tr thang nay"
	planAccentless := router.Plan(ctx, "dat muc tieu tiet kiem 3tr thang nay")
	hasSavingPack = false
	for _, p := range planAccentless.Packs {
		if p == "saving" {
			hasSavingPack = true
		}
	}
	if !hasSavingPack {
		t.Fatalf("expected saving pack for accentless VN, got: %v", planAccentless.Packs)
	}

	// Scenario 3: English "set savings goal 10 million this month"
	planEN := router.Plan(ctx, "set savings goal 10 million this month")
	hasSavingPack = false
	for _, p := range planEN.Packs {
		if p == "saving" {
			hasSavingPack = true
		}
	}
	if !hasSavingPack {
		t.Fatalf("expected saving pack for English query, got: %v", planEN.Packs)
	}

	// Scenario 4: Mixed-language "tiết kiệm goal 2000000 VND monthly"
	planMixed := router.Plan(ctx, "tiết kiệm goal 2000000 VND monthly")
	hasSavingPack = false
	for _, p := range planMixed.Packs {
		if p == "saving" {
			hasSavingPack = true
		}
	}
	if !hasSavingPack {
		t.Fatalf("expected saving pack for mixed query, got: %v", planMixed.Packs)
	}

	// Scenario 5: Query progress before setting budget: "tháng này tao đã tiết kiệm được bao nhiêu?"
	// -> Router selects saving & budget packs
	// -> Invokes "saving.progress" (read-only, zero mutations)
	// -> Result does NOT invent money saved; returns truthful "insufficient_data" and "spend_only"
	planProgress := router.Plan(ctx, "tháng này tao đã tiết kiệm được bao nhiêu?")
	hasSavingPack = false
	for _, p := range planProgress.Packs {
		if p == "saving" {
			hasSavingPack = true
		}
	}
	if !hasSavingPack {
		t.Fatalf("expected saving pack for progress inquiry, got: %v", planProgress.Packs)
	}

	// Query progress with zero expenses, no budget
	progRes1 := registry.Execute(ctx, "saving.progress", capability.ToolRequest{
		Key:       "turn-read-1",
		Arguments: `{"period":"monthly"}`,
	})
	if !strings.Contains(progRes1.Content, `"status":"insufficient_data"`) ||
		!strings.Contains(progRes1.Content, `"basis":"spend_only"`) {
		t.Fatalf("expected truthful insufficient_data spend_only before budget, got: %s", progRes1.Content)
	}

	// Scenario 6: Set monthly budget and record expenses, then check progress
	if err := data.SetBudget(ctx, "owner-scenario-1", "monthly", 15000000); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateExpense(ctx, "owner-scenario-1", "exp-sc-1", 7000000, "shopping", "tablet", now); err != nil {
		t.Fatal(err)
	}

	// Now remaining budget is 8,000,000 >= 5,000,000 target -> budget_headroom_covers_target
	progRes2 := registry.Execute(ctx, "saving.progress", capability.ToolRequest{
		Key:       "turn-read-2",
		Arguments: `{"period":"monthly"}`,
	})
	if !strings.Contains(progRes2.Content, `"status":"budget_headroom_covers_target"`) ||
		!strings.Contains(progRes2.Content, `"budget_remaining_vnd":8000000`) ||
		!strings.Contains(progRes2.Content, `"headroom_vs_target_vnd":3000000`) {
		t.Fatalf("expected budget_headroom_covers_target with headroom, got: %s", progRes2.Content)
	}
}

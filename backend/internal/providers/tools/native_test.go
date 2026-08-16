package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	resourceprovider "companion-server/internal/providers/resources"
	"companion-server/internal/store"
)

func TestNativeRegistrySeparatesTimerFromReminderAndReadsResources(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "tools.db"))
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
	now := time.Now()
	if err := RegisterNative(registry, NativeDependencies{Store: data, Resources: resources, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", DeviceID: "device-a"})

	timer := registry.Execute(ctx, "timer.create", capability.ToolRequest{Key: "timer-1", Arguments: `{"title":"tea","delay_seconds":60}`})
	if !strings.Contains(timer.Content, `"ok":true`) {
		t.Fatalf("timer = %s", timer.Content)
	}
	reminder := registry.Execute(ctx, "reminder.create", capability.ToolRequest{Key: "reminder-1", Arguments: `{"title":"meeting","fire_at":"2030-01-01T10:00:00+07:00"}`})
	if !strings.Contains(reminder.Content, `"ok":true`) {
		t.Fatalf("reminder = %s", reminder.Content)
	}

	timers, err := data.ListTimers(ctx, "user-a", "device-a", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(timers) != 1 || timers[0].Kind != "timer" {
		t.Fatalf("timers = %+v", timers)
	}

	resource := registry.Execute(ctx, "resource.read", capability.ToolRequest{Key: "read-1", Arguments: `{"uri":"timers://active"}`})
	var resourceEnvelope struct {
		OK       bool `json:"ok"`
		Resource struct {
			Text string `json:"text"`
		} `json:"resource"`
	}
	if err := json.Unmarshal([]byte(resource.Content), &resourceEnvelope); err != nil {
		t.Fatalf("decode resource result: %v (%s)", err, resource.Content)
	}
	if !resourceEnvelope.OK || !strings.Contains(resourceEnvelope.Resource.Text, `"kind":"timer"`) {
		t.Fatalf("resource = %s", resource.Content)
	}

	names := map[string]bool{}
	for _, definition := range registry.Definitions() {
		names[definition.Name] = true
	}
	for _, name := range []string{"expense.query", "timer.create", "timer.list", "expense.update", "note.delete"} {
		if !names[name] {
			t.Fatalf("tool %s is not discoverable", name)
		}
	}
	if names["expense.create"] || names["resource.read"] || names["resource.list"] {
		t.Fatal("legacy/resource tools should remain callable but hidden from model discovery")
	}
}

func TestNotesAndVoiceMemosQueryFiltering(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "query_tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	registry := capability.NewToolRegistry()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if err := RegisterNative(registry, NativeDependencies{Store: data, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-query", DeviceID: "device-query"})

	// Create notes
	_ = registry.Execute(ctx, "note.create", capability.ToolRequest{Key: "n1", Arguments: `{"content":"buy coffee and oats"}`})
	_ = registry.Execute(ctx, "note.create", capability.ToolRequest{Key: "n2", Arguments: `{"content":"review Q3 roadmap"}`})
	_ = registry.Execute(ctx, "note.create", capability.ToolRequest{Key: "n3", Arguments: `{"content":"call mechanic about bike"}`})

	// Search notes by keyword
	searchResult := registry.Execute(ctx, "note.list", capability.ToolRequest{Key: "l1", Arguments: `{"search":"coffee"}`})
	if !strings.Contains(searchResult.Content, "buy coffee and oats") || strings.Contains(searchResult.Content, "review Q3 roadmap") {
		t.Fatalf("note search result = %s", searchResult.Content)
	}

	// Create voice memos
	if err := data.CreateVoiceMemo(ctx, "user-query", "vm1", "device-query", "/tmp/vm1.wav", "meeting notes about budget", 15000); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateVoiceMemo(ctx, "user-query", "vm2", "device-query", "/tmp/vm2.wav", "personal reflection on vacation", 30000); err != nil {
		t.Fatal(err)
	}

	// Search voice memos by transcript
	vmSearch := registry.Execute(ctx, "voice_memo.list", capability.ToolRequest{Key: "vl1", Arguments: `{"search":"budget"}`})
	if !strings.Contains(vmSearch.Content, "meeting notes about budget") || strings.Contains(vmSearch.Content, "personal reflection on vacation") {
		t.Fatalf("voice memo search result = %s", vmSearch.Content)
	}

	// User isolation: other user should see nothing
	ctxOther := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-other", DeviceID: "device-other"})
	otherNotes := registry.Execute(ctxOther, "note.list", capability.ToolRequest{Key: "l2", Arguments: `{"search":"coffee"}`})
	if strings.Contains(otherNotes.Content, "coffee") {
		t.Fatalf("cross-user note leak: %s", otherNotes.Content)
	}

	// Single-bound query (from only)
	fromOnly := registry.Execute(ctx, "note.list", capability.ToolRequest{Key: "l3", Arguments: `{"from":"2026-08-01T00:00:00Z"}`})
	if !strings.Contains(fromOnly.Content, `"ok":true`) || !strings.Contains(fromOnly.Content, "buy coffee and oats") {
		t.Fatalf("from-only query failed: %s", fromOnly.Content)
	}

	// Single-bound query (to only)
	toOnly := registry.Execute(ctx, "note.list", capability.ToolRequest{Key: "l4", Arguments: `{"to":"2030-01-01T00:00:00Z"}`})
	if !strings.Contains(toOnly.Content, `"ok":true`) || !strings.Contains(toOnly.Content, "buy coffee and oats") {
		t.Fatalf("to-only query failed: %s", toOnly.Content)
	}
}

func TestNativeSavingTools(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "saving_tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	registry := capability.NewToolRegistry()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if err := RegisterNative(registry, NativeDependencies{Store: data, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-savings-tool"})

	// 1. Boundary validations for target_vnd
	// Target = 0 -> fail
	res0 := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-0", Arguments: `{"period":"monthly","target_vnd":0}`})
	if strings.Contains(res0.Content, `"ok":true`) {
		t.Fatalf("expected 0 VND to fail, got: %s", res0.Content)
	}

	// Target = -5000 -> fail
	resNeg := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-neg", Arguments: `{"period":"monthly","target_vnd":-5000}`})
	if strings.Contains(resNeg.Content, `"ok":true`) {
		t.Fatalf("expected negative VND to fail, got: %s", resNeg.Content)
	}

	// Target = 1_000_000_000_001 -> fail (> MaxSavingsTargetVND)
	resOverflow := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-over", Arguments: `{"period":"monthly","target_vnd":1000000000001}`})
	if strings.Contains(resOverflow.Content, `"ok":true`) {
		t.Fatalf("expected > 1T VND to fail, got: %s", resOverflow.Content)
	}

	// Valid target = 1 VND (lower bound)
	res1 := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-1", Arguments: `{"period":"monthly","target_vnd":1}`})
	if !strings.Contains(res1.Content, `"ok":true`) {
		t.Fatalf("expected 1 VND to succeed, got: %s", res1.Content)
	}

	// Valid target = 1_000_000_000_000 VND (upper bound)
	resMax := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-max", Arguments: `{"period":"monthly","target_vnd":1000000000000}`})
	if !strings.Contains(resMax.Content, `"ok":true`) {
		t.Fatalf("expected 1T VND to succeed, got: %s", resMax.Content)
	}

	// 2. Normal target: 5,000,000 VND
	resSet := registry.Execute(ctx, "saving.goal_set", capability.ToolRequest{Key: "sg-set-1", Arguments: `{"period":"monthly","target_vnd":5000000,"description":"Emergency fund"}`})
	if !strings.Contains(resSet.Content, `"ok":true`) || !strings.Contains(resSet.Content, `"saved":"savings_goal"`) {
		t.Fatalf("saving.goal_set failed: %s", resSet.Content)
	}

	// 3. Get goal
	resGet := registry.Execute(ctx, "saving.goal_get", capability.ToolRequest{Key: "sg-get-1", Arguments: `{"period":"monthly"}`})
	if !strings.Contains(resGet.Content, `"set":true`) || !strings.Contains(resGet.Content, `"target_vnd":5000000`) || !strings.Contains(resGet.Content, "Emergency fund") {
		t.Fatalf("saving.goal_get failed: %s", resGet.Content)
	}

	// 4. Progress without budget -> insufficient_data
	resProg1 := registry.Execute(ctx, "saving.progress", capability.ToolRequest{Key: "sg-p-1", Arguments: `{"period":"monthly"}`})
	if !strings.Contains(resProg1.Content, `"status":"insufficient_data"`) || !strings.Contains(resProg1.Content, `"basis":"spend_only"`) {
		t.Fatalf("expected insufficient_data without budget, got: %s", resProg1.Content)
	}

	// 5. Set budget (20,000,000) and expense (8,000,000) -> remaining 12,000,000 >= 5,000,000 -> budget_headroom_covers_target
	if err := data.SetBudget(ctx, "user-savings-tool", "monthly", 20000000); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateExpense(ctx, "user-savings-tool", "exp-1", 8000000, "shopping", "laptop", now); err != nil {
		t.Fatal(err)
	}

	resProg2 := registry.Execute(ctx, "saving.progress", capability.ToolRequest{Key: "sg-p-2", Arguments: `{"period":"monthly"}`})
	if !strings.Contains(resProg2.Content, `"status":"budget_headroom_covers_target"`) ||
		!strings.Contains(resProg2.Content, `"budget_remaining_vnd":12000000`) ||
		!strings.Contains(resProg2.Content, `"headroom_vs_target_vnd":7000000`) {
		t.Fatalf("expected budget_headroom_covers_target, got: %s", resProg2.Content)
	}

	// 6. Delete goal
	resDel := registry.Execute(ctx, "saving.goal_delete", capability.ToolRequest{Key: "sg-del-1", Arguments: `{"period":"monthly"}`})
	if !strings.Contains(resDel.Content, `"ok":true`) || !strings.Contains(resDel.Content, `"deleted":"savings_goal"`) {
		t.Fatalf("saving.goal_delete failed: %s", resDel.Content)
	}

	// 7. Get goal after delete -> set: false
	resGet2 := registry.Execute(ctx, "saving.goal_get", capability.ToolRequest{Key: "sg-get-2", Arguments: `{"period":"monthly"}`})
	if !strings.Contains(resGet2.Content, `"set":false`) {
		t.Fatalf("expected goal not set after delete, got: %s", resGet2.Content)
	}
}



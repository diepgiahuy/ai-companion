package resources

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	"companion-server/internal/store"
)

func TestNativeResourcesExposeAuthoritativeDomainState(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "resources.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	location, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	now := time.Now().In(location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, location)
	if err := data.CreateExpense(context.Background(), "device-a", "expense-resource", 125000, "food", "lunch", today); err != nil {
		t.Fatal(err)
	}
	if err := data.SetBudget(context.Background(), "device-a", "weekly", 1000000); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateTimerForDevice(context.Background(), "device-a", "timer-resource", "device-a", "tea", time.Now().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateReminderForDevice(context.Background(), "device-a", "reminder-resource", "device-a", "meeting", today.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	registry := capability.NewResourceRegistry()
	if err := registry.Register(NewNative(data, nil, location)); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a"})

	expense, err := registry.Read(ctx, "expenses://today")
	if err != nil {
		t.Fatal(err)
	}
	var expensePayload struct {
		Total int64 `json:"total_vnd"`
	}
	if err := json.Unmarshal([]byte(expense.Text), &expensePayload); err != nil {
		t.Fatal(err)
	}
	if expensePayload.Total != 125000 {
		t.Fatalf("expense resource = %s", expense.Text)
	}

	timers, err := registry.Read(ctx, "timers://active")
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSON(timers.Text, `"kind":"timer"`) {
		t.Fatalf("timers = %s", timers.Text)
	}

	reminders, err := registry.Read(ctx, "reminders://today")
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSON(reminders.Text, `"kind":"reminder"`) {
		t.Fatalf("reminders = %s", reminders.Text)
	}
}

func containsJSON(text, fragment string) bool {
	for i := 0; i+len(fragment) <= len(text); i++ {
		if text[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

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
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a"})

	timer := registry.Execute(ctx, "timer.create", capability.ToolRequest{Key: "timer-1", Arguments: `{"title":"tea","delay_seconds":60}`})
	if !strings.Contains(timer.Content, `"ok":true`) {
		t.Fatalf("timer = %s", timer.Content)
	}
	reminder := registry.Execute(ctx, "reminder.create", capability.ToolRequest{Key: "reminder-1", Arguments: `{"title":"meeting","fire_at":"2030-01-01T10:00:00+07:00"}`})
	if !strings.Contains(reminder.Content, `"ok":true`) {
		t.Fatalf("reminder = %s", reminder.Content)
	}

	timers, err := data.ListTimers(ctx, "device-a", "device-a", "pending", 10)
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

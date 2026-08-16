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
}


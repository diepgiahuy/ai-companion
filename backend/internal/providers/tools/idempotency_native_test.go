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
	"companion-server/internal/store"
)

func TestTimerCreateDurableIdempotencyReplaysOriginalOutcomeAndConflicts(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	now := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})

	first := registry.Execute(ctx, "timer.create", capability.ToolRequest{Key: "timer-replay-key", Arguments: `{"title":"tea","delay_seconds":60}`})
	firstFire := toolResultFireAt(t, first)
	if want := now.Add(time.Minute).UTC().Format(time.RFC3339); firstFire != want {
		t.Fatalf("first fire_at = %q; want %q", firstFire, want)
	}

	// A retry can arrive much later. The semantic request is still the same and
	// must replay the original committed outcome rather than deriving a new time.
	now = now.Add(10 * time.Minute)
	replayed := registry.Execute(ctx, "timer.create", capability.ToolRequest{Key: "timer-replay-key", Arguments: `{"title":"tea","delay_seconds":60}`})
	if replayFire := toolResultFireAt(t, replayed); replayFire != firstFire {
		t.Fatalf("replayed fire_at = %q; want original %q", replayFire, firstFire)
	}

	conflict := registry.Execute(ctx, "timer.create", capability.ToolRequest{Key: "timer-replay-key", Arguments: `{"title":"tea","delay_seconds":120}`})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("conflicting retry = %s", conflict.Content)
	}

	items, err := data.ListTimers(ctx, "user-a", "device-a", "all", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("timer count = %d; want exactly one committed mutation (%+v)", len(items), items)
	}
	if got := items[0].FireAt.UTC().Format(time.RFC3339); got != firstFire {
		t.Fatalf("stored fire_at = %q; want %q", got, firstFire)
	}
}

func TestDurableMutationRequiresAuthenticatedUserActor(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "actor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data}); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-only"})
	result := registry.Execute(ctx, "note.create", capability.ToolRequest{Key: "note-key", Arguments: `{"content":"hello"}`})
	if !strings.Contains(result.Content, "authenticated user is required") {
		t.Fatalf("device-only mutation unexpectedly accepted: %s", result.Content)
	}
}

func toolResultFireAt(t *testing.T, result capability.ToolResult) string {
	t.Helper()
	var envelope struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		FireAt string `json:"fire_at"`
	}
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, result.Content)
	}
	if !envelope.OK {
		t.Fatalf("tool failed: %s", envelope.Error)
	}
	return envelope.FireAt
}

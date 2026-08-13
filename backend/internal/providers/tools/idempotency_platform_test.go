package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/pipeline"
	"companion-server/internal/store"
)

type idempotencyMarketProvider struct{}

func (idempotencyMarketProvider) Name() string { return "fake-market" }
func (idempotencyMarketProvider) Quote(context.Context, string, string) (market.Quote, error) {
	return market.Quote{Symbol: "XAU/USD", Price: 3000, Currency: "USD", Source: "fake-market", AsOf: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func TestMemoryRememberAutoTimeReplaysOriginalOutcomeAndConflicts(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil { t.Fatal(err) }
	defer data.Close()
	now := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)
	mem := memory.New(data, memory.HashEmbedding{Dimensions: 16})
	registry := capability.NewToolRegistry()
	if err := RegisterPlatform(registry, PlatformDependencies{Memory: mem, Now: func() time.Time { return now }}); err != nil { t.Fatal(err) }
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})
	args := `{"key":"favorite_food","kind":"semantic","value":"pho"}`
	args = strings.ReplaceAll(args, `\"`, `"`)
	firstItem := resultMemory(t, registry.Execute(ctx, "memory.remember", capability.ToolRequest{Key: "memory-request", Arguments: args}))
	if !firstItem.ValidFrom.Equal(now) { t.Fatalf("first valid_from = %s; want %s", firstItem.ValidFrom, now) }
	now = now.Add(24 * time.Hour)
	replayedItem := resultMemory(t, registry.Execute(ctx, "memory.remember", capability.ToolRequest{Key: "memory-request", Arguments: args}))
	if replayedItem.ID != firstItem.ID || !replayedItem.ValidFrom.Equal(firstItem.ValidFrom) { t.Fatalf("replayed memory = %+v; want original %+v", replayedItem, firstItem) }
	conflictArgs := strings.ReplaceAll(`{"key":"favorite_food","kind":"semantic","value":"rice"}`, `\"`, `"`)
	conflict := registry.Execute(ctx, "memory.remember", capability.ToolRequest{Key: "memory-request", Arguments: conflictArgs})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") { t.Fatalf("memory conflict = %s", conflict.Content) }
	current, err := data.CurrentMemories(ctx, "user-a", now.Add(time.Hour), 20)
	if err != nil { t.Fatal(err) }
	if len(current) != 1 || current[0].ID != firstItem.ID || current[0].Value != "pho" { t.Fatalf("current memories = %+v", current) }
	forgetArgs := strings.ReplaceAll(`{"key":"favorite_food"}`, `\"`, `"`)
	forgot := registry.Execute(ctx, "memory.forget", capability.ToolRequest{Key: "forget-request", Arguments: forgetArgs})
	if !strings.Contains(forgot.Content, `"ok":true`) { t.Fatalf("forget = %s", forgot.Content) }
	replayedForget := registry.Execute(ctx, "memory.forget", capability.ToolRequest{Key: "forget-request", Arguments: forgetArgs})
	if !strings.Contains(replayedForget.Content, `"ok":true`) { t.Fatalf("replayed forget = %s", replayedForget.Content) }
}

func TestMarketWatchCreateDeleteDurableReplayAndConflict(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "market.db"))
	if err != nil { t.Fatal(err) }
	defer data.Close()
	quotes := market.New(time.Minute, idempotencyMarketProvider{})
	registry := capability.NewToolRegistry()
	if err := RegisterPlatform(registry, PlatformDependencies{Market: quotes, MarketWatches: data}); err != nil { t.Fatal(err) }
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})
	firstArgs := strings.ReplaceAll(`{"provider":"fake-market","symbol":"XAU/USD","currency":"usd","operator":">=","threshold":3500}`, `\"`, `"`)
	firstWatch := resultWatch(t, registry.Execute(ctx, "market.watch.create", capability.ToolRequest{Key: "watch-request", Arguments: firstArgs}))
	replayArgs := strings.ReplaceAll(`{"provider":"fake-market","symbol":"XAU/USD","currency":"USD","operator":">=","threshold":3500}`, `\"`, `"`)
	replayed := registry.Execute(ctx, "market.watch.create", capability.ToolRequest{Key: "watch-request", Arguments: replayArgs})
	if got := resultWatch(t, replayed).ID; got != firstWatch.ID { t.Fatalf("replayed watch id = %d; want %d", got, firstWatch.ID) }
	conflictArgs := strings.ReplaceAll(`{"provider":"fake-market","symbol":"XAU/USD","currency":"USD","operator":">=","threshold":3600}`, `\"`, `"`)
	conflict := registry.Execute(ctx, "market.watch.create", capability.ToolRequest{Key: "watch-request", Arguments: conflictArgs})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") { t.Fatalf("watch conflict = %s", conflict.Content) }
	watches, err := data.ListMarketWatches(ctx, "user-a", "device-a", 20)
	if err != nil { t.Fatal(err) }
	if len(watches) != 1 || watches[0].ID != firstWatch.ID { t.Fatalf("watches = %+v", watches) }
	deleteArgs := fmt.Sprintf(`{"id":%d}`, firstWatch.ID)
	deleteArgs = strings.ReplaceAll(deleteArgs, `\"`, `"`)
	deleted := registry.Execute(ctx, "market.watch.delete", capability.ToolRequest{Key: "watch-delete", Arguments: deleteArgs})
	if !strings.Contains(deleted.Content, `"ok":true`) { t.Fatalf("delete = %s", deleted.Content) }
	replayedDelete := registry.Execute(ctx, "market.watch.delete", capability.ToolRequest{Key: "watch-delete", Arguments: deleteArgs})
	if !strings.Contains(replayedDelete.Content, `"ok":true`) { t.Fatalf("replayed delete = %s", replayedDelete.Content) }
	watches, err = data.ListMarketWatches(ctx, "user-a", "device-a", 20)
	if err != nil { t.Fatal(err) }
	if len(watches) != 0 { t.Fatalf("watches after delete replay = %+v", watches) }
}

func resultMemory(t *testing.T, result capability.ToolResult) memory.Item {
	t.Helper()
	var envelope struct { OK bool `json:"ok"`; Error string `json:"error"`; Memory memory.Item `json:"memory"` }
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil { t.Fatalf("decode memory result: %v (%s)", err, result.Content) }
	if !envelope.OK { t.Fatalf("memory tool failed: %s", envelope.Error) }
	return envelope.Memory
}

func resultWatch(t *testing.T, result capability.ToolResult) market.Watch {
	t.Helper()
	var envelope struct { OK bool `json:"ok"`; Error string `json:"error"`; Watch market.Watch `json:"watch"` }
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil { t.Fatalf("decode watch result: %v (%s)", err, result.Content) }
	if !envelope.OK { t.Fatalf("market watch tool failed: %s", envelope.Error) }
	return envelope.Watch
}

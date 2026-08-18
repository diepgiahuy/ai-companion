package capability

import (
	"context"
	"strings"
	"testing"
)

type guardedTool struct {
	available bool
	called    *bool
}

func (t guardedTool) Name() string { return "device.guarded" }
func (t guardedTool) Definition() *ToolDefinition {
	return &ToolDefinition{Name: t.Name(), Pack: "device", Parameters: map[string]any{"type": "object"}}
}
func (t guardedTool) Available(context.Context) bool { return t.available }
func (t guardedTool) Execute(context.Context, ToolRequest) ToolResult {
	*t.called = true
	return Success(map[string]any{"executed": true})
}

func TestToolRegistryContextAvailabilityGatesExposureAndExecution(t *testing.T) {
	registry := NewToolRegistry()
	called := false
	if err := registry.Register(guardedTool{available: false, called: &called}); err != nil {
		t.Fatal(err)
	}
	if registry.Available(context.Background(), "device.guarded") {
		t.Fatal("guarded tool exposed while unavailable")
	}
	result := registry.Execute(context.Background(), "device.guarded", ToolRequest{Arguments: `{}`})
	if called {
		t.Fatal("unavailable tool handler executed")
	}
	if !strings.Contains(result.Content, "unavailable in current context") {
		t.Fatalf("result=%s", result.Content)
	}
}

package capability

import (
	"context"
	"strings"
	"testing"
)

func TestToolRegistryDiscoversAndExecutesProviders(t *testing.T) {
	registry := NewToolRegistry()
	err := registry.Register(FunctionTool{
		ToolName:       "demo.echo",
		ToolDefinition: &ToolDefinition{Name: "demo.echo", Description: "echo", Parameters: map[string]any{"type": "object"}},
		Handler: func(_ context.Context, request ToolRequest) ToolResult {
			return Success(map[string]any{"key": request.Key, "arguments": request.Arguments})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(FunctionTool{ToolName: "legacy.hidden", Handler: func(context.Context, ToolRequest) ToolResult { return Success(map[string]any{"hidden": true}) }}); err != nil {
		t.Fatal(err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "demo.echo" {
		t.Fatalf("definitions = %+v", definitions)
	}
	result := registry.Execute(context.Background(), "demo.echo", ToolRequest{Key: "turn-1", Arguments: `{"x":1}`})
	if !strings.Contains(result.Content, `"ok":true`) || !strings.Contains(result.Content, `"key":"turn-1"`) {
		t.Fatalf("result = %s", result.Content)
	}
	hidden := registry.Execute(context.Background(), "legacy.hidden", ToolRequest{})
	if !strings.Contains(hidden.Content, `"hidden":true`) {
		t.Fatalf("hidden = %s", hidden.Content)
	}
}

func TestRegistryRejectsInvalidArgumentsBeforeHandler(t *testing.T) {
	r := NewToolRegistry()
	called := false
	err := r.Register(FunctionTool{ToolName: "x", ToolDefinition: &ToolDefinition{Name: "x", Parameters: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"n"}, "additionalProperties": false}}, Handler: func(context.Context, ToolRequest) ToolResult { called = true; return Success(map[string]any{}) }})
	if err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), "x", ToolRequest{Arguments: `{"n":0,"oops":1}`})
	if called {
		t.Fatal("handler must not run")
	}
	if !strings.Contains(got.Content, "rejected") {
		t.Fatalf("unexpected %s", got.Content)
	}
}

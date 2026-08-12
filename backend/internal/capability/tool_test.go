package capability

import (
	"context"
	"encoding/json"
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

func TestToolRegistryDefinitionLookup(t *testing.T) {
	reg := NewToolRegistry()
	original := ToolDefinition{Name: "sample.read", Description: "sample", Pack: "sample", Parameters: map[string]any{"type": "object"}}
	if err := reg.Register(FunctionTool{ToolName: original.Name, ToolDefinition: &original, Handler: func(context.Context, ToolRequest) ToolResult { return Success(map[string]any{}) }}); err != nil {
		t.Fatal(err)
	}
	got, ok := reg.Definition(original.Name)
	if !ok || got.Name != original.Name || got.Pack != original.Pack {
		t.Fatalf("unexpected definition: %#v ok=%v", got, ok)
	}
	if _, ok := reg.Definition("missing"); ok {
		t.Fatal("missing tool unexpectedly resolved")
	}
}

type panicTool struct{}

func (panicTool) Name() string { return "test.panic" }
func (panicTool) Definition() *ToolDefinition {
	return &ToolDefinition{Name: "test.panic", Risk: "read", Parameters: map[string]any{"type": "object"}}
}
func (panicTool) Execute(context.Context, ToolRequest) ToolResult {
	panic("secret panic payload must never reach model")
}

func TestToolRegistryContainsToolPanicsAsGenericFailure(t *testing.T) {
	reg := NewToolRegistry()
	if err := reg.Register(panicTool{}); err != nil {
		t.Fatal(err)
	}
	result := reg.Execute(context.Background(), "test.panic", ToolRequest{Arguments: `{}`})
	if strings.Contains(result.Content, "secret panic payload") {
		t.Fatalf("panic payload leaked: %s", result.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Content), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false || out["error"] != "internal tool execution failed" {
		t.Fatalf("unexpected panic failure: %#v", out)
	}
}

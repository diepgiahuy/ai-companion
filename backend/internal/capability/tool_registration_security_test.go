package capability

import (
	"context"
	"testing"
)

func TestRegistryRejectsNonCanonicalAndMismatchedDefinitionNames(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(FunctionTool{
		ToolName:       " unsafe.read ",
		ToolDefinition: &ToolDefinition{Name: "unsafe.read", Parameters: map[string]any{"type": "object"}},
		Handler:        func(context.Context, ToolRequest) ToolResult { return Success(map[string]any{}) },
	}); err == nil {
		t.Fatal("non-canonical registry name was accepted")
	}

	if err := registry.Register(FunctionTool{
		ToolName:       "internal.write",
		ToolDefinition: &ToolDefinition{Name: "model.read", Parameters: map[string]any{"type": "object"}},
		Handler:        func(context.Context, ToolRequest) ToolResult { return Success(map[string]any{}) },
	}); err == nil {
		t.Fatal("definition name mismatch was accepted")
	}
}

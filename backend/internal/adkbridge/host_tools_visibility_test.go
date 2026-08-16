package adkbridge

import (
	"context"
	"testing"

	"companion-server/internal/capability"
)

func TestHostToolExecutorRejectsRegisteredButUnexposedTool(t *testing.T) {
	registry := capability.NewToolRegistry()
	called := false
	if err := registry.Register(capability.FunctionTool{
		ToolName: "internal.hidden",
		Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
			called = true
			return capability.Success(map[string]any{"hidden": true})
		},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := (HostToolExecutor{Registry: registry}).Execute(
		context.Background(), "internal.hidden", "call-1", map[string]any{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("unexposed tool handler executed from model ingress")
	}
	if result["ok"] != false || result["error_code"] != "tool_not_exposed" || result["execution_status"] != "not_started" || result["retryable"] != false {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestHostToolExecutorRejectsUnknownProviderToolNameBeforeRegistryExecute(t *testing.T) {
	registry := capability.NewToolRegistry()
	result, err := (HostToolExecutor{Registry: registry}).Execute(
		context.Background(), "provider.hallucinated", "call-2", map[string]any{"x": 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != false || result["error_code"] != "tool_not_exposed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

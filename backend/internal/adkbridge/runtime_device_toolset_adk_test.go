//go:build adk

package adkbridge

import (
	"context"
	"encoding/json"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/devicecap"
	"companion-server/internal/pipeline"
)

type exposureEndpoint struct {
	supported bool
}

func (e exposureEndpoint) Supports(name, version string) bool {
	return e.supported && name == devicecap.VolumeSetName && version == devicecap.VolumeSetVersion
}
func (e exposureEndpoint) Call(context.Context, devicecap.Call) (devicecap.Result, error) {
	return devicecap.Result{Value: json.RawMessage(`{"applied":true}`)}, nil
}

func TestStaticToolsExcludeDevicePackAndDeviceToolsetUsesCurrentDevice(t *testing.T) {
	registry := capability.NewToolRegistry()
	if err := registry.Register(capability.FunctionTool{
		ToolName: "notes.list",
		ToolDefinition: &capability.ToolDefinition{
			Name: "notes.list", Description: "List notes", Pack: "personal",
			Parameters: map[string]any{"type": "object"},
		},
		Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
			return capability.Success(map[string]any{"notes": []any{}})
		},
	}); err != nil {
		t.Fatal(err)
	}
	router := devicecap.NewRouter()
	if err := router.Register("device-a", exposureEndpoint{supported: true}); err != nil {
		t.Fatal(err)
	}
	if err := router.Register("device-b", exposureEndpoint{supported: false}); err != nil {
		t.Fatal(err)
	}
	if err := devicecap.RegisterTools(registry, router); err != nil {
		t.Fatal(err)
	}

	static, err := buildRegistryTools(registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(static) != 1 || static[0].Name() != "notes.list" {
		t.Fatalf("static tools=%v", toolNames(static))
	}

	toolset := registryDeviceToolset{registry: registry}
	ctxA := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a", TurnID: "turn-a"})
	deviceATools, err := toolset.toolsForContext(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if len(deviceATools) != 1 || deviceATools[0].Name() != devicecap.VolumeSetName {
		t.Fatalf("Device A tools=%v", toolNames(deviceATools))
	}

	ctxB := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-b", TurnID: "turn-b"})
	deviceBTools, err := toolset.toolsForContext(ctxB)
	if err != nil {
		t.Fatal(err)
	}
	if len(deviceBTools) != 0 {
		t.Fatalf("Device B tools=%v", toolNames(deviceBTools))
	}
}

func toolNames[T interface{ Name() string }](tools []T) []string {
	out := make([]string, 0, len(tools))
	for _, item := range tools {
		out = append(out, item.Name())
	}
	return out
}

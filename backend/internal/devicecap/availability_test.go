package devicecap

import (
	"context"
	"strings"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

func TestVolumeToolAvailabilityUsesTrustedCurrentDeviceSupport(t *testing.T) {
	registry := capability.NewToolRegistry()
	router := NewRouter()
	endpointA := &fakeEndpoint{supported: true}
	endpointB := &fakeEndpoint{supported: false}
	if err := router.Register("device-a", endpointA); err != nil {
		t.Fatal(err)
	}
	if err := router.Register("device-b", endpointB); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTools(registry, router); err != nil {
		t.Fatal(err)
	}

	ctxA := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a", TurnID: "turn-a"})
	ctxB := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-b", TurnID: "turn-b"})
	if !registry.Available(ctxA, VolumeSetName) {
		t.Fatal("advertised Device A volume tool hidden")
	}
	if registry.Available(ctxB, VolumeSetName) {
		t.Fatal("unsupported Device B volume tool exposed")
	}

	result := registry.Execute(ctxB, VolumeSetName, capability.ToolRequest{Arguments: `{"volume":42}`})
	if endpointB.calls.Load() != 0 {
		t.Fatalf("unsupported Device B reached endpoint calls=%d", endpointB.calls.Load())
	}
	if !strings.Contains(result.Content, "unavailable in current context") {
		t.Fatalf("result=%s", result.Content)
	}
}

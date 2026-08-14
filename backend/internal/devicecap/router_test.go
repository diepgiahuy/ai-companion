package devicecap

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

type fakeEndpoint struct {
	supported bool
	calls     atomic.Int32
	last      Call
	err       error
}

func (e *fakeEndpoint) Supports(name, version string) bool {
	return e.supported && name == VolumeSetName && version == VolumeSetVersion
}
func (e *fakeEndpoint) Call(_ context.Context, call Call) (Result, error) {
	e.calls.Add(1)
	e.last = call
	if e.err != nil {
		return Result{}, e.err
	}
	return Result{Value: json.RawMessage(`{"applied":true}`)}, nil
}

func TestRouterRequiresAdvertisedVersionAndAuthenticatedDevice(t *testing.T) {
	router := NewRouter()
	endpoint := &fakeEndpoint{}
	if err := router.Register("device-a", endpoint); err != nil { t.Fatal(err) }

	if _, err := router.Call(context.Background(), "device-a", Call{Name: VolumeSetName, Version: VolumeSetVersion}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported error=%v", err)
	}
	if endpoint.calls.Load() != 0 { t.Fatalf("unsupported call reached endpoint") }

	endpoint.supported = true
	if _, err := router.Call(context.Background(), "", Call{Name: VolumeSetName, Version: VolumeSetVersion}); err == nil {
		t.Fatal("empty authenticated device id accepted")
	}
	if _, err := router.Call(context.Background(), "device-a", Call{Name: VolumeSetName, Version: "2"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("version mismatch error=%v", err)
	}
	if endpoint.calls.Load() != 0 { t.Fatalf("version mismatch reached endpoint") }
}

func TestDeviceToolUsesTurnDeviceIDNotModelArguments(t *testing.T) {
	registry := capability.NewToolRegistry()
	router := NewRouter()
	endpointA := &fakeEndpoint{supported: true}
	endpointB := &fakeEndpoint{supported: true}
	if err := router.Register("device-a", endpointA); err != nil { t.Fatal(err) }
	if err := router.Register("device-b", endpointB); err != nil { t.Fatal(err) }
	if err := RegisterTools(registry, router); err != nil { t.Fatal(err) }

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", DeviceID: "device-a"})
	result := registry.Execute(ctx, VolumeSetName, capability.ToolRequest{Arguments: `{"volume":42}`})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil { t.Fatal(err) }
	if decoded["ok"] != true || decoded["device_id"] != "device-a" {
		t.Fatalf("result=%s", result.Content)
	}
	if endpointA.calls.Load() != 1 || endpointB.calls.Load() != 0 {
		t.Fatalf("calls A=%d B=%d", endpointA.calls.Load(), endpointB.calls.Load())
	}
	var args map[string]any
	if err := json.Unmarshal(endpointA.last.Arguments, &args); err != nil { t.Fatal(err) }
	if args["volume"] != float64(42) { t.Fatalf("forwarded args=%v", args) }
}

func TestDeviceToolRejectsMissingTurnAndOutOfRangeBeforeEndpoint(t *testing.T) {
	registry := capability.NewToolRegistry()
	router := NewRouter()
	endpoint := &fakeEndpoint{supported: true}
	_ = router.Register("device-a", endpoint)
	if err := RegisterTools(registry, router); err != nil { t.Fatal(err) }

	missing := registry.Execute(context.Background(), VolumeSetName, capability.ToolRequest{Arguments: `{"volume":42}`})
	if missing.Content == "" || endpoint.calls.Load() != 0 { t.Fatalf("missing turn=%s", missing.Content) }

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a"})
	invalid := registry.Execute(ctx, VolumeSetName, capability.ToolRequest{Arguments: `{"volume":101}`})
	if invalid.Content == "" || endpoint.calls.Load() != 0 { t.Fatalf("invalid range=%s", invalid.Content) }
}

package devicecap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
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

func TestSettingsArgsRejectWakeModelUntilPlan07B(t *testing.T) {
	_, err := json.Marshal(SettingsArgs{Version: 7, Settings: controlplane.RuntimeConfig{WakeModel: "wn9_hey_computer"}})
	if err == nil || !strings.Contains(err.Error(), "PLAN 07B") {
		t.Fatalf("wake model must fail closed until Audio owner can apply it, err=%v", err)
	}

	wire, err := json.Marshal(SettingsArgs{Version: 8, Settings: controlplane.RuntimeConfig{}})
	if err != nil {
		t.Fatalf("normal settings marshal failed: %v", err)
	}
	if strings.Contains(string(wire), "wake_model") {
		t.Fatalf("wake_model leaked onto PLAN 07A device wire: %s", wire)
	}
}

func TestDeviceToolUsesTurnDeviceIDNotModelArguments(t *testing.T) {
	registry := capability.NewToolRegistry()
	router := NewRouter()
	endpointA := &fakeEndpoint{supported: true}
	endpointB := &fakeEndpoint{supported: true}
	if err := router.Register("device-a", endpointA); err != nil { t.Fatal(err) }
	if err := router.Register("device-b", endpointB); err != nil { t.Fatal(err) }
	if err := RegisterTools(registry, router); err != nil { t.Fatal(err) }

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", DeviceID: "device-a", TurnID: "turn-a"})
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
	if endpointA.last.TurnID != "turn-a" { t.Fatalf("forwarded turn=%q", endpointA.last.TurnID) }
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

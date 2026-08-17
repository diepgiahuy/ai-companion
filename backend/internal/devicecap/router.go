package devicecap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/pipeline"
)

const (
	VolumeSetName           = "device.volume.set"
	VolumeSetVersion        = "1"
	UserConfirmationName    = "device.user_confirmation"
	UserConfirmationVersion = "1"
	SettingsName            = "device.settings_v1"
	SettingsVersion         = "1"
)

var (
	ErrOffline     = errors.New("device capability endpoint is offline")
	ErrUnsupported = errors.New("device capability is not advertised by this device")
)

type Descriptor struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

type SettingsArgs struct {
	Version  int64                       `json:"version"`
	Settings controlplane.RuntimeConfig `json:"settings"`
}

type SettingsResult struct {
	Applied  bool                        `json:"applied"`
	Version  int64                       `json:"version"`
	Settings *controlplane.RuntimeConfig `json:"settings,omitempty"`
	Error    string                      `json:"error,omitempty"`
}

type Call struct {
	Name      string
	Version   string
	Arguments json.RawMessage
	TurnID    string
	Deadline  time.Time
}

type Result struct {
	Value json.RawMessage
}

type Endpoint interface {
	Supports(name, version string) bool
	Call(context.Context, Call) (Result, error)
}

type Router struct {
	mu       sync.RWMutex
	byDevice map[string]Endpoint
}

func NewRouter() *Router { return &Router{byDevice: map[string]Endpoint{}} }

func (r *Router) Register(deviceID string, endpoint Endpoint) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || endpoint == nil {
		return fmt.Errorf("device capability registration requires device id and endpoint")
	}
	r.mu.Lock()
	r.byDevice[deviceID] = endpoint
	r.mu.Unlock()
	return nil
}

func (r *Router) Unregister(deviceID string, endpoint Endpoint) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || endpoint == nil {
		return
	}
	r.mu.Lock()
	if r.byDevice[deviceID] == endpoint {
		delete(r.byDevice, deviceID)
	}
	r.mu.Unlock()
}

func (r *Router) Call(ctx context.Context, deviceID string, call Call) (Result, error) {
	if r == nil {
		return Result{}, ErrOffline
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return Result{}, fmt.Errorf("authenticated device id is required")
	}
	r.mu.RLock()
	endpoint := r.byDevice[deviceID]
	r.mu.RUnlock()
	if endpoint == nil {
		return Result{}, ErrOffline
	}
	if !endpoint.Supports(call.Name, call.Version) {
		return Result{}, ErrUnsupported
	}
	if !call.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, call.Deadline)
		defer cancel()
	}
	return endpoint.Call(ctx, call)
}

// RequestConfirmation implements capability.ConfirmationRequester using the
// existing authenticated device capability channel. ArgumentsHash is purposely
// not transmitted: the server-side policy keeps that exact-arguments binding,
// while the device can approve only the unique correlated call it received.
func (r *Router) RequestConfirmation(ctx context.Context, target capability.ConfirmationTarget, intent capability.ConfirmationIntent) (bool, error) {
	if strings.TrimSpace(target.DeviceID) == "" || strings.TrimSpace(target.TurnID) == "" {
		return false, fmt.Errorf("confirmation requires authenticated device and turn")
	}
	toolName := strings.TrimSpace(intent.ToolName)
	prompt := strings.TrimSpace(intent.Description)
	if toolName == "" || len(toolName) > 96 || prompt == "" || len(prompt) > 192 {
		return false, fmt.Errorf("confirmation intent is invalid")
	}
	if intent.ExpiresAt.IsZero() || !intent.ExpiresAt.After(time.Now()) || time.Until(intent.ExpiresAt) > 5*time.Second {
		return false, fmt.Errorf("confirmation expiry is invalid")
	}
	arguments, err := json.Marshal(map[string]any{"tool_name": toolName, "prompt": prompt})
	if err != nil {
		return false, err
	}
	result, err := r.Call(ctx, target.DeviceID, Call{
		Name: UserConfirmationName, Version: UserConfirmationVersion,
		Arguments: arguments, TurnID: target.TurnID, Deadline: intent.ExpiresAt,
	})
	if err != nil {
		return false, err
	}
	var decoded struct {
		Approved bool `json:"approved"`
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return false, fmt.Errorf("invalid confirmation result: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("invalid confirmation result trailing data")
	}
	return decoded.Approved, nil
}

var _ capability.ConfirmationRequester = (*Router)(nil)

func RegisterTools(registry *capability.ToolRegistry, router *Router) error {
	if registry == nil || router == nil {
		return fmt.Errorf("device capability ToolRegistry and router are required")
	}
	return registry.Register(capability.FunctionTool{
		ToolName: VolumeSetName,
		ToolDefinition: &capability.ToolDefinition{
			Name: VolumeSetName,
			Description: "Set the authenticated current device speaker volume from 0 to 100.",
			Pack: "device", Risk: "write",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{"volume": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}},
				"required": []string{"volume"},
				"additionalProperties": false,
			},
		},
		Handler: func(ctx context.Context, req capability.ToolRequest) capability.ToolResult {
			turn, ok := pipeline.CurrentTurn(ctx)
			if !ok || strings.TrimSpace(turn.DeviceID) == "" {
				return capability.Failure(fmt.Errorf("device capability requires authenticated turn context"))
			}
			var args struct{ Volume int `json:"volume"` }
			if err := json.Unmarshal([]byte(req.Arguments), &args); err != nil {
				return capability.Failure(fmt.Errorf("decode device capability arguments: %w", err))
			}
			payload, _ := json.Marshal(map[string]any{"volume": args.Volume})
			result, err := router.Call(ctx, turn.DeviceID, Call{
				Name: VolumeSetName, Version: VolumeSetVersion, Arguments: payload,
				Deadline: time.Now().Add(3 * time.Second),
			})
			if err != nil {
				return capability.Failure(err)
			}
			var value any
			if len(result.Value) > 0 {
				if err := json.Unmarshal(result.Value, &value); err != nil {
					return capability.Failure(fmt.Errorf("invalid device capability result: %w", err))
				}
			}
			return capability.Success(map[string]any{"device_id": turn.DeviceID, "capability": VolumeSetName, "result": value})
		},
	})
}

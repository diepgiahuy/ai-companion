package devicecap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

const (
	VolumeSetName    = "device.volume.set"
	VolumeSetVersion = "1"
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

type Call struct {
	Name      string
	Version   string
	Arguments json.RawMessage
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
				Name: VolumeSetName, Version: VolumeSetVersion, Arguments: payload, Deadline: time.Now().Add(3 * time.Second),
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

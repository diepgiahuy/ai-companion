package speech

import "context"

// NativeRealtimeTool is a Companion-owned tool declaration exposed to a native
// audio model for benchmark/reference use. Name remains the canonical
// ToolRegistry name; adapters map it to provider-safe names and back.
type NativeRealtimeTool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// NativeRealtimeToolCall is a provider request that must be executed by the
// Companion controller/ToolRegistry. Native providers never execute tools or
// own durable application state themselves.
type NativeRealtimeToolCall struct {
	CallID    string
	Name      string
	Arguments map[string]any
}

// NativeRealtimeEvent is the normalized full-duplex surface used only by
// benchmark/reference controllers until a native-audio path is separately
// measured and promoted. Product Protocol v2 remains unchanged.
type NativeRealtimeEvent struct {
	Type              string
	InputTranscript   string
	InputFinal        bool
	TextDelta         string
	AudioTranscript   string
	AudioPCM          []byte
	ToolCall          *NativeRealtimeToolCall
	ResponseDone      bool
	ResponseStatus    string
}

// NativeRealtimeSession exposes explicit turn/cancellation/tool-result actions
// while keeping provider WebSocket details out of server/session code.
type NativeRealtimeSession interface {
	AppendAudio(ctx context.Context, pcm16Mono16k []byte) error
	CommitAudio(ctx context.Context) error
	CreateResponse(ctx context.Context) error
	CancelResponse(ctx context.Context) error
	ReturnToolResult(ctx context.Context, callID, output string) error
	Receive(ctx context.Context) (NativeRealtimeEvent, error)
	Close() error
}

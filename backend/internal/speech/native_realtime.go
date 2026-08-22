package speech

import "context"

type NativeRealtimeTool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type NativeRealtimeToolCall struct {
	CallID    string
	Name      string
	Arguments map[string]any
}

type NativeRealtimeEvent struct {
	Type             string
	InputTranscript  string
	InputFinal       bool
	TextDelta        string
	AudioTranscript  string
	AudioPCM         []byte
	ToolCall         *NativeRealtimeToolCall
	ResponseDone     bool
	ResponseStatus   string
	ResumptionHandle string
	Resumable        bool
}

// NativeRealtimeProvider is the provider-neutral connection seam used by
// benchmark/reference native-realtime clients. Product policy/session ownership
// remains in Companion; this interface grants no provider-side tool authority.
type NativeRealtimeProvider interface {
	Connect(ctx context.Context) (NativeRealtimeSession, error)
}

type NativeRealtimeSession interface {
	AppendAudio(ctx context.Context, pcm16Mono16k []byte) error
	CommitAudio(ctx context.Context) error
	CreateResponse(ctx context.Context) error
	CancelResponse(ctx context.Context) error
	ReturnToolResult(ctx context.Context, callID, output string) error
	Receive(ctx context.Context) (NativeRealtimeEvent, error)
	Close() error
}

package pipeline

import "context"

type ASR interface {
	Transcribe(ctx context.Context, pcm []byte) (string, error)
}

// ASRPartial is provider-neutral streaming recognition state. Providers can
// omit stability/confidence when unavailable; turn detection must not depend on
// one vendor-specific score.
type ASRPartial struct {
	Text       string
	Final      bool
	Confidence float64
	Stable     bool
}

// StreamingASR is an optional capability for providers that can emit partial
// transcripts while audio is still arriving. The existing batch ASR interface
// remains the rollback/compatibility boundary.
type StreamingASR interface {
	TranscribeStream(ctx context.Context, pcm <-chan []byte, emit func(ASRPartial) error) (string, error)
}

type Agent interface {
	Respond(ctx context.Context, turnID, transcript string) (string, error)
}

type UICard struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Primary   string `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
	Progress  int    `json:"progress,omitempty"`
}

type AgentResult struct {
	Text string
	UI   *UICard
}

type RichAgent interface {
	RespondRich(ctx context.Context, turnID, transcript string) (AgentResult, error)
}

type TTS interface {
	Synthesize(ctx context.Context, text string, emit func([]byte) error) error
}

type AudioCodec interface {
	DecodeUplink(packet []byte) ([]byte, error)
	EncodeDownlink(pcm []byte) ([]byte, error)
}

type CodecFactory interface {
	New() (AudioCodec, error)
}

type Components struct {
	ASR    ASR
	Agent  Agent
	TTS    TTS
	Codecs CodecFactory
}

// TurnContext carries bounded, ephemeral metadata for the current user turn.
// It lets tools and providers use canonical turn identity without server coupling.
type TurnContext struct {
	UserID     string
	ThreadID   string
	DeviceID   string
	SessionID  string
	TurnID     string
	Transcript string
	PCM16Mono  []byte
	SampleRate int
	Locale     string
	Timezone   string
	VoiceKey   string
	TenantID   string
	Plan       string
	Done       <-chan struct{}
}

type turnContextKey struct{}

func WithTurnContext(ctx context.Context, turn TurnContext) context.Context {
	return context.WithValue(ctx, turnContextKey{}, turn)
}

func CurrentTurn(ctx context.Context) (TurnContext, bool) {
	turn, ok := ctx.Value(turnContextKey{}).(TurnContext)
	return turn, ok
}

// AgentStreamEvent is the normalized streaming surface between an agent runtime
// (ADK, local model adapter, cloud model, etc.) and the realtime voice session.
// Tool execution remains inside the agent/runtime; the voice server consumes
// only user-presentable deltas and UI/status updates.
type AgentStreamEvent struct {
	TextDelta string
	UI        *UICard
	Status    string
	ToolName  string
}

// StreamingAgent allows text to reach sentence segmentation/TTS before the
// model has finished the whole response. Implementations must return promptly
// when ctx is cancelled so barge-in can stop a stale generation.
type StreamingAgent interface {
	Stream(ctx context.Context, turnID, transcript string, emit func(AgentStreamEvent) error) error
}

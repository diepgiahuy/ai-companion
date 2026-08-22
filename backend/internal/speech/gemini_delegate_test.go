package speech

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
)

func TestGeminiDelegatingBridgeKeepsTurnsIsolated(t *testing.T) {
	frameBytes := protocol.DownlinkSamplesPerFrame * 2
	provider := &fakeNativeRealtimeProvider{sessions: []*fakeNativeRealtimeSession{
		newFakeNativeRealtimeSession("one", "call-one", append(make([]byte, frameBytes), 1, 1)),
		newFakeNativeRealtimeSession("two", "call-two", append(make([]byte, frameBytes), 2, 2)),
	}}
	bridge, err := NewGeminiDelegatingBridge(provider)
	if err != nil {
		t.Fatal(err)
	}

	ctxOne := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{SessionID: "session-one", TurnID: "turn-one"})
	ctxTwo := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{SessionID: "session-two", TurnID: "turn-two"})
	pcm := make([]byte, 2560)

	transcriptOne, err := bridge.Transcribe(ctxOne, pcm)
	if err != nil {
		t.Fatal(err)
	}
	transcriptTwo, err := bridge.Transcribe(ctxTwo, pcm)
	if err != nil {
		t.Fatal(err)
	}
	if transcriptOne != "one" || transcriptTwo != "two" {
		t.Fatalf("transcripts=(%q,%q)", transcriptOne, transcriptTwo)
	}

	var framesOne, framesTwo [][]byte
	if err := bridge.Synthesize(ctxOne, "reply-one", func(frame []byte) error {
		framesOne = append(framesOne, append([]byte(nil), frame...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Synthesize(ctxTwo, "reply-two", func(frame []byte) error {
		framesTwo = append(framesTwo, append([]byte(nil), frame...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(framesOne) != 2 || len(framesTwo) != 2 {
		t.Fatalf("frame counts=(%d,%d), want (2,2)", len(framesOne), len(framesTwo))
	}
	if framesOne[1][0] != 1 || framesTwo[1][0] != 2 {
		t.Fatalf("padded frame markers=(%d,%d)", framesOne[1][0], framesTwo[1][0])
	}
	if provider.sessions[0].toolResult != "reply-one" || provider.sessions[1].toolResult != "reply-two" {
		t.Fatalf("tool results=(%q,%q)", provider.sessions[0].toolResult, provider.sessions[1].toolResult)
	}
	if provider.sessions[0].toolCallID != "call-one" || provider.sessions[1].toolCallID != "call-two" {
		t.Fatalf("tool call ids=(%q,%q)", provider.sessions[0].toolCallID, provider.sessions[1].toolCallID)
	}
}

func TestGeminiDelegatingBridgeCancelsProviderWithTurn(t *testing.T) {
	session := newFakeNativeRealtimeSession("hello", "call-cancel", nil)
	provider := &fakeNativeRealtimeProvider{sessions: []*fakeNativeRealtimeSession{session}}
	bridge, err := NewGeminiDelegatingBridge(provider)
	if err != nil {
		t.Fatal(err)
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := pipeline.WithTurnContext(baseCtx, pipeline.TurnContext{SessionID: "session-cancel", TurnID: "turn-cancel"})
	if _, err := bridge.Transcribe(ctx, make([]byte, 1280)); err != nil {
		t.Fatal(err)
	}
	cancel()

	select {
	case <-session.cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider cancellation was not observed")
	}
	if err := bridge.Synthesize(ctx, "stale", func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Synthesize error=%v, want context canceled", err)
	}
}

func TestGeminiDelegatingBridgeRejectsAudioBeforeDelegation(t *testing.T) {
	session := &fakeNativeRealtimeSession{
		events: []NativeRealtimeEvent{
			{Type: "input_audio_transcription.completed", InputTranscript: "hello", InputFinal: true},
			{Type: "response.audio.delta", AudioPCM: []byte{0, 0}},
		},
		cancelled: make(chan struct{}),
	}
	provider := &fakeNativeRealtimeProvider{sessions: []*fakeNativeRealtimeSession{session}}
	bridge, err := NewGeminiDelegatingBridge(provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{SessionID: "session-unsafe", TurnID: "turn-unsafe"})
	_, err = bridge.Transcribe(ctx, make([]byte, 1280))
	if err == nil || err.Error() != "Gemini emitted audio before Companion delegation" {
		t.Fatalf("error=%v", err)
	}
	if session.closeCalls != 1 {
		t.Fatalf("close calls=%d, want 1", session.closeCalls)
	}
}

type fakeNativeRealtimeProvider struct {
	mu       sync.Mutex
	sessions []*fakeNativeRealtimeSession
	next     int
}

func (p *fakeNativeRealtimeProvider) Connect(context.Context) (NativeRealtimeSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.sessions) {
		return nil, errors.New("no fake session available")
	}
	session := p.sessions[p.next]
	p.next++
	return session, nil
}

type fakeNativeRealtimeSession struct {
	mu sync.Mutex

	events     []NativeRealtimeEvent
	next       int
	toolCallID string
	toolResult string
	cancelled  chan struct{}
	cancelOnce sync.Once
	closeCalls int
}

func newFakeNativeRealtimeSession(transcript, callID string, audio []byte) *fakeNativeRealtimeSession {
	events := []NativeRealtimeEvent{
		{Type: "input_audio_transcription.completed", InputTranscript: transcript, InputFinal: true},
		{Type: "response.function_call_arguments.done", ToolCall: &NativeRealtimeToolCall{CallID: callID, Name: "companion_delegate"}},
	}
	if len(audio) > 0 {
		events = append(events,
			NativeRealtimeEvent{Type: "response.audio.delta", AudioPCM: audio},
			NativeRealtimeEvent{Type: "response.done", ResponseDone: true, ResponseStatus: "completed"},
		)
	}
	return &fakeNativeRealtimeSession{events: events, cancelled: make(chan struct{})}
}

func (s *fakeNativeRealtimeSession) AppendAudio(context.Context, []byte) error { return nil }
func (s *fakeNativeRealtimeSession) CommitAudio(context.Context) error         { return nil }
func (s *fakeNativeRealtimeSession) CreateResponse(context.Context) error      { return nil }

func (s *fakeNativeRealtimeSession) CancelResponse(context.Context) error {
	s.cancelOnce.Do(func() { close(s.cancelled) })
	return nil
}

func (s *fakeNativeRealtimeSession) ReturnToolResult(_ context.Context, callID, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCallID = callID
	s.toolResult = output
	return nil
}

func (s *fakeNativeRealtimeSession) Receive(ctx context.Context) (NativeRealtimeEvent, error) {
	s.mu.Lock()
	if s.next < len(s.events) {
		event := s.events[s.next]
		s.next++
		s.mu.Unlock()
		return event, nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return NativeRealtimeEvent{}, ctx.Err()
}

func (s *fakeNativeRealtimeSession) Close() error {
	s.mu.Lock()
	s.closeCalls++
	s.mu.Unlock()
	return nil
}

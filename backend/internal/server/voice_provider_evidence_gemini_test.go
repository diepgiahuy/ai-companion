//go:build adk

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/adkbridge"
	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	"companion-server/internal/speech"
	"companion-server/internal/store"

	"github.com/coder/websocket"
)

// TestVoiceEvidenceCanonicalGeminiLive keeps the production server path intact:
// the Gemini-specific glue is test-only. Gemini supplies native audio input,
// transcription, a mandatory delegation function call and native audio output;
// Companion still owns the authenticated /v2/device turn, ADK and ToolRegistry.
func TestVoiceEvidenceCanonicalGeminiLive(t *testing.T) {
	if os.Getenv("COMPANION_GEMINI_LIVE_EVIDENCE") != "1" {
		t.Skip("Gemini Live canonical evidence is opt-in")
	}
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_TOKEN"))
	pcmPath := strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_PCM"))
	reportPath := strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_GEMINI_CANONICAL_REPORT"))
	model := strings.TrimSpace(os.Getenv("GEMINI_LIVE_MODEL"))
	voice := strings.TrimSpace(os.Getenv("GEMINI_LIVE_VOICE"))
	if model == "" {
		model = "gemini-3.1-flash-live-preview"
	}
	if voice == "" {
		voice = "Kore"
	}
	if apiKey == "" || pcmPath == "" || reportPath == "" {
		t.Fatal("GEMINI_TOKEN, VOICE_EVIDENCE_PCM and VOICE_EVIDENCE_GEMINI_CANONICAL_REPORT are required")
	}
	pcm, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		t.Fatalf("invalid PCM fixture length %d", len(pcm))
	}
	maxFrames := protocol.UplinkSampleRate * protocol.MaximumAudioSecs / protocol.UplinkSamplesPerFrame
	maxPCMBytes := maxFrames * protocol.UplinkSamplesPerFrame * 2
	trimmed := false
	if len(pcm) > maxPCMBytes {
		pcm = pcm[:maxPCMBytes]
		trimmed = true
	}

	provider, err := speech.NewGeminiLive(speech.GeminiLiveConfig{
		Model:  model,
		APIKey: apiKey,
		Voice:  voice,
		Instructions: "You are the voice transport for Companion. For every user audio turn, do not answer or speak first. Call the only available function exactly once and wait for its result. After the function result arrives, speak exactly the string in response.result and nothing else.",
		Tools: []speech.NativeRealtimeTool{{
			Name:        "companion_delegate",
			Description: "MANDATORY handoff to Companion. Call exactly once for every user audio turn before producing any answer or audio. Companion returns the final text to speak.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge := &geminiCanonicalBridge{provider: provider}

	var toolExecutions atomic.Int32
	registry := capability.NewToolRegistry()
	definition := &capability.ToolDefinition{
		Name:        "benchmark_echo",
		Description: "Echo one benchmark marker through the canonical ToolRegistry.",
		Pack:        "benchmark",
		Risk:        "read",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required":             []string{"value"},
			"additionalProperties": false,
		},
	}
	if err := registry.Register(capability.FunctionTool{
		ToolName:       definition.Name,
		ToolDefinition: definition,
		Handler: func(_ context.Context, request capability.ToolRequest) capability.ToolResult {
			toolExecutions.Add(1)
			return capability.Success(map[string]any{"echo": request.Arguments})
		},
	}); err != nil {
		t.Fatal(err)
	}

	const shortReply = "Đã xong."
	const longReply = "Đã xong. Đây là phản hồi kiểm thử dài để giữ luồng âm thanh hoạt động đủ lâu cho Companion gửi lệnh hủy lượt một cách có thể quan sát được."
	var llmRequests atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requestNumber := llmRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if requestNumber%2 == 1 {
			callID := fmt.Sprintf("call-gemini-evidence-%d", requestNumber)
			fmt.Fprintf(w, "data: {\"id\":\"gemini-canonical-tool\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"benchmark_echo\",\"arguments\":\"{\\\"value\\\":\\\"canonical-gemini-session\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", callID)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		reply := shortReply
		if requestNumber >= 4 {
			reply = longReply
		}
		fmt.Fprintf(w, "data: {\"id\":\"gemini-canonical-reply\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", reply)
		fmt.Fprint(w, "data: {\"id\":\"gemini-canonical-reply\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	data, err := store.Open(filepath.Join(t.TempDir(), "gemini-voice-evidence.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	conversation := conversationctx.New(voiceEvidenceConversationStore{data: data}, conversationctx.NewMemoryCache(10*time.Minute, 4))
	agent, err := adkbridge.New(adkbridge.Config{
		AppName:       "companion-gemini-voice-evidence",
		ModelName:     "evidence-model",
		ModelProtocol: adkbridge.ModelProtocolChatCompletions,
		BaseURL:       llm.URL,
		APIKey:        "fixture-key",
		Instruction:   "Use benchmark_echo once, then answer with a short Vietnamese confirmation.",
		PromptVersion: "gemini-voice-evidence@1",
		HTTPClient:    llm.Client(),
		Tools:         registry,
		Conversation:  conversation,
		HistoryLimit:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	batchAgent := batchOnlyAgent{delegate: agent}

	service := newAuthenticatedTestServer(pipeline.Components{
		ASR:    bridge,
		Agent:  batchAgent,
		TTS:    bridge,
		Codecs: pipeline.OpusFactory{},
	}, WithStore(data))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("device-gemini-voice-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "Gemini voice evidence done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, connection)
	if hello.Type != protocol.SessionReadyType || hello.SessionID == "" {
		t.Fatalf("invalid session.ready: %+v", hello)
	}

	turnID := "turn-gemini-live-evidence-normal"
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID})
	if err := writeEvidencePCM(ctx, connection, pcm); err != nil {
		t.Fatal(err)
	}
	turnStarted := time.Now()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: turnID})

	transcript := ""
	binaryFrames := 0
	gotTTSStart := false
	gotTTSStop := false
	for !gotTTSStop {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("canonical Gemini read failed: %v transcript=%q tools=%d delegates=%d tts_start=%v frames=%d", err, transcript, toolExecutions.Load(), bridge.delegateCalls.Load(), gotTTSStart, binaryFrames)
		}
		if kind == websocket.MessageBinary {
			binaryFrames++
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == protocol.ProtocolErrorType {
			t.Fatalf("canonical Gemini session returned protocol error: %s %s", message.Code, message.Message)
		}
		switch {
		case message.Type == protocol.TranscriptFinalType:
			transcript = strings.TrimSpace(message.Text)
		case message.Type == protocol.TTSLifecycleType && message.State == "start":
			gotTTSStart = true
		case message.Type == protocol.TTSLifecycleType && message.State == "stop":
			gotTTSStop = true
		}
	}
	normalTurnMS := float64(time.Since(turnStarted).Microseconds()) / 1000
	if transcript == "" {
		t.Fatal("Gemini Live produced no canonical transcript")
	}
	if toolExecutions.Load() != 1 {
		t.Fatalf("normal ToolRegistry executions=%d, want 1", toolExecutions.Load())
	}
	if bridge.delegateCalls.Load() != 1 {
		t.Fatalf("normal Gemini delegation calls=%d, want 1", bridge.delegateCalls.Load())
	}
	if !gotTTSStart || binaryFrames == 0 {
		t.Fatalf("Gemini native audio stream incomplete: start=%v frames=%d", gotTTSStart, binaryFrames)
	}
	normalTools := toolExecutions.Load()

	// Second turn proves a canonical turn.abort reaches the active native provider
	// and that the production generation gate prevents any old-turn audio from
	// reaching the device after the authoritative interrupted marker.
	abortTurnID := "turn-gemini-live-evidence-abort"
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: abortTurnID})
	if err := writeEvidencePCM(ctx, connection, pcm); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: abortTurnID})
	gotAbortTTSStart := false
	for !gotAbortTTSStart {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("canonical Gemini abort turn read failed: %v", err)
		}
		if kind == websocket.MessageBinary {
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == protocol.ProtocolErrorType {
			t.Fatalf("canonical Gemini abort turn protocol error: %s %s", message.Code, message.Message)
		}
		if message.Type == protocol.TTSLifecycleType && message.State == "start" {
			gotAbortTTSStart = true
		}
	}
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnAbortType, SessionID: hello.SessionID, TurnID: abortTurnID, Reason: "gemini_evidence_barge_in"})

	preMarkerBinaryFrames := 0
	gotInterruptedMarker := false
	markerDeadline := time.Now().Add(5 * time.Second)
	for !gotInterruptedMarker && time.Now().Before(markerDeadline) {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("canonical Gemini interruption marker read failed: %v", err)
		}
		if kind == websocket.MessageBinary {
			// These bytes may already have crossed the socket before the abort was
			// processed. The interrupted marker is the device's authoritative point
			// to discard buffered playback from the invalidated generation.
			preMarkerBinaryFrames++
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == protocol.ProtocolErrorType {
			t.Fatalf("canonical Gemini cancellation protocol error: %s %s", message.Code, message.Message)
		}
		if message.Type == protocol.TurnStateType && message.TurnID == abortTurnID && message.State == "interrupted" {
			gotInterruptedMarker = true
		}
	}
	if !gotInterruptedMarker {
		t.Fatal("canonical turn.abort produced no authoritative interrupted marker")
	}

	cancelDeadline := time.Now().Add(5 * time.Second)
	for bridge.cancelRequests.Load() == 0 && time.Now().Before(cancelDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if bridge.cancelRequests.Load() == 0 {
		t.Fatal("canonical turn.abort did not reach Gemini Live cancellation boundary")
	}

	postInterruptBinaryFrames := 0
	postMarkerCtx, postMarkerCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	for {
		kind, raw, readErr := connection.Read(postMarkerCtx)
		if readErr != nil {
			if postMarkerCtx.Err() != nil {
				break
			}
			postMarkerCancel()
			t.Fatalf("canonical Gemini post-interrupt read failed: %v", readErr)
		}
		if kind == websocket.MessageBinary {
			postInterruptBinaryFrames++
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			postMarkerCancel()
			t.Fatal(err)
		}
		if message.Type == protocol.ProtocolErrorType {
			postMarkerCancel()
			t.Fatalf("canonical Gemini post-interrupt protocol error: %s %s", message.Code, message.Message)
		}
	}
	postMarkerCancel()
	if postInterruptBinaryFrames != 0 {
		t.Fatalf("canonical generation gate leaked %d stale binary frames after interrupted marker", postInterruptBinaryFrames)
	}

	evidence := map[string]any{
		"schema_version": "companion.voice.gemini-canonical-evidence.v1",
		"source_commit":  strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_SOURCE_SHA")),
		"evidence_class": "real_provider_recorded_fixture_canonical_native_realtime_session",
		"provider": map[string]any{
			"name":  "Google Gemini Developer API Live",
			"model": model,
			"voice": voice,
		},
		"canonical_path": []string{
			"authenticated /v2/device",
			"Opus uplink decode",
			"test-only Gemini canonical bridge",
			"real Gemini Live native audio input + final input transcription",
			"Gemini companion_delegate function call",
			"Companion ADK runtime",
			"Companion ToolRegistry",
			"deterministic local Chat Completions fixture",
			"Gemini scalar tool response",
			"real Gemini Live native audio output",
			"fixed Protocol v2 PCM framing",
			"production generation gate",
			"Opus downlink",
		},
		"input_pcm": map[string]any{
			"path":                    pcmPath,
			"used_bytes":              len(pcm),
			"used_duration_ms":        float64(len(pcm)) / float64(protocol.UplinkSampleRate*2) * 1000,
			"trimmed_to_protocol_max": trimmed,
		},
		"normal_turn": map[string]any{
			"transcript":                transcript,
			"tool_executions":           normalTools,
			"provider_delegate_calls":   1,
			"tts_binary_frames":         binaryFrames,
			"after_listen_stop_ms":      normalTurnMS,
			"provider_output_pcm_bytes": bridge.outputPCMBytes.Load(),
		},
		"cancellation": map[string]any{
			"canonical_turn_abort_sent":    true,
			"provider_cancel_requests":     bridge.cancelRequests.Load(),
			"server_reason":                "gemini_evidence_barge_in",
			"interruption_marker_received": gotInterruptedMarker,
			"pre_marker_binary_frames":     preMarkerBinaryFrames,
			"post_interrupt_binary_frames": postInterruptBinaryFrames,
		},
		"limitations": []string{
			"The Gemini bridge exists only in this evidence test. #105 does not add a second production runtime path; #106 owns any provider selection and hard-cut.",
			"Gemini performs native audio input/transcription and native audio output, while Companion ADK and ToolRegistry remain authoritative for business tools through the mandatory delegation boundary.",
			"Provider/network audio that was already in flight before the interrupted marker is recorded separately. The canonical safety assertion is zero old-generation binary output after that marker.",
			"The ADK LLM transport is a deterministic local fixture, so this artifact proves orchestration and speech transport rather than external LLM quality.",
			"Recorded input does not prove physical microphone, enclosure, AEC, WakeNet or speaker quality.",
			"SQLite is used only as this isolated test's conversation Store implementation; it is not a product persistence path.",
		},
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Gemini turn transcript=%q frames=%d tool_calls=%d delegate_calls=%d duration_ms=%.1f cancel_requests=%d pre_marker_frames=%d post_interrupt_frames=%d", transcript, binaryFrames, normalTools, bridge.delegateCalls.Load(), normalTurnMS, bridge.cancelRequests.Load(), preMarkerBinaryFrames, postInterruptBinaryFrames)
}

type batchOnlyAgent struct {
	delegate pipeline.Agent
}

func (a batchOnlyAgent) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	return a.delegate.Respond(ctx, turnID, transcript)
}

type geminiCanonicalBridge struct {
	provider speech.NativeRealtimeProvider

	mu      sync.Mutex
	session speech.NativeRealtimeSession
	call    *speech.NativeRealtimeToolCall

	delegateCalls  atomic.Int32
	cancelRequests atomic.Int32
	outputPCMBytes atomic.Int64
}

func (b *geminiCanonicalBridge) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	b.mu.Lock()
	if b.session != nil {
		_ = b.session.Close()
		b.session = nil
		b.call = nil
	}
	b.mu.Unlock()

	session, err := b.provider.Connect(ctx)
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = session.Close()
		}
	}()
	const chunk = 1280
	for offset := 0; offset < len(pcm); offset += chunk {
		end := offset + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := session.AppendAudio(ctx, pcm[offset:end]); err != nil {
			return "", err
		}
	}
	if err := session.CommitAudio(ctx); err != nil {
		return "", err
	}
	if err := session.CreateResponse(ctx); err != nil {
		return "", err
	}

	var transcript string
	var call *speech.NativeRealtimeToolCall
	for strings.TrimSpace(transcript) == "" || call == nil {
		event, err := session.Receive(ctx)
		if err != nil {
			return "", err
		}
		if event.InputFinal && strings.TrimSpace(event.InputTranscript) != "" {
			transcript = strings.TrimSpace(event.InputTranscript)
		}
		if event.ToolCall != nil {
			copyCall := *event.ToolCall
			call = &copyCall
			b.delegateCalls.Add(1)
		}
		if len(event.AudioPCM) > 0 && call == nil {
			return "", fmt.Errorf("Gemini emitted audio before Companion delegation")
		}
		if event.ResponseDone && call == nil {
			return "", fmt.Errorf("Gemini completed without Companion delegation")
		}
	}
	b.mu.Lock()
	b.session = session
	b.call = call
	b.mu.Unlock()
	cleanup = false
	return transcript, nil
}

func (b *geminiCanonicalBridge) Synthesize(ctx context.Context, text string, emit func([]byte) error) error {
	b.mu.Lock()
	session := b.session
	call := b.call
	b.mu.Unlock()
	if session == nil || call == nil {
		return fmt.Errorf("Gemini canonical bridge has no pending delegated turn")
	}
	// Google's Live WebSocket examples use response.result as the function's
	// direct result value. Keep the canonical speech handoff scalar so Gemini
	// has one unambiguous string to speak after Companion finishes ADK/tools.
	if err := session.ReturnToolResult(ctx, call.CallID, text); err != nil {
		return err
	}

	frameBytes := protocol.DownlinkSamplesPerFrame * 2
	buffer := make([]byte, 0, frameBytes*2)
	providerPCMBytes := 0
	closeSession := func() {
		b.mu.Lock()
		if b.session == session {
			b.session = nil
			b.call = nil
		}
		b.mu.Unlock()
		_ = session.Close()
	}
	defer closeSession()

	for {
		event, err := session.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				b.cancelProvider(session)
				return ctx.Err()
			}
			return err
		}
		if len(event.AudioPCM) > 0 {
			providerPCMBytes += len(event.AudioPCM)
			b.outputPCMBytes.Add(int64(len(event.AudioPCM)))
			buffer = append(buffer, event.AudioPCM...)
			for len(buffer) >= frameBytes {
				frame := append([]byte(nil), buffer[:frameBytes]...)
				buffer = buffer[frameBytes:]
				if err := emit(frame); err != nil {
					if ctx.Err() != nil {
						b.cancelProvider(session)
					}
					return err
				}
			}
		}
		if event.ResponseDone {
			if event.ResponseStatus == "cancelled" {
				return context.Canceled
			}
			if providerPCMBytes == 0 {
				return fmt.Errorf("Gemini completed delegated response without native audio")
			}
			if len(buffer) > 0 {
				frame := make([]byte, frameBytes)
				copy(frame, buffer)
				if err := emit(frame); err != nil {
					return err
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			b.cancelProvider(session)
			return ctx.Err()
		default:
		}
	}
}

func (b *geminiCanonicalBridge) cancelProvider(session speech.NativeRealtimeSession) {
	b.cancelRequests.Add(1)
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = session.CancelResponse(cancelCtx)
}

var _ pipeline.ASR = (*geminiCanonicalBridge)(nil)
var _ pipeline.TTS = (*geminiCanonicalBridge)(nil)
var _ pipeline.Agent = batchOnlyAgent{}

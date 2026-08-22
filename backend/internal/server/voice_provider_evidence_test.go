//go:build adk

package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	opus "gopkg.in/hraban/opus.v2"
)

// TestVoiceEvidenceCanonicalRealCascade is opt-in because it intentionally uses
// the real FunASR and EdgeTTS provider boundaries. It proves the recorded-audio
// cascade travels through the canonical authenticated /v2/device session, the
// real ADK runtime and ToolRegistry before real TTS audio is returned. The LLM
// transport itself is a deterministic local fixture, so this test is evidence
// for Companion orchestration + real speech-provider integration, not LLM
// provider quality.
func TestVoiceEvidenceCanonicalRealCascade(t *testing.T) {
	if os.Getenv("COMPANION_VOICE_EVIDENCE") != "1" {
		t.Skip("real provider evidence is opt-in")
	}
	pcmPath := strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_PCM"))
	reportPath := strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_CANONICAL_REPORT"))
	funBase := strings.TrimSpace(os.Getenv("FUNASR_BASE_URL"))
	funModel := strings.TrimSpace(os.Getenv("FUNASR_MODEL"))
	if pcmPath == "" || reportPath == "" || funBase == "" || funModel == "" {
		t.Fatalf("VOICE_EVIDENCE_PCM, VOICE_EVIDENCE_CANONICAL_REPORT, FUNASR_BASE_URL and FUNASR_MODEL are required")
	}
	pcm, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		t.Fatalf("invalid PCM fixture length %d", len(pcm))
	}

	// Protocol v2 has an eight-second bounded audio turn, while the production
	// uplink encodes fixed 60 ms Opus frames. Use the largest whole-frame prefix
	// that remains inside the production bound so a padded final partial frame
	// cannot decode past MaximumAudioSecs.
	maxFrames := protocol.UplinkSampleRate * protocol.MaximumAudioSecs / protocol.UplinkSamplesPerFrame
	maxPCMBytes := maxFrames * protocol.UplinkSamplesPerFrame * 2
	trimmed := false
	if len(pcm) > maxPCMBytes {
		pcm = pcm[:maxPCMBytes]
		trimmed = true
	}

	funASR, err := speech.NewFunASR(speech.FunASRConfig{
		BaseURL:  funBase,
		Model:    funModel,
		Language: "vi",
	})
	if err != nil {
		t.Fatal(err)
	}
	voice := strings.TrimSpace(os.Getenv("EDGE_TTS_VOICE_VI"))
	if voice == "" {
		voice = "vi-VN-HoaiMyNeural"
	}
	edgeTTS, err := speech.NewEdgeTTS(speech.EdgeTTSConfig{Voice: voice})
	if err != nil {
		t.Fatal(err)
	}
	adapter := speech.PipelineAdapter{
		ASRProvider: funASR,
		TTSProvider: edgeTTS,
		ASRFormat:   speech.AudioFormat{SampleRate: protocol.UplinkSampleRate, Channels: 1},
		TTSFormat:   speech.AudioFormat{SampleRate: protocol.DownlinkSampleRate, Channels: 1},
		Locale:      "vi-VN",
		Voice:       voice,
	}
	if err := adapter.Validate(); err != nil {
		t.Fatal(err)
	}

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

	const fixtureReply = "Đã xong."
	llmRequests := atomic.Int32{}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requestNumber := llmRequests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode deterministic LLM request: %v", err)
			return
		}
		if body["stream"] != true {
			t.Errorf("canonical ADK request was not streaming: %#v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requestNumber == 1 {
			fmt.Fprint(w, "data: {\"id\":\"canonical-1\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-evidence-1\",\"type\":\"function\",\"function\":{\"name\":\"benchmark_echo\",\"arguments\":\"{\\\"value\\\":\\\"canonical-session\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprintf(w, "data: {\"id\":\"canonical-2\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", fixtureReply)
		fmt.Fprint(w, "data: {\"id\":\"canonical-2\",\"model\":\"evidence-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	data, err := store.Open(filepath.Join(t.TempDir(), "voice-evidence.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	conversation := conversationctx.New(voiceEvidenceConversationStore{data: data}, conversationctx.NewMemoryCache(10*time.Minute, 4))
	agent, err := adkbridge.New(adkbridge.Config{
		AppName:       "companion-voice-evidence",
		ModelName:     "evidence-model",
		ModelProtocol: adkbridge.ModelProtocolChatCompletions,
		BaseURL:       llm.URL,
		APIKey:        "fixture-key",
		Instruction:   "Use benchmark_echo once, then answer with a short Vietnamese confirmation.",
		PromptVersion: "voice-evidence@1",
		HTTPClient:    llm.Client(),
		Tools:         registry,
		Conversation:  conversation,
		HistoryLimit:  4,
	})
	if err != nil {
		t.Fatal(err)
	}

	service := newAuthenticatedTestServer(pipeline.Components{
		ASR:    adapter,
		Agent:  agent,
		TTS:    adapter,
		Codecs: pipeline.OpusFactory{},
	}, WithStore(data))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("device-voice-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "voice evidence done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, connection)
	if hello.Type != protocol.SessionReadyType || hello.SessionID == "" {
		t.Fatalf("invalid session.ready: %+v", hello)
	}
	turnID := "turn-real-provider-evidence"
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
	var generation uint64
	for !gotTTSStop {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("canonical read failed: %v transcript=%q llm_requests=%d tool_executions=%d tts_start=%v binary_frames=%d", err, transcript, llmRequests.Load(), toolExecutions.Load(), gotTTSStart, binaryFrames)
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
			t.Fatalf("canonical session returned protocol error: %s %s", message.Code, message.Message)
		}
		if message.GenerationID > generation {
			generation = message.GenerationID
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
	turnMS := float64(time.Since(turnStarted).Microseconds()) / 1000
	if transcript == "" {
		t.Fatal("real FunASR produced no canonical transcript")
	}
	if toolExecutions.Load() != 1 {
		t.Fatalf("ToolRegistry executions=%d, want 1", toolExecutions.Load())
	}
	if llmRequests.Load() < 2 {
		t.Fatalf("ADK Chat Completions requests=%d, want at least 2", llmRequests.Load())
	}
	if !gotTTSStart || binaryFrames == 0 {
		t.Fatalf("real TTS stream incomplete: start=%v binary_frames=%d", gotTTSStart, binaryFrames)
	}

	evidence := map[string]any{
		"schema_version": "companion.voice.canonical-session-evidence.v1",
		"source_commit":  strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_SOURCE_SHA")),
		"evidence_class": "real_provider_recorded_fixture_canonical_session",
		"canonical_path": []string{
			"authenticated /v2/device",
			"Opus uplink decode",
			"speech.PipelineAdapter",
			"real FunASR",
			"ADK runtime",
			"ToolRegistry",
			"deterministic local Chat Completions fixture",
			"real EdgeTTS",
			"Opus downlink",
		},
		"input_pcm": map[string]any{
			"path":                    pcmPath,
			"used_bytes":              len(pcm),
			"used_duration_ms":        float64(len(pcm)) / float64(protocol.UplinkSampleRate*2) * 1000,
			"trimmed_to_protocol_max": trimmed,
		},
		"llm_fixture_response":       fixtureReply,
		"transcript":                 transcript,
		"tool_executions":            toolExecutions.Load(),
		"llm_fixture_requests":        llmRequests.Load(),
		"tts_binary_frames":           binaryFrames,
		"generation_id":               generation,
		"turn_after_listen_stop_ms":   turnMS,
		"limitations": []string{
			"Speech providers are real; the LLM transport is a deterministic local fixture so this artifact does not claim LLM provider quality.",
			"The canonical LLM fixture intentionally uses a short spoken reply. This artifact proves one bounded real-provider session path; it does not claim sustained EdgeTTS media-backpressure behavior for longer utterances. Provider-lane chunk/timing data remains the evidence for EdgeTTS burst characteristics.",
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
	t.Logf("canonical real-provider turn transcript=%q frames=%d tool_calls=%d duration_ms=%.1f", transcript, binaryFrames, toolExecutions.Load(), turnMS)
}

func writeEvidencePCM(ctx context.Context, connection *websocket.Conn, pcm []byte) error {
	encoder, err := opus.NewEncoder(protocol.UplinkSampleRate, 1, opus.AppVoIP)
	if err != nil {
		return err
	}
	packet := make([]byte, protocol.MaximumOpusPacketBytes)
	for offset := 0; offset < len(pcm); offset += protocol.UplinkSamplesPerFrame * 2 {
		end := offset + protocol.UplinkSamplesPerFrame*2
		if end > len(pcm) {
			end = len(pcm)
		}
		samples := make([]int16, protocol.UplinkSamplesPerFrame)
		for i := 0; offset+i*2+1 < end; i++ {
			samples[i] = int16(binary.LittleEndian.Uint16(pcm[offset+i*2 : offset+i*2+2]))
		}
		n, err := encoder.Encode(samples, packet)
		if err != nil {
			return err
		}
		if err := connection.Write(ctx, websocket.MessageBinary, packet[:n]); err != nil {
			return err
		}
	}
	return nil
}

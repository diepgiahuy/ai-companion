//go:build adk

package server

import (
	"context"
	"encoding/json"
	"errors"
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
)

type injectedFaultResult struct {
	EvidenceClass       string `json:"evidence_class"`
	Fault               string `json:"fault"`
	ProviderRequests    int32  `json:"provider_requests"`
	RetriesObserved     int32  `json:"retries_observed"`
	ProtocolErrorCode   string `json:"protocol_error_code"`
	AgentRequests       int32  `json:"agent_requests"`
	ToolExecutions      int32  `json:"tool_executions"`
	TTSSyntheses        int32  `json:"tts_syntheses"`
	StaleBinaryFrames   int    `json:"stale_binary_frames"`
	Handled             bool   `json:"handled"`
}

type faultTrackingTTS struct{ calls *atomic.Int32 }

func (p faultTrackingTTS) Synthesize(ctx context.Context, _ speech.TTSRequest, _ func(speech.AudioEvent) error) error {
	p.calls.Add(1)
	return errors.New("injected-fault oracle unexpectedly reached TTS")
}

// TestVoiceEvidenceCanonicalFaultSemantics exercises #105 S5 through the same
// authenticated Protocol-v2 session and production FunASR adapter used by the
// real cascade. Faults are injected locally and are intentionally classified
// separately from provider-quality evidence.
func TestVoiceEvidenceCanonicalFaultSemantics(t *testing.T) {
	var results []injectedFaultResult

	t.Run("rate_limit", func(t *testing.T) {
		var requests atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			http.Error(w, "injected rate limit", http.StatusTooManyRequests)
		}))
		defer upstream.Close()

		provider, err := speech.NewFunASR(speech.FunASRConfig{
			BaseURL: upstream.URL,
			Model:   "injected-fault-model",
			Language: "vi",
			HTTPClient: upstream.Client(),
		})
		if err != nil {
			t.Fatal(err)
		}
		result := runCanonicalASRFaultProbe(t, "rate_limit", provider, &requests)
		if result.ProviderRequests != 1 || result.RetriesObserved != 0 {
			t.Fatalf("rate-limit requests=%d retries=%d, want one request and zero hidden retries", result.ProviderRequests, result.RetriesObserved)
		}
		results = append(results, result)
	})

	t.Run("timeout", func(t *testing.T) {
		var requests atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			time.Sleep(150 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"too late"}`))
		}))
		defer upstream.Close()

		provider, err := speech.NewFunASR(speech.FunASRConfig{
			BaseURL: upstream.URL,
			Model:   "injected-fault-model",
			Language: "vi",
			HTTPClient: &http.Client{Timeout: 25 * time.Millisecond},
		})
		if err != nil {
			t.Fatal(err)
		}
		result := runCanonicalASRFaultProbe(t, "timeout", provider, &requests)
		if result.ProviderRequests != 1 || result.RetriesObserved != 0 {
			t.Fatalf("timeout requests=%d retries=%d, want one request and zero hidden retries", result.ProviderRequests, result.RetriesObserved)
		}
		results = append(results, result)
	})

	for _, result := range results {
		if !result.Handled || result.ProtocolErrorCode != "asr_failed" {
			t.Fatalf("fault %s was not surfaced as bounded canonical asr_failed: %+v", result.Fault, result)
		}
		if result.AgentRequests != 0 || result.ToolExecutions != 0 || result.TTSSyntheses != 0 || result.StaleBinaryFrames != 0 {
			t.Fatalf("fault %s escaped ASR failure boundary: %+v", result.Fault, result)
		}
	}

	if path := strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_FAULT_REPORT")); path != "" {
		report := map[string]any{
			"schema_version": "companion.voice.injected-failure-evidence.v1",
			"source_commit": strings.TrimSpace(os.Getenv("VOICE_EVIDENCE_SOURCE_SHA")),
			"evidence_class": "injected_failure_canonical_session",
			"probes": results,
			"summary": map[string]any{
				"timeout_probe_attempted": true,
				"timeout_handled": true,
				"rate_limit_probe_attempted": true,
				"rate_limit_handled": true,
				"provider_errors_observed": len(results),
				"retries_observed": 0,
				"retry_policy": "no implicit FunASR retry in current production adapter",
			},
			"limitations": []string{
				"429 and timeout are injected locally through the production FunASR adapter and authenticated Companion session; they are failure-semantics evidence, not real-provider reliability-rate evidence.",
				"The current FunASR adapter performs no implicit retry, so retries_observed=0 is reported rather than fabricating retry success.",
			},
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runCanonicalASRFaultProbe(t *testing.T, fault string, asr speech.StreamingASRProvider, providerRequests *atomic.Int32) injectedFaultResult {
	t.Helper()
	var agentRequests atomic.Int32
	var toolExecutions atomic.Int32
	var ttsCalls atomic.Int32

	registry := capability.NewToolRegistry()
	definition := &capability.ToolDefinition{
		Name: "benchmark_fault_tool", Description: "Must not execute after ASR failure.", Pack: "benchmark", Risk: "read",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
	if err := registry.Register(capability.FunctionTool{
		ToolName: definition.Name, ToolDefinition: definition,
		Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
			toolExecutions.Add(1)
			return capability.Success(map[string]any{"unexpected": true})
		},
	}); err != nil {
		t.Fatal(err)
	}

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		agentRequests.Add(1)
		http.Error(w, "agent must not be reached after ASR failure", http.StatusInternalServerError)
	}))
	defer llm.Close()

	data, err := store.Open(filepath.Join(t.TempDir(), "fault.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	conversation := conversationctx.New(voiceEvidenceConversationStore{data: data}, conversationctx.NewMemoryCache(time.Minute, 2))
	agent, err := adkbridge.New(adkbridge.Config{
		AppName: "companion-voice-fault-evidence", ModelName: "fault-fixture", ModelProtocol: adkbridge.ModelProtocolChatCompletions,
		BaseURL: llm.URL, APIKey: "fixture-key", Instruction: "Do not run in this fault probe.", PromptVersion: "voice-fault-evidence@1",
		HTTPClient: llm.Client(), Tools: registry, Conversation: conversation, HistoryLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	adapter := speech.PipelineAdapter{
		ASRProvider: asr,
		TTSProvider: faultTrackingTTS{calls: &ttsCalls},
		ASRFormat: speech.AudioFormat{SampleRate: protocol.UplinkSampleRate, Channels: 1},
		TTSFormat: speech.AudioFormat{SampleRate: protocol.DownlinkSampleRate, Channels: 1},
		Locale: "vi-VN",
	}
	if err := adapter.Validate(); err != nil {
		t.Fatal(err)
	}
	service := newAuthenticatedTestServer(pipeline.Components{ASR: adapter, Agent: agent, TTS: adapter, Codecs: pipeline.OpusFactory{}}, WithStore(data))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("device-fault-"+fault))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, connection)
	if hello.Type != protocol.SessionReadyType {
		t.Fatalf("fault probe session ready=%+v", hello)
	}
	turnID := "turn-fault-" + fault
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID})
	if err := writeEvidencePCM(ctx, connection, make([]byte, protocol.UplinkSamplesPerFrame*2)); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: turnID})

	result := injectedFaultResult{EvidenceClass: "injected_failure_canonical_session", Fault: fault}
	for {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("read fault %s result: %v", fault, err)
		}
		if kind == websocket.MessageBinary {
			result.StaleBinaryFrames++
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == protocol.TranscriptFinalType || message.Type == protocol.TTSLifecycleType || message.Type == protocol.AgentStatusType {
			t.Fatalf("fault %s emitted post-ASR-success output %+v", fault, message)
		}
		if message.Type == protocol.ProtocolErrorType {
			result.ProtocolErrorCode = message.Code
			result.Handled = message.Code == "asr_failed"
			break
		}
	}
	result.ProviderRequests = providerRequests.Load()
	if result.ProviderRequests > 0 {
		result.RetriesObserved = result.ProviderRequests - 1
	}
	result.AgentRequests = agentRequests.Load()
	result.ToolExecutions = toolExecutions.Load()
	result.TTSSyntheses = ttsCalls.Load()
	return result
}

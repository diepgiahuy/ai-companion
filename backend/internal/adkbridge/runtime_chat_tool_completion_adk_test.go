//go:build adk

package adkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
)

func TestRuntimeChatCompletionsToolTurnTerminatesAfterDone(t *testing.T) {
	registry := capability.NewToolRegistry()
	definition := &capability.ToolDefinition{
		Name: "benchmark_echo", Description: "fixture", Pack: "test", Risk: "read",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required": []string{"value"},
			"additionalProperties": false,
		},
	}
	var toolExecutions atomic.Int32
	if err := registry.Register(capability.FunctionTool{
		ToolName: definition.Name, ToolDefinition: definition,
		Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
			toolExecutions.Add(1)
			return capability.Success(map[string]any{"ok": true})
		},
	}); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		request := requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if body["stream"] != true {
			t.Errorf("stream=%#v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if request == 1 {
			fmt.Fprint(w, "data: {\"id\":\"r1\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"benchmark_echo\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"r2\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Da xong.\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"r2\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer fixture.Close()

	store := &testConversationStore{}
	runtime, err := New(Config{
		AppName: "chat-tool-completion-test",
		ModelName: "fixture",
		ModelProtocol: ModelProtocolChatCompletions,
		BaseURL: fixture.URL,
		APIKey: "fixture-key",
		Instruction: "Call benchmark_echo once, then answer.",
		PromptVersion: "chat-tool-completion@1",
		HTTPClient: fixture.Client(),
		Tools: registry,
		Conversation: conversationctx.New(store, nil),
		HistoryLimit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = pipeline.WithTurnContext(ctx, pipeline.TurnContext{UserID: "u1", ThreadID: "default", DeviceID: "d1", SessionID: "s1", TurnID: "t1"})
	var text string
	if err := runtime.Stream(ctx, "t1", "hello", func(event pipeline.AgentStreamEvent) error {
		text += event.TextDelta
		return nil
	}); err != nil {
		t.Fatalf("chat-completions stream did not terminate: %v requests=%d tools=%d text=%q", err, requests.Load(), toolExecutions.Load(), text)
	}
	if requests.Load() != 2 || toolExecutions.Load() != 1 || text != "Da xong." {
		t.Fatalf("requests=%d tools=%d text=%q", requests.Load(), toolExecutions.Load(), text)
	}
	if len(store.messages) != 2 || store.messages[1].Content != "Da xong." {
		t.Fatalf("durable conversation=%#v", store.messages)
	}
}

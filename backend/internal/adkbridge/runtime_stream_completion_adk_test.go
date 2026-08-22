//go:build adk

package adkbridge

import (
	"context"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type streamCompletionLLM struct {
	requests atomic.Int32
}

func (m *streamCompletionLLM) Name() string { return "stream-completion" }

func (m *streamCompletionLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	request := m.requests.Add(1)
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			yield(&model.LLMResponse{ErrorCode: "fixture", ErrorMessage: "expected streaming request"}, nil)
			return
		}
		if request == 1 {
			yield(&model.LLMResponse{
			Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
				ID: "call-stream-completion", Name: "benchmark_echo", Args: map[string]any{"value": "ok"},
			}}}},
			Partial: true,
		}, nil)
			yield(&model.LLMResponse{
			Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
				ID: "call-stream-completion", Name: "benchmark_echo", Args: map[string]any{"value": "ok"},
			}}}},
			Partial:      false,
			TurnComplete: true,
			FinishReason: genai.FinishReasonOther,
		}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "Da xong."}}},
			Partial: true,
		}, nil)
		yield(&model.LLMResponse{
			Content:       &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "Da xong."}}},
			Partial:       false,
			TurnComplete:  true,
			FinishReason:  genai.FinishReasonStop,
		}, nil)
	}
}

func TestRuntimeStreamToolTurnTerminatesAfterFinalAnswer(t *testing.T) {
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
	store := &testConversationStore{}
	llm := &streamCompletionLLM{}
	runtime, err := newWithModel(Config{
		AppName: "stream-completion-test",
		Tools: registry,
		Conversation: conversationctx.New(store, nil),
		Instruction: "Call benchmark_echo once, then answer.",
		PromptVersion: "stream-completion@1#fixture",
	}, llm)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = pipeline.WithTurnContext(ctx, pipeline.TurnContext{
		UserID: "u1", ThreadID: "default", DeviceID: "d1", SessionID: "s1", TurnID: "t1",
	})
	var text string
	err = runtime.Stream(ctx, "t1", "hello", func(event pipeline.AgentStreamEvent) error {
		text += event.TextDelta
		return nil
	})
	if err != nil {
		t.Fatalf("stream did not terminate cleanly: %v requests=%d tools=%d text=%q", err, llm.requests.Load(), toolExecutions.Load(), text)
	}
	if llm.requests.Load() != 2 {
		t.Fatalf("LLM requests=%d, want 2", llm.requests.Load())
	}
	if toolExecutions.Load() != 1 {
		t.Fatalf("tool executions=%d, want 1", toolExecutions.Load())
	}
	if text != "Da xong." {
		t.Fatalf("stream text=%q, want %q", text, "Da xong.")
	}
	if len(store.messages) != 2 || store.messages[1].Role != "assistant" || store.messages[1].Content != "Da xong." {
		t.Fatalf("durable conversation=%#v", store.messages)
	}
}

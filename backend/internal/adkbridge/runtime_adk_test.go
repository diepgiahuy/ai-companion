//go:build adk

package adkbridge

import (
	"context"
	"errors"
	"iter"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	usagepkg "companion-server/internal/usage"
)

type fakeLLM struct{}

func (fakeLLM) Name() string { return "fake" }
func (fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func TestADKRepresentativeToolsReuseRegistrySchemas(t *testing.T) {
	reg := capability.NewToolRegistry()
	for _, name := range RepresentativeToolNames() {
		name := name
		def := capability.ToolDefinition{
			Name: name, Description: name, Pack: "test",
			Parameters: map[string]any{"type": "object", "additionalProperties": true},
		}
		if err := reg.Register(capability.FunctionTool{
			ToolName: name, ToolDefinition: &def,
			Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
				return capability.Success(map[string]any{"name": name})
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newWithModel(Config{
		AppName:       "test",
		Tools:         reg,
		Instruction:   "test instruction",
		PromptVersion: "test@1#fixture",
	}, fakeLLM{}); err != nil {
		t.Fatal(err)
	}
}

type usageLLM struct {
	called bool
}

func (m *usageLLM) Name() string { return "usage-fake" }
func (m *usageLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.called = true
		yield(&model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     12,
			CandidatesTokenCount: 5,
			TotalTokenCount:      17,
		}}, nil)
	}
}

type denyUsageGuard struct{}

func (denyUsageGuard) Check(context.Context, string) error { return errors.New("quota exceeded") }

func TestMeteredLLMRecordsOneUsageSnapshot(t *testing.T) {
	inner := &usageLLM{}
	meter := usagepkg.NewMemory()
	llm := &meteredLLM{inner: inner, modelName: "configured", promptVersion: "companion@4#abc", meter: meter}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u1", DeviceID: "d1"})
	for _, err := range llm.GenerateContent(ctx, &model.LLMRequest{Model: "selected"}, true) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !inner.called {
		t.Fatal("inner model was not called")
	}
	got := meter.Snapshot()
	if got["prompt_tokens"] != 12 || got["completion_tokens"] != 5 || got["total_tokens"] != 17 {
		t.Fatalf("unexpected usage: %#v", got)
	}
}

func TestMeteredLLMChecksQuotaBeforeProviderCall(t *testing.T) {
	inner := &usageLLM{}
	llm := &meteredLLM{inner: inner, guard: denyUsageGuard{}}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u1"})
	var gotErr error
	for _, err := range llm.GenerateContent(ctx, &model.LLMRequest{}, true) {
		gotErr = err
	}
	if gotErr == nil || inner.called {
		t.Fatalf("guard error=%v inner.called=%v", gotErr, inner.called)
	}
}

//go:build adk

package adkbridge

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	usagepkg "companion-server/internal/usage"
)

type fakeLLM struct{}

func (fakeLLM) Name() string { return "fake" }
func (fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

type testConversationStore struct {
	messages []conversationctx.Message
}

func (s *testConversationStore) Append(_ context.Context, _ string, _ conversationctx.Scope, role, content string) error {
	s.messages = append(s.messages, conversationctx.Message{Role: role, Content: content})
	return nil
}
func (s *testConversationStore) Recent(_ context.Context, _ conversationctx.Scope, limit int) ([]conversationctx.Message, error) {
	messages := s.messages
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return append([]conversationctx.Message(nil), messages...), nil
}
func (s *testConversationStore) Clear(context.Context, conversationctx.Scope) error {
	s.messages = nil
	return nil
}

func registerTestTools(t *testing.T, reg *capability.ToolRegistry, names ...string) {
	t.Helper()
	for _, name := range names {
		name := name
		def := capability.ToolDefinition{
			Name: name, Description: name, Pack: "test",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{"value": map[string]any{"type": "string"}},
			},
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
}

func TestADKExposesCompleteRegistryCatalogUsingRegistrySchemas(t *testing.T) {
	reg := capability.NewToolRegistry()
	names := []string{"expense.log", "budget.get", "timer.create", "memory.recall", "note.create"}
	registerTestTools(t, reg, names...)
	tools, err := buildRegistryTools(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != len(names) {
		t.Fatalf("ADK tool count=%d want=%d", len(tools), len(names))
	}
	conversation := conversationctx.New(&testConversationStore{}, nil)
	if _, err := newWithModel(Config{
		AppName:       "test",
		Tools:         reg,
		Conversation:  conversation,
		Instruction:   "test instruction",
		PromptVersion: "test@1#fixture",
	}, fakeLLM{}); err != nil {
		t.Fatal(err)
	}
}

func TestADKSessionRehydratesFromDurableConversation(t *testing.T) {
	reg := capability.NewToolRegistry()
	registerTestTools(t, reg, "note.create")
	store := &testConversationStore{messages: []conversationctx.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "xin chao"},
	}}
	runtime, err := newWithModel(Config{
		AppName:       "test",
		Tools:         reg,
		Conversation:  conversationctx.New(store, nil),
		Instruction:   "test instruction",
		PromptVersion: "test@1#fixture",
	}, fakeLLM{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := runtime.ensureSession(ctx, "user-hash", "session-hash", store.messages); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.sessions.Get(ctx, &session.GetRequest{AppName: "test", UserID: "user-hash", SessionID: "session-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Session == nil {
		t.Fatal("rehydrated ADK session missing")
	}
	if got.Session.Events().Len() != 2 {
		t.Fatalf("rehydrated events=%d want=2", got.Session.Events().Len())
	}
	if text := contentText(got.Session.Events().At(0).Content); text != "hello" {
		t.Fatalf("first hydrated event=%q", text)
	}
	if text := contentText(got.Session.Events().At(1).Content); text != "xin chao" {
		t.Fatalf("second hydrated event=%q", text)
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

func TestADKRequiresPromptBundleAndVersion(t *testing.T) {
	reg := capability.NewToolRegistry()
	registerTestTools(t, reg, "note.create")
	conversation := conversationctx.New(&testConversationStore{}, nil)

	// Missing instruction
	if _, err := newWithModel(Config{
		AppName:       "test",
		Tools:         reg,
		Conversation:  conversation,
		Instruction:   "",
		PromptVersion: "v1#abc",
	}, fakeLLM{}); err == nil || !strings.Contains(err.Error(), "ADK instruction must be supplied") {
		t.Fatalf("expected instruction required error, got %v", err)
	}

	// Missing prompt version
	if _, err := newWithModel(Config{
		AppName:       "test",
		Tools:         reg,
		Conversation:  conversation,
		Instruction:   "valid instruction",
		PromptVersion: "",
	}, fakeLLM{}); err == nil || !strings.Contains(err.Error(), "ADK prompt version/fingerprint is required") {
		t.Fatalf("expected prompt version required error, got %v", err)
	}
}

func TestADKRejectsEmptyToolRegistry(t *testing.T) {
	emptyReg := capability.NewToolRegistry()
	conversation := conversationctx.New(&testConversationStore{}, nil)

	if _, err := newWithModel(Config{
		AppName:       "test",
		Tools:         emptyReg,
		Conversation:  conversation,
		Instruction:   "valid instruction",
		PromptVersion: "v1#abc",
	}, fakeLLM{}); err == nil || !strings.Contains(err.Error(), "tool registry is empty") {
		t.Fatalf("expected empty tool registry error, got %v", err)
	}
}


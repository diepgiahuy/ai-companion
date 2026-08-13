//go:build adk

package adkbridge

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"companion-server/internal/capability"
	conversationpkg "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	usagepkg "companion-server/internal/usage"
)

type fakeLLM struct{}

func (fakeLLM) Name() string { return "fake" }
func (fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

type fakeConversationHistory struct {
	mu       sync.Mutex
	messages map[string][]conversationpkg.Message
	seenKeys map[string]struct{}
}

func newFakeConversationHistory() *fakeConversationHistory {
	return &fakeConversationHistory{messages: make(map[string][]conversationpkg.Message), seenKeys: make(map[string]struct{})}
}

func (h *fakeConversationHistory) Append(_ context.Context, key string, scope conversationpkg.Scope, role, content string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.seenKeys[key]; ok {
		return nil
	}
	h.seenKeys[key] = struct{}{}
	h.messages[scope.Key()] = append(h.messages[scope.Key()], conversationpkg.Message{Role: role, Content: content, CreatedAt: time.Now().UTC()})
	return nil
}

func (h *fakeConversationHistory) Recent(_ context.Context, scope conversationpkg.Scope, limit int) ([]conversationpkg.Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages := append([]conversationpkg.Message(nil), h.messages[scope.Key()]...)
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages, nil
}

func validTestConfig(reg *capability.ToolRegistry, history ConversationHistory) Config {
	return Config{
		AppName: "test", Tools: reg, Conversation: history,
		Instruction: "test instruction", PromptVersion: "test@1#fixture",
		Temperature: 0.1, MaxOutputTokens: 384, MaxToolRounds: 3,
	}
}

func TestADKExposesEveryRegisteredHostTool(t *testing.T) {
	reg := capability.NewToolRegistry()
	for _, name := range []string{"expense.create", "note.create", "timer.create"} {
		name := name
		def := capability.ToolDefinition{Name: name, Description: name, Pack: "test", Parameters: map[string]any{"type": "object", "additionalProperties": true}}
		if err := reg.Register(capability.FunctionTool{ToolName: name, ToolDefinition: &def, Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
			return capability.Success(map[string]any{"name": name})
		}}); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := buildHostTools(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != len(reg.Definitions()) {
		t.Fatalf("ADK tools=%d registry=%d", len(tools), len(reg.Definitions()))
	}
	if _, err := newWithModel(validTestConfig(reg, newFakeConversationHistory()), fakeLLM{}); err != nil {
		t.Fatal(err)
	}
}

func TestADKRequiresDurableConversationHistory(t *testing.T) {
	reg := capability.NewToolRegistry()
	def := capability.ToolDefinition{Name: "test.tool", Description: "test", Pack: "test", Parameters: map[string]any{"type": "object"}}
	if err := reg.Register(capability.FunctionTool{ToolName: def.Name, ToolDefinition: &def, Handler: func(context.Context, capability.ToolRequest) capability.ToolResult {
		return capability.Success(map[string]any{"ok": true})
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newWithModel(validTestConfig(reg, nil), fakeLLM{}); err == nil {
		t.Fatal("expected missing durable conversation history to fail closed")
	}
}

func TestSessionIdentitySurvivesTransportReconnect(t *testing.T) {
	base := pipeline.TurnContext{UserID: "u1", ThreadID: "thread-1", DeviceID: "d1", SessionID: "socket-a"}
	reconnected := base
	reconnected.SessionID = "socket-b"
	u1, s1 := SessionIdentity(base)
	u2, s2 := SessionIdentity(reconnected)
	if u1 != u2 || s1 != s2 {
		t.Fatalf("transport reconnect changed ADK identity: %q/%q != %q/%q", u1, s1, u2, s2)
	}
}

type limiterContext struct {
	adkagent.StrictContextMock
	invocationID string
}
func (c limiterContext) InvocationID() string { return c.invocationID }

func TestModelRoundLimiterMatchesConfiguredBound(t *testing.T) {
	limiter := newModelRoundLimiter(3)
	ctx := limiterContext{invocationID: "inv-1"}
	for i := 0; i < 3; i++ {
		if _, err := limiter.BeforeModel(ctx, nil); err != nil {
			t.Fatalf("allowed model round %d failed: %v", i+1, err)
		}
	}
	if _, err := limiter.BeforeModel(ctx, nil); err == nil {
		t.Fatal("expected fourth model round to fail")
	}
	if _, err := limiter.BeforeModel(ctx, nil); err != nil {
		t.Fatalf("limiter did not reset after fail-closed bound: %v", err)
	}
	_, _ = limiter.AfterAgent(ctx)
}

func TestCompanionSessionServiceRecoversDurableHistoryAfterRestart(t *testing.T) {
	ctx := context.Background()
	history := newFakeConversationHistory()
	scope := conversationpkg.Scope{UserID: "owner", ThreadID: "thread"}
	_ = history.Append(ctx, "seed-user", scope, "user", "hello")
	_ = history.Append(ctx, "seed-assistant", scope, "assistant", "xin chao")

	service1, err := newCompanionSessionService(history)
	if err != nil {
		t.Fatal(err)
	}
	service1.Bind("companion", "user-hash", "thread-hash", scope)
	created, err := service1.Create(ctx, &session.CreateRequest{AppName: "companion", UserID: "user-hash", SessionID: "thread-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Session.Events().Len(); got != 2 {
		t.Fatalf("bootstrapped events=%d want=2", got)
	}

	userEvent := session.NewEvent(ctx, "turn-2")
	userEvent.Author = "user"
	userEvent.LLMResponse = model.LLMResponse{Content: &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "second turn"}}}}
	if err := service1.AppendEvent(ctx, created.Session, userEvent); err != nil {
		t.Fatal(err)
	}

	service2, err := newCompanionSessionService(history)
	if err != nil {
		t.Fatal(err)
	}
	service2.Bind("companion", "user-hash", "thread-hash", scope)
	recovered, err := service2.Create(ctx, &session.CreateRequest{AppName: "companion", UserID: "user-hash", SessionID: "thread-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Session.Events().Len(); got != 3 {
		t.Fatalf("recovered events=%d want=3", got)
	}
}

type usageLLM struct{ called bool }

func (m *usageLLM) Name() string { return "usage-fake" }
func (m *usageLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.called = true
		yield(&model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 12, CandidatesTokenCount: 5, TotalTokenCount: 17}}, nil)
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
		if err != nil { t.Fatal(err) }
	}
	if !inner.called { t.Fatal("inner model was not called") }
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
	for _, err := range llm.GenerateContent(ctx, &model.LLMRequest{}, true) { gotErr = err }
	if gotErr == nil || inner.called {
		t.Fatalf("guard error=%v inner.called=%v", gotErr, inner.called)
	}
}

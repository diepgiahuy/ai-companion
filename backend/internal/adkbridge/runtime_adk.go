//go:build adk

package adkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/genai"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
)

type Runtime struct {
	runner       *runner.Runner
	sessions     session.Service
	appName      string
	conversation *conversationctx.Service
	historyLimit int
}

func Enabled() bool { return true }

// New remains the stable product entrypoint. Provider transport selection is
// delegated to NewProvider so Responses and Chat Completions still share one
// ADK/ToolRegistry/conversation runtime instead of creating parallel agents.
func New(cfg Config) (pipeline.Agent, error) {
	return NewProvider(cfg)
}

func newWithModel(cfg Config, llm model.LLM) (*Runtime, error) {
	if llm == nil {
		return nil, fmt.Errorf("ADK model is required")
	}
	instruction := strings.TrimSpace(cfg.Instruction)
	if instruction == "" {
		return nil, fmt.Errorf("ADK instruction must be supplied by the versioned prompt bundle")
	}
	promptVersion := strings.TrimSpace(cfg.PromptVersion)
	if promptVersion == "" {
		return nil, fmt.Errorf("ADK prompt version/fingerprint is required")
	}
	llm = &meteredLLM{inner: llm, modelName: strings.TrimSpace(cfg.ModelName), promptVersion: promptVersion, guard: cfg.UsageGuard, meter: cfg.UsageMeter}
	if cfg.Tools == nil {
		return nil, fmt.Errorf("tool registry is required")
	}
	if cfg.Conversation == nil {
		return nil, fmt.Errorf("Companion conversation service is required")
	}
	tools, err := buildRegistryTools(cfg.Tools)
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("tool registry is empty")
	}
	instructionProvider := func(agentCtx adkagent.ReadonlyContext) (string, error) {
		base := instruction
		if turn, ok := pipeline.CurrentTurn(agentCtx); ok {
			loc := time.UTC
			if tz := strings.TrimSpace(turn.Timezone); tz != "" {
				if loaded, err := time.LoadLocation(tz); err == nil {
					loc = loaded
				}
			}
			now := time.Now().In(loc)
			timeContext := fmt.Sprintf("Current time: %s (%s).", now.Format(time.RFC3339), loc.String())
			if strings.TrimSpace(turn.Locale) != "" {
				timeContext += fmt.Sprintf(" Active locale: %s.", strings.TrimSpace(turn.Locale))
			}
			return base + "\n\n[Runtime Context]\n" + timeContext, nil
		}
		return base, nil
	}
	agent, err := llmagent.New(llmagent.Config{
		Name: "companion", Description: "Single-user voice companion coordinator", Model: llm,
		Instruction: instruction, InstructionProvider: instructionProvider, Tools: tools, Mode: llmagent.ModeChat,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK llm agent: %w", err)
	}
	appName := strings.TrimSpace(cfg.AppName)
	if appName == "" {
		appName = "companion"
	}
	historyLimit := cfg.HistoryLimit
	if historyLimit <= 0 {
		historyLimit = 12
	}
	sessions := session.InMemoryService()
	r, err := runner.New(runner.Config{AppName: appName, Agent: agent, SessionService: sessions})
	if err != nil {
		return nil, fmt.Errorf("create ADK runner: %w", err)
	}
	return &Runtime{runner: r, sessions: sessions, appName: appName, conversation: cfg.Conversation, historyLimit: historyLimit}, nil
}

func (r *Runtime) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	var b strings.Builder
	err := r.Stream(ctx, turnID, transcript, func(event pipeline.AgentStreamEvent) error {
		b.WriteString(event.TextDelta)
		return nil
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("ADK returned no speakable text")
	}
	return out, nil
}

func (r *Runtime) Stream(ctx context.Context, turnID, transcript string, emit func(pipeline.AgentStreamEvent) error) error {
	if r == nil || r.runner == nil || r.sessions == nil || r.conversation == nil {
		return fmt.Errorf("ADK runtime is not initialized")
	}
	if emit == nil {
		return fmt.Errorf("stream callback is required")
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return fmt.Errorf("transcript is required")
	}

	turn, _ := pipeline.CurrentTurn(ctx)
	if strings.TrimSpace(turn.TurnID) == "" {
		turn.TurnID = turnID
		ctx = pipeline.WithTurnContext(ctx, turn)
	}
	userID, sessionID := SessionIdentity(turn)
	conversationUserID := strings.TrimSpace(turn.UserID)
	if conversationUserID == "" {
		conversationUserID = strings.TrimSpace(turn.DeviceID)
	}
	threadID := strings.TrimSpace(turn.ThreadID)
	if threadID == "" {
		threadID = "default"
	}
	scope := conversationctx.Scope{UserID: conversationUserID, ThreadID: threadID}
	history, err := r.conversation.Recent(ctx, scope, r.historyLimit)
	if err != nil {
		return fmt.Errorf("load durable conversation: %w", err)
	}
	if err := r.ensureSession(ctx, userID, sessionID, history); err != nil {
		return err
	}
	turnKey := durableTurnKey(turn, turnID)
	if err := r.conversation.Append(ctx, turnKey, scope, "user", transcript); err != nil {
		return fmt.Errorf("persist user conversation: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	presentations := &presentationQueue{}
	ctx = capability.WithPresentationSink(ctx, presentations.Push)
	outcomes := &invocationOutcomeTracker{}
	ctx = withToolOutcomeSink(ctx, outcomes.RecordTool)

	msg := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{Text: transcript}}}
	tracker := &textDeltaTracker{}
	var finalText strings.Builder
	sentText := false
	for event, runErr := range r.runner.Run(ctx, userID, sessionID, msg, adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
		if runErr != nil {
			return fmt.Errorf("ADK run: %w", runErr)
		}
		if err := emitPresentations(presentations.Drain(), emit); err != nil {
			return err
		}
		if event == nil {
			continue
		}
		status, toolName, callIDs := eventStatus(event.Content)
		for _, callID := range callIDs {
			if callID != "" || !event.Partial {
				outcomes.RecordToolCall(callID)
			}
		}
		if status != "" {
			if err := emit(pipeline.AgentStreamEvent{Status: status, ToolName: toolName}); err != nil {
				return err
			}
		}
		candidate := contentText(event.Content)
		if delta := tracker.Delta(candidate, event.Partial); delta != "" {
			sentText = true
			outcomes.RecordSpeakableText()
			finalText.WriteString(delta)
			if err := emit(pipeline.AgentStreamEvent{TextDelta: delta}); err != nil {
				return err
			}
		}
	}
	if err := emitPresentations(presentations.Drain(), emit); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fallback, err := outcomes.Finalize(sentText)
	if err != nil {
		return err
	}
	if fallback != "" {
		finalText.WriteString(fallback)
		if err := emit(pipeline.AgentStreamEvent{TextDelta: fallback}); err != nil {
			return err
		}
	}
	assistantText := strings.TrimSpace(finalText.String())
	if assistantText != "" {
		if err := r.conversation.Append(ctx, turnKey, scope, "assistant", assistantText); err != nil {
			return fmt.Errorf("persist assistant conversation: %w", err)
		}
	}
	return nil
}

func (r *Runtime) ensureSession(ctx context.Context, userID, sessionID string, history []conversationctx.Message) error {
	listed, err := r.sessions.List(ctx, &session.ListRequest{AppName: r.appName, UserID: userID})
	if err != nil {
		return fmt.Errorf("list ADK sessions: %w", err)
	}
	for _, existing := range listed.Sessions {
		if existing != nil && existing.ID() == sessionID {
			return nil
		}
	}
	created, err := r.sessions.Create(ctx, &session.CreateRequest{AppName: r.appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("create ADK session: %w", err)
	}
	if created == nil || created.Session == nil {
		return fmt.Errorf("create ADK session returned no session")
	}
	for i, message := range history {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		role := genai.RoleUser
		author := "user"
		if strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			role = genai.RoleModel
			author = "companion"
		}
		event := session.NewEvent(ctx, fmt.Sprintf("hydrate-%d", i))
		event.Author = author
		event.Content = &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}}
		if err := r.sessions.AppendEvent(ctx, created.Session, event); err != nil {
			return fmt.Errorf("hydrate ADK session event %d: %w", i, err)
		}
	}
	return nil
}

func durableTurnKey(turn pipeline.TurnContext, fallbackTurnID string) string {
	userID := strings.TrimSpace(turn.UserID)
	if userID == "" {
		userID = strings.TrimSpace(turn.DeviceID)
	}
	threadID := strings.TrimSpace(turn.ThreadID)
	if threadID == "" {
		threadID = "default"
	}
	sessionID := strings.TrimSpace(turn.SessionID)
	if sessionID == "" {
		sessionID = "local"
	}
	turnID := strings.TrimSpace(turn.TurnID)
	if turnID == "" {
		turnID = strings.TrimSpace(fallbackTurnID)
	}
	return userID + ":" + threadID + ":" + strings.TrimSpace(turn.DeviceID) + ":" + sessionID + ":" + turnID
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func eventStatus(content *genai.Content) (string, string, []string) {
	if content == nil {
		return "", "", nil
	}
	var callIDs []string
	toolName := ""
	for _, part := range content.Parts {
		if part != nil && part.FunctionCall != nil {
			callIDs = append(callIDs, part.FunctionCall.ID)
			if toolName == "" {
				toolName = strings.TrimSpace(part.FunctionCall.Name)
			}
		}
	}
	if len(callIDs) > 0 {
		return "tool_running", toolName, callIDs
	}
	return "", "", nil
}

type presentationQueue struct {
	mu    sync.Mutex
	items []capability.Presentation
}

func (q *presentationQueue) Push(p capability.Presentation) {
	q.mu.Lock()
	q.items = append(q.items, p)
	q.mu.Unlock()
}

func (q *presentationQueue) Drain() []capability.Presentation {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := append([]capability.Presentation(nil), q.items...)
	q.items = q.items[:0]
	return out
}

func emitPresentations(ps []capability.Presentation, emit func(pipeline.AgentStreamEvent) error) error {
	for _, p := range ps {
		ui := &pipeline.UICard{Kind: p.Kind, Title: p.Title, Primary: p.Primary, Secondary: p.Secondary, Progress: p.Progress}
		if err := emit(pipeline.AgentStreamEvent{UI: ui}); err != nil {
			return err
		}
	}
	return nil
}

// buildRegistryTools adapts the complete current Companion ToolRegistry into
// ADK FunctionTools. ToolRegistry remains the source of truth for schema,
// authorization, idempotency and execution; this layer only translates calls.
func buildRegistryTools(registry *capability.ToolRegistry) ([]tool.Tool, error) {
	definitions := registry.Definitions()
	executor := HostToolExecutor{Registry: registry}
	out := make([]tool.Tool, 0, len(definitions))
	for _, def := range definitions {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			return nil, fmt.Errorf("registered tool has empty name")
		}
		schema, err := schemaFromMap(def.Parameters)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", name, err)
		}
		adapted, err := functiontool.New[map[string]any, map[string]any](
			functiontool.Config{Name: name, Description: def.Description, InputSchema: schema},
			func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
				return executor.Execute(ctx, name, ctx.FunctionCallID(), args)
			},
		)
		if err != nil {
			return nil, fmt.Errorf("adapt tool %s: %w", name, err)
		}
		out = append(out, adapted)
	}
	return out, nil
}

func schemaFromMap(input map[string]any) (*jsonschema.Schema, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(payload, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

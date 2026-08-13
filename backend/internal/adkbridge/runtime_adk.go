//go:build adk

package adkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/genai"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

type Runtime struct {
	runner *runner.Runner
}

func Enabled() bool { return true }

func New(cfg Config) (pipeline.Agent, error) {
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, fmt.Errorf("ADK model name is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	llm, err := openaimodel.NewModel(context.Background(), cfg.ModelName, &openaimodel.ClientConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    strings.TrimSpace(cfg.BaseURL),
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK OpenAI-compatible model: %w", err)
	}
	return newWithModel(cfg, llm)
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
	llm = &meteredLLM{
		inner:         llm,
		modelName:     strings.TrimSpace(cfg.ModelName),
		promptVersion: promptVersion,
		guard:         cfg.UsageGuard,
		meter:         cfg.UsageMeter,
	}
	if cfg.Tools == nil {
		return nil, fmt.Errorf("tool registry is required")
	}
	tools, err := buildHostTools(cfg.Tools)
	if err != nil {
		return nil, err
	}
	agent, err := llmagent.New(llmagent.Config{
		Name:        "companion",
		Description: "Single-user voice companion coordinator",
		Model:       llm,
		Instruction: instruction,
		Tools:       tools,
		Mode:        llmagent.ModeChat,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK llm agent: %w", err)
	}
	appName := strings.TrimSpace(cfg.AppName)
	if appName == "" {
		appName = "companion"
	}
	// ADK's in-memory runner is still temporary. Companion-owned durable
	// conversation/session semantics are the remaining session-parity gate in #15.
	r, err := runner.NewInMemory(appName, agent)
	if err != nil {
		return nil, fmt.Errorf("create ADK runner: %w", err)
	}
	return &Runtime{runner: r}, nil
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
	if r == nil || r.runner == nil {
		return fmt.Errorf("ADK runtime is not initialized")
	}
	if emit == nil {
		return fmt.Errorf("stream callback is required")
	}
	if strings.TrimSpace(transcript) == "" {
		return fmt.Errorf("transcript is required")
	}

	turn, _ := pipeline.CurrentTurn(ctx)
	if strings.TrimSpace(turn.TurnID) == "" {
		turn.TurnID = turnID
		ctx = pipeline.WithTurnContext(ctx, turn)
	}
	userID, sessionID := SessionIdentity(turn)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	presentations := &presentationQueue{}
	ctx = capability.WithPresentationSink(ctx, presentations.Push)
	outcomes := &invocationOutcomeTracker{}
	ctx = withToolOutcomeSink(ctx, outcomes.RecordTool)

	msg := &genai.Content{Role: "user", Parts: []*genai.Part{{Text: transcript}}}
	tracker := &textDeltaTracker{}
	sentText := false
	for event, err := range r.runner.Run(ctx, userID, sessionID, msg, adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
		if err != nil {
			return fmt.Errorf("ADK run: %w", err)
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
		if err := emit(pipeline.AgentStreamEvent{TextDelta: fallback}); err != nil {
			return err
		}
	}
	return nil
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
		ui := &pipeline.UICard{
			Kind: p.Kind, Title: p.Title, Primary: p.Primary,
			Secondary: p.Secondary, Progress: p.Progress,
		}
		if err := emit(pipeline.AgentStreamEvent{UI: ui}); err != nil {
			return err
		}
	}
	return nil
}

// buildHostTools adapts every current Companion ToolRegistry definition into ADK.
// ToolRegistry remains the canonical schema/policy/execution boundary; ADK owns only
// model-facing invocation plumbing.
func buildHostTools(registry *capability.ToolRegistry) ([]tool.Tool, error) {
	definitions := registry.Definitions()
	if len(definitions) == 0 {
		return nil, fmt.Errorf("tool registry exposes no tools")
	}
	executor := HostToolExecutor{Registry: registry}
	out := make([]tool.Tool, 0, len(definitions))
	for _, definition := range definitions {
		hostTool, err := newHostTool(registry, executor, definition)
		if err != nil {
			return nil, err
		}
		out = append(out, hostTool)
	}
	return out, nil
}

func newHostTool(registry *capability.ToolRegistry, executor HostToolExecutor, definition capability.ToolDefinition) (tool.Tool, error) {
	name := strings.TrimSpace(definition.Name)
	if name == "" {
		return nil, fmt.Errorf("registered tool has empty name")
	}
	if _, ok := registry.Definition(name); !ok {
		return nil, fmt.Errorf("registered tool %q disappeared while building ADK tools", name)
	}
	schema, err := schemaFromMap(definition.Parameters)
	if err != nil {
		return nil, fmt.Errorf("tool %s schema: %w", name, err)
	}
	return functiontool.New[map[string]any, map[string]any](functiontool.Config{
		Name:        name,
		Description: definition.Description,
		InputSchema: schema,
	}, func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
		return executor.Execute(ctx, name, ctx.FunctionCallID(), args)
	})
}

func schemaFromMap(input map[string]any) (*jsonschema.Schema, error) {
	if len(input) == 0 {
		input = map[string]any{"type": "object", "additionalProperties": true}
	}
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

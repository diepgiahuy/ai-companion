package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/contextengine"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	"companion-server/internal/policy"
	usagepkg "companion-server/internal/usage"
	promptpkg "companion-server/prompts"
)

type TurnResultStore interface {
	TurnResult(context.Context, string) (string, bool, error)
	SaveTurnResult(context.Context, string, string) error
}
type ContextPlanner interface {
	Plan(context.Context, string) contextengine.Plan
}

type Qwen struct {
	baseURL, apiKey, model string
	location               *time.Location
	client                 *http.Client
	turns                  TurnResultStore
	conversation           *conversationctx.Service
	toolRegistry           *capability.ToolRegistry
	planner                ContextPlanner
	usageMeter             UsageMeter
	usageGuard             interface {
		Check(context.Context, string) error
	}
	modelSelector ModelSelector
	generation    GenerationConfig
	promptBundle  *promptpkg.Bundle
	persona       string
}
type Usage = usagepkg.Record
type UsageMeter = usagepkg.Meter

type Option func(*Qwen)

func WithModelSelector(selector ModelSelector) Option {
	return func(q *Qwen) { q.modelSelector = selector }
}
func WithGenerationConfig(config GenerationConfig) Option {
	return func(q *Qwen) { q.generation = config }
}
func WithPromptBundle(bundle *promptpkg.Bundle) Option {
	return func(q *Qwen) { q.promptBundle = bundle }
}
func WithPersona(persona string) Option {
	return func(q *Qwen) { q.persona = strings.TrimSpace(persona) }
}
func WithUsageMeter(m UsageMeter) Option { return func(q *Qwen) { q.usageMeter = m } }
func WithUsageGuard(g interface {
	Check(context.Context, string) error
}) Option                                                { return func(q *Qwen) { q.usageGuard = g } }
func WithConversation(s *conversationctx.Service) Option { return func(q *Qwen) { q.conversation = s } }
func WithToolRegistry(r *capability.ToolRegistry) Option { return func(q *Qwen) { q.toolRegistry = r } }
func WithContextPlanner(p ContextPlanner) Option         { return func(q *Qwen) { q.planner = p } }
func NewQwen(baseURL, apiKey, model, timezone string, turns TurnResultStore, options ...Option) (*Qwen, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone: %w", err)
	}
	q := &Qwen{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		location:   loc,
		turns:      turns,
		generation: DefaultGenerationConfig(),
	}
	for _, o := range options {
		o(q)
	}
	if err := q.generation.Validate(); err != nil {
		return nil, fmt.Errorf("generation config: %w", err)
	}
	q.client = &http.Client{Timeout: q.generation.HTTPTimeout}
	if q.promptBundle == nil {
		q.promptBundle, err = promptpkg.LoadDefault()
		if err != nil {
			return nil, fmt.Errorf("load default prompt bundle: %w", err)
		}
	}
	if q.turns == nil {
		return nil, fmt.Errorf("turn result store is required")
	}
	if q.toolRegistry == nil {
		return nil, fmt.Errorf("tool registry is required")
	}
	return q, nil
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}
type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type toolDefinition struct {
	Type     string         `json:"type"`
	Function map[string]any `json:"function"`
}
type chatRequest struct {
	Model             string           `json:"model"`
	Messages          []chatMessage    `json:"messages"`
	Tools             []toolDefinition `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls,omitempty"`
	Temperature       float64          `json:"temperature"`
	MaxTokens         int              `json:"max_tokens"`
}
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (q *Qwen) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	r, e := q.RespondRich(ctx, turnID, transcript)
	return r.Text, e
}
func (q *Qwen) RespondRich(ctx context.Context, turnID, transcript string) (pipeline.AgentResult, error) {
	ctx = policy.WithExplicitDestructive(ctx, policy.LooksDestructive(transcript))
	turn, _ := pipeline.CurrentTurn(ctx)
	userID := strings.TrimSpace(turn.UserID)
	if userID == "" {
		userID = strings.TrimSpace(turn.DeviceID)
	}
	if q.usageGuard != nil {
		if err := q.usageGuard.Check(ctx, userID); err != nil {
			return pipeline.AgentResult{}, err
		}
	}
	threadID := strings.TrimSpace(turn.ThreadID)
	if threadID == "" {
		threadID = "default"
	}
	turnKey := turnID
	if userID != "" {
		// turn_id is monotonic only within a device process and can repeat after
		// reboot. Include the server session nonce so durable idempotency never
		// returns a stale response from an earlier boot/reconnect.
		sessionID := strings.TrimSpace(turn.SessionID)
		if sessionID == "" {
			sessionID = "local"
		}
		turnKey = userID + ":" + threadID + ":" + turn.DeviceID + ":" + sessionID + ":" + turnID
	}
	if cached, ok, err := q.turns.TurnResult(ctx, turnKey); err != nil {
		return pipeline.AgentResult{}, err
	} else if ok {
		return pipeline.AgentResult{Text: cached}, nil
	}
	plan := contextengine.Plan{Packs: []string{"expense", "budget", "note", "journal", "schedule", "voice"}}
	if q.planner != nil {
		plan = q.planner.Plan(ctx, transcript)
	}
	location := q.location
	if strings.TrimSpace(turn.Timezone) != "" {
		if l, err := time.LoadLocation(turn.Timezone); err == nil {
			location = l
		}
	}
	now := time.Now().In(location)
	locale := strings.TrimSpace(turn.Locale)
	if locale == "" {
		locale = "vi-VN"
	}
	renderedPrompt, err := q.promptBundle.Render(promptpkg.RenderInput{
		Locale:      locale,
		CurrentTime: now,
		Timezone:    location.String(),
		Persona:     q.persona,
		Packs:       plan.Packs,
	})
	if err != nil {
		return pipeline.AgentResult{}, fmt.Errorf("render prompt: %w", err)
	}
	promptVersion := renderedPrompt.ID + "@" + renderedPrompt.Version + "#" + renderedPrompt.Fingerprint
	messages := []chatMessage{{Role: "system", Content: renderedPrompt.Text}}
	if q.conversation != nil {
		scope := conversationctx.Scope{UserID: userID, ThreadID: threadID}
		if history, err := q.conversation.Recent(ctx, scope, 12); err == nil {
			for _, x := range history {
				messages = append(messages, chatMessage{Role: x.Role, Content: x.Content})
			}
		}
	}
	for _, res := range plan.Resources {
		messages = append(messages, chatMessage{Role: "system", Content: fmt.Sprintf("CONTEXT_RESOURCE %s (%s): %s", res.URI, res.MIMEType, res.Text)})
	}
	messages = append(messages, chatMessage{Role: "user", Content: transcript})
	if q.conversation != nil {
		_ = q.conversation.Append(ctx, turnKey, conversationctx.Scope{UserID: userID, ThreadID: threadID}, "user", transcript)
	}
	modelTools := q.modelTools(plan.Packs)
	selectedModel := q.model
	if q.modelSelector != nil {
		if m := strings.TrimSpace(q.modelSelector.Select(ctx, transcript)); m != "" {
			selectedModel = m
		}
	}
	var ui *pipeline.UICard
	for round := 0; round < q.generation.MaxToolRounds; round++ {
		message, err := q.complete(ctx, messages, modelTools, selectedModel, promptVersion)
		if err != nil {
			return pipeline.AgentResult{}, err
		}
		messages = append(messages, message)
		if len(message.ToolCalls) == 0 {
			response := strings.TrimSpace(message.Content)
			if response == "" {
				return pipeline.AgentResult{}, fmt.Errorf("model returned an empty response")
			}
			if q.conversation != nil {
				_ = q.conversation.Append(ctx, turnKey, conversationctx.Scope{UserID: userID, ThreadID: threadID}, "assistant", response)
			}
			if err := q.turns.SaveTurnResult(ctx, turnKey, response); err != nil {
				return pipeline.AgentResult{}, err
			}
			return pipeline.AgentResult{Text: response, UI: ui}, nil
		}
		for i, call := range message.ToolCalls {
			key := fmt.Sprintf("%s:%d:%d:%s", turnKey, round, i, call.Function.Name)
			result := q.toolRegistry.Execute(ctx, call.Function.Name, capability.ToolRequest{Key: key, Arguments: call.Function.Arguments})
			if result.Presentation != nil {
				capability.EmitPresentation(ctx, *result.Presentation)
				ui = &pipeline.UICard{Kind: result.Presentation.Kind, Title: result.Presentation.Title, Primary: result.Presentation.Primary, Secondary: result.Presentation.Secondary, Progress: result.Presentation.Progress}
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: result.Content})
		}
	}
	return pipeline.AgentResult{}, fmt.Errorf("tool loop exceeded configured maximum of %d rounds", q.generation.MaxToolRounds)
}
func (q *Qwen) complete(ctx context.Context, messages []chatMessage, tools []toolDefinition, model, promptVersion string) (chatMessage, error) {
	reqBody := chatRequest{
		Model:       model,
		Messages:    messages,
		Tools:       tools,
		Temperature: q.generation.Temperature,
		MaxTokens:   q.generation.MaxTokens,
	}
	if len(tools) > 0 {
		reqBody.ToolChoice = "auto"
		reqBody.ParallelToolCalls = true
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return chatMessage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return chatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if q.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+q.apiKey)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return chatMessage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return chatMessage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return chatMessage{}, fmt.Errorf("qwen endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return chatMessage{}, fmt.Errorf("decode qwen response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return chatMessage{}, fmt.Errorf("qwen response has no choices")
	}
	if q.usageMeter != nil && decoded.Usage.TotalTokens > 0 {
		turn, _ := pipeline.CurrentTurn(ctx)
		q.usageMeter.RecordUsage(ctx, Usage{Provider: "openai-compatible", Model: model, PromptVersion: promptVersion, PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens, TotalTokens: decoded.Usage.TotalTokens, UserID: turn.UserID, DeviceID: turn.DeviceID})
	}
	return decoded.Choices[0].Message, nil
}
func (q *Qwen) executeTool(ctx context.Context, key string, call toolCall) string {
	return q.toolRegistry.Execute(ctx, call.Function.Name, capability.ToolRequest{Key: key, Arguments: call.Function.Arguments}).Content
}

func (q *Qwen) modelTools(packs []string) []toolDefinition {
	defs := q.toolRegistry.DefinitionsForPacks(packs)
	out := make([]toolDefinition, 0, len(defs))
	for _, d := range defs {
		out = append(out, toolDefinition{Type: "function", Function: map[string]any{"name": d.Name, "description": d.Description, "parameters": d.Parameters}})
	}
	return out
}

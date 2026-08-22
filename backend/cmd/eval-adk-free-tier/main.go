//go:build adk

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"companion-server/internal/adkbridge"
	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
)

const (
	modelID        = "gemma-4-31b-it"
	providerBase   = "https://generativelanguage.googleapis.com/v1beta/openai"
	probeToolName  = "evidence.lookup"
	probeQuery     = "adk-production-path"
	requestSpacing = 15 * time.Second
)

type evidenceReport struct {
	Status              string `json:"status"`
	Model               string `json:"model"`
	Protocol            string `json:"protocol"`
	ProviderBase        string `json:"provider_base"`
	ProviderToolAliases bool   `json:"provider_tool_aliases"`
	ToolName            string `json:"tool_name"`
	ToolExecutions      int64  `json:"tool_executions"`
	ToolQuery           string `json:"tool_query,omitempty"`
	Response            string `json:"response,omitempty"`
	SourceCommit        string `json:"source_commit,omitempty"`
	Hardware            string `json:"hardware,omitempty"`
	RequestSpacingMS    int64  `json:"request_spacing_ms"`
	Retries             int    `json:"retries"`
	ElapsedMS           int64  `json:"elapsed_ms"`
	Error               string `json:"error,omitempty"`
}

type memoryStore struct {
	mu       sync.Mutex
	messages map[string][]conversationctx.Message
}

func newMemoryStore() *memoryStore {
	return &memoryStore{messages: make(map[string][]conversationctx.Message)}
}

func (s *memoryStore) Append(_ context.Context, _ string, scope conversationctx.Scope, role, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scope.Key()
	s.messages[key] = append(s.messages[key], conversationctx.Message{Role: role, Content: strings.TrimSpace(content), CreatedAt: time.Now().UTC()})
	return nil
}

func (s *memoryStore) Recent(_ context.Context, scope conversationctx.Scope, limit int) ([]conversationctx.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := append([]conversationctx.Message(nil), s.messages[scope.Key()]...)
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages, nil
}

func (s *memoryStore) Clear(_ context.Context, scope conversationctx.Scope) error {
	s.mu.Lock()
	delete(s.messages, scope.Key())
	s.mu.Unlock()
	return nil
}

type pacingTransport struct {
	inner    http.RoundTripper
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func (p *pacingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	inner := p.inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	p.mu.Lock()
	now := time.Now()
	start := now
	if p.next.After(now) {
		start = p.next
	}
	p.next = start.Add(p.interval)
	p.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
	return inner.RoundTrip(req)
}

func buildProbeRegistry(executions *atomic.Int64, lastQuery *atomic.Value) (*capability.ToolRegistry, error) {
	registry := capability.NewToolRegistry()
	definition := &capability.ToolDefinition{
		Name:        probeToolName,
		Description: "Read-only production-path compatibility probe. Call exactly once with query adk-production-path before answering.",
		Pack:        "evidence",
		Risk:        "read",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "enum": []string{probeQuery}},
			},
			"required": []string{"query"},
		},
	}
	tool := capability.FunctionTool{
		ToolName:       probeToolName,
		ToolDefinition: definition,
		Handler: func(_ context.Context, req capability.ToolRequest) capability.ToolResult {
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(req.Arguments), &args); err != nil {
				return capability.Failure(fmt.Errorf("decode probe arguments: %w", err))
			}
			executions.Add(1)
			lastQuery.Store(args.Query)
			if args.Query != probeQuery {
				return capability.Failure(fmt.Errorf("unexpected probe query %q", args.Query))
			}
			return capability.Success(map[string]any{"value": "adk-tool-roundtrip-ok", "query": args.Query})
		},
	}
	if err := registry.Register(tool); err != nil {
		return nil, err
	}
	return registry, nil
}

func run(ctx context.Context, apiKey, sourceCommit, hardware string) (report evidenceReport, err error) {
	report = evidenceReport{
		Status:              "FAIL",
		Model:               modelID,
		Protocol:            adkbridge.ModelProtocolChatCompletions,
		ProviderBase:        providerBase,
		ProviderToolAliases: true,
		ToolName:            probeToolName,
		SourceCommit:        strings.TrimSpace(sourceCommit),
		Hardware:            strings.TrimSpace(hardware),
		RequestSpacingMS:    requestSpacing.Milliseconds(),
		Retries:             0,
	}
	started := time.Now()
	defer func() { report.ElapsedMS = time.Since(started).Milliseconds() }()

	if strings.TrimSpace(apiKey) == "" {
		return report, errors.New("GEMINI_API_KEY is required")
	}

	var executions atomic.Int64
	var lastQuery atomic.Value
	registry, err := buildProbeRegistry(&executions, &lastQuery)
	if err != nil {
		return report, err
	}
	conversation := conversationctx.New(newMemoryStore(), conversationctx.NoopCache{})
	client := &http.Client{
		Timeout: 75 * time.Second,
		Transport: &pacingTransport{
			inner:    http.DefaultTransport,
			interval: requestSpacing,
		},
	}
	agent, err := adkbridge.NewProvider(adkbridge.Config{
		AppName:             "companion-model-evidence",
		ModelName:           modelID,
		ModelProtocol:       adkbridge.ModelProtocolChatCompletions,
		ProviderToolAliases: true,
		BaseURL:             providerBase,
		APIKey:              strings.TrimSpace(apiKey),
		Instruction:         "This is a production-path compatibility probe. Before answering, call the available read-only tool evidence.lookup exactly once with query adk-production-path. After the tool result, give a short confirmation.",
		PromptVersion:       "issue-23-adk-provider-evidence-v1",
		HTTPClient:          client,
		Tools:               registry,
		Conversation:        conversation,
		HistoryLimit:        4,
	})
	if err != nil {
		return report, fmt.Errorf("build production ADK provider: %w", err)
	}

	turn := pipeline.TurnContext{
		UserID:    "issue-23-evidence",
		ThreadID:  "production-path",
		DeviceID:  "ci-evidence-device",
		SessionID: "issue-23-session",
		TurnID:    "issue-23-turn",
		Locale:    "vi-VN",
		Timezone:  "Asia/Ho_Chi_Minh",
	}
	ctx = pipeline.WithTurnContext(ctx, turn)
	response, err := agent.Respond(ctx, turn.TurnID, "Run the production ADK tool compatibility probe now.")
	report.ToolExecutions = executions.Load()
	if value := lastQuery.Load(); value != nil {
		report.ToolQuery, _ = value.(string)
	}
	report.Response = strings.TrimSpace(response)
	if err != nil {
		return report, fmt.Errorf("production ADK response: %w", err)
	}
	if report.ToolExecutions != 1 {
		return report, fmt.Errorf("expected exactly one ToolRegistry execution, got %d", report.ToolExecutions)
	}
	if report.ToolQuery != probeQuery {
		return report, fmt.Errorf("unexpected ToolRegistry query %q", report.ToolQuery)
	}
	if report.Response == "" {
		return report, errors.New("ADK returned empty final response after tool execution")
	}
	report.Status = "PASS"
	return report, nil
}

func main() {
	out := flag.String("out", "", "optional JSON evidence output path")
	sourceCommit := flag.String("source-commit", "", "exact source commit under test")
	hardware := flag.String("hardware", "", "runner/host provenance")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report, err := run(ctx, os.Getenv("GEMINI_API_KEY"), *sourceCommit, *hardware)
	if err != nil {
		report.Error = err.Error()
	}
	payload, marshalErr := json.MarshalIndent(report, "", "  ")
	if marshalErr != nil {
		fmt.Fprintln(os.Stderr, marshalErr)
		os.Exit(1)
	}
	payload = append(payload, '\n')
	if strings.TrimSpace(*out) != "" {
		if writeErr := os.WriteFile(*out, payload, 0o644); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
			os.Exit(1)
		}
	}
	fmt.Print(string(payload))
	if err != nil {
		os.Exit(1)
	}
}

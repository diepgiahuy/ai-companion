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
)

// ModelGateway is the provider boundary used by the companion agent runtime.
//
// Product orchestration must depend on this interface rather than directly on
// an OpenAI-compatible HTTP endpoint. This keeps local Qwen, hosted models,
// deterministic tests, and future ADK model adapters replaceable without
// changing domain/tool logic.
type ModelGateway interface {
	Complete(context.Context, ModelRequest) (ModelResponse, error)
}

type ModelRequest struct {
	Model             string
	Messages          []chatMessage
	Tools             []toolDefinition
	ToolChoice        string
	ParallelToolCalls bool
	Temperature       float64
	MaxTokens         int
}

type ModelResponse struct {
	Message chatMessage
	Usage   ModelUsage
}

type ModelUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// OpenAICompatibleGateway is only a transport/provider adapter. It does not
// own conversation history, tool execution, domain policy, or idempotency.
type OpenAICompatibleGateway struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewOpenAICompatibleGateway(baseURL, apiKey string, client *http.Client) *OpenAICompatibleGateway {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &OpenAICompatibleGateway{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  apiKey,
		client:  client,
	}
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

func (g *OpenAICompatibleGateway) Complete(ctx context.Context, input ModelRequest) (ModelResponse, error) {
	if g == nil || g.client == nil {
		return ModelResponse{}, fmt.Errorf("model gateway is not initialized")
	}
	if g.baseURL == "" {
		return ModelResponse{}, fmt.Errorf("model gateway base URL is empty")
	}
	reqBody := chatRequest{
		Model:             input.Model,
		Messages:          input.Messages,
		Tools:             input.Tools,
		ToolChoice:        input.ToolChoice,
		ParallelToolCalls: input.ParallelToolCalls,
		Temperature:       input.Temperature,
		MaxTokens:         input.MaxTokens,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return ModelResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ModelResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return ModelResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ModelResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ModelResponse{}, fmt.Errorf("model endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ModelResponse{}, fmt.Errorf("decode model response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return ModelResponse{}, fmt.Errorf("model response has no choices")
	}
	return ModelResponse{
		Message: decoded.Choices[0].Message,
		Usage: ModelUsage{
			PromptTokens:     decoded.Usage.PromptTokens,
			CompletionTokens: decoded.Usage.CompletionTokens,
			TotalTokens:      decoded.Usage.TotalTokens,
		},
	}, nil
}

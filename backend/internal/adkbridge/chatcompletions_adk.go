//go:build adk

package adkbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// NewChatCompletions builds the same Companion ADK llmagent/runtime using an
// OpenAI-compatible Chat Completions transport. Tool execution, sessions,
// policy and durable state stay inside the existing ADK + ToolRegistry path.
func NewChatCompletions(cfg Config) (pipelineAgent, error) {
	llm, err := newChatCompletionsModel(cfg)
	if err != nil {
		return nil, err
	}
	return newWithModel(cfg, llm)
}

type pipelineAgent interface {
	Respond(context.Context, string, string) (string, error)
}

type chatCompletionsModel struct {
	name       string
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newChatCompletionsModel(cfg Config) (*chatCompletionsModel, error) {
	name := strings.TrimSpace(cfg.ModelName)
	if name == "" {
		return nil, errors.New("ADK model name is required")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, errors.New("Chat Completions base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Chat Completions base URL %q", baseURL)
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return nil, fmt.Errorf("Chat Completions base URL must use https outside localhost")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &chatCompletionsModel{name: name, baseURL: strings.TrimRight(baseURL, "/"), apiKey: strings.TrimSpace(cfg.APIKey), httpClient: client}, nil
}

func (m *chatCompletionsModel) Name() string { return m.name }

func (m *chatCompletionsModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		body, err := buildChatRequest(m.name, req, stream)
		if err != nil {
			yield(nil, err)
			return
		}
		payload, err := json.Marshal(body)
		if err != nil {
			yield(nil, fmt.Errorf("chat completions: marshal request: %w", err))
			return
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if m.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
		}
		resp, err := m.httpClient.Do(httpReq)
		if err != nil {
			yield(nil, fmt.Errorf("chat completions: request failed: %w", err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			yield(nil, fmt.Errorf("chat completions: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
			return
		}
		if stream {
			m.readStream(ctx, resp.Body, yield)
			return
		}
		m.readResponse(resp.Body, yield)
	}
}

type chatRequest struct {
	Model            string        `json:"model"`
	Messages         []chatMessage `json:"messages"`
	Tools            []chatTool    `json:"tools,omitempty"`
	ToolChoice       any           `json:"tool_choice,omitempty"`
	Stream           bool          `json:"stream,omitempty"`
	Temperature      *float32      `json:"temperature,omitempty"`
	TopP             *float32      `json:"top_p,omitempty"`
	MaxTokens        int32         `json:"max_tokens,omitempty"`
	Stop             []string      `json:"stop,omitempty"`
	FrequencyPenalty *float32      `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float32      `json:"presence_penalty,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

type chatToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatToolCallFunc `json:"function"`
}

type chatToolCallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func buildChatRequest(modelName string, req *model.LLMRequest, stream bool) (chatRequest, error) {
	if req == nil {
		return chatRequest{}, errors.New("chat completions: request is nil")
	}
	request := chatRequest{Model: modelName, Stream: stream}
	if strings.TrimSpace(req.Model) != "" {
		request.Model = strings.TrimSpace(req.Model)
	}
	if req.Config != nil && req.Config.SystemInstruction != nil {
		text, err := contentTextOnly(req.Config.SystemInstruction)
		if err != nil {
			return chatRequest{}, fmt.Errorf("chat completions: system instruction: %w", err)
		}
		if text != "" {
			request.Messages = append(request.Messages, chatMessage{Role: "system", Content: text})
		}
	}
	messages, err := convertChatContents(req.Contents)
	if err != nil {
		return chatRequest{}, err
	}
	request.Messages = append(request.Messages, messages...)
	if len(request.Messages) == 0 {
		return chatRequest{}, errors.New("chat completions: no messages")
	}
	if req.Config != nil {
		request.Temperature = req.Config.Temperature
		request.TopP = req.Config.TopP
		request.MaxTokens = req.Config.MaxOutputTokens
		request.Stop = req.Config.StopSequences
		request.FrequencyPenalty = req.Config.FrequencyPenalty
		request.PresencePenalty = req.Config.PresencePenalty
		request.Tools, err = convertChatTools(req.Config)
		if err != nil {
			return chatRequest{}, err
		}
		request.ToolChoice = convertChatToolChoice(req.Config)
	}
	return request, nil
}

func contentTextOnly(content *genai.Content) (string, error) {
	if content == nil {
		return "", nil
	}
	var out []string
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.Text == "" || part.FunctionCall != nil || part.FunctionResponse != nil {
			return "", errors.New("non-text content is unsupported in system instruction")
		}
		out = append(out, part.Text)
	}
	return strings.Join(out, "\n"), nil
}

func convertChatContents(contents []*genai.Content) ([]chatMessage, error) {
	messages := make([]chatMessage, 0, len(contents))
	for _, content := range contents {
		if content == nil {
			continue
		}
		role := "user"
		switch genai.Role(content.Role) {
		case "", genai.RoleUser:
			role = "user"
		case genai.RoleModel:
			role = "assistant"
		case "system":
			role = "system"
		default:
			return nil, fmt.Errorf("chat completions: unsupported role %q", content.Role)
		}
		var text strings.Builder
		var calls []chatToolCall
		var responses []*genai.FunctionResponse
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			switch {
			case part.Text != "":
				text.WriteString(part.Text)
			case part.FunctionCall != nil:
				args, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("chat completions: marshal tool args: %w", err)
				}
				calls = append(calls, chatToolCall{ID: part.FunctionCall.ID, Type: "function", Function: chatToolCallFunc{Name: part.FunctionCall.Name, Arguments: string(args)}})
			case part.FunctionResponse != nil:
				responses = append(responses, part.FunctionResponse)
			default:
				return nil, errors.New("chat completions: unsupported non-text/non-function content part")
			}
		}
		if text.Len() > 0 || len(calls) > 0 {
			messages = append(messages, chatMessage{Role: role, Content: text.String(), ToolCalls: calls})
		}
		for _, response := range responses {
			payload, err := json.Marshal(response.Response)
			if err != nil {
				return nil, fmt.Errorf("chat completions: marshal tool response: %w", err)
			}
			if strings.TrimSpace(response.ID) == "" {
				return nil, fmt.Errorf("chat completions: function response %q missing call id", response.Name)
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: response.ID, Name: response.Name, Content: string(payload)})
		}
	}
	return messages, nil
}

func convertChatTools(cfg *genai.GenerateContentConfig) ([]chatTool, error) {
	if cfg == nil {
		return nil, nil
	}
	var result []chatTool
	for _, tool := range cfg.Tools {
		if tool == nil {
			continue
		}
		for _, declaration := range tool.FunctionDeclarations {
			if declaration == nil {
				continue
			}
			raw, err := json.Marshal(declaration)
			if err != nil {
				return nil, err
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, err
			}
			name, _ := object["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, errors.New("chat completions: function declaration missing name")
			}
			description, _ := object["description"].(string)
			parameters := object["parametersJsonSchema"]
			if parameters == nil {
				parameters = object["parameters"]
			}
			if parameters == nil {
				parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			result = append(result, chatTool{Type: "function", Function: chatToolFunction{Name: name, Description: description, Parameters: parameters}})
		}
	}
	return result, nil
}

func convertChatToolChoice(cfg *genai.GenerateContentConfig) any {
	if cfg == nil || cfg.ToolConfig == nil || cfg.ToolConfig.FunctionCallingConfig == nil {
		return nil
	}
	functionConfig := cfg.ToolConfig.FunctionCallingConfig
	switch functionConfig.Mode {
	case genai.FunctionCallingConfigModeNone:
		return "none"
	case genai.FunctionCallingConfigModeAny:
		if len(functionConfig.AllowedFunctionNames) == 1 {
			return map[string]any{"type": "function", "function": map[string]any{"name": functionConfig.AllowedFunctionNames[0]}}
		}
		return "required"
	case genai.FunctionCallingConfigModeAuto, genai.FunctionCallingConfigModeValidated, genai.FunctionCallingConfigModeUnspecified:
		return "auto"
	default:
		return nil
	}
}

type chatResponse struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Error *struct { Message string `json:"message"` } `json:"error,omitempty"`
	Choices []struct {
		Index int `json:"index"`
		FinishReason string `json:"finish_reason"`
		Message struct {
			Role string `json:"role"`
			Content string `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
}

type chatUsage struct {
	PromptTokens int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens int32 `json:"total_tokens"`
}

type chatStreamChunk struct {
	ID string `json:"id"`
	Model string `json:"model"`
	Error *struct { Message string `json:"message"` } `json:"error,omitempty"`
	Choices []struct {
		Index int `json:"index"`
		FinishReason *string `json:"finish_reason"`
		Delta struct {
			Role string `json:"role"`
			Content string `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage,omitempty"`
}

func (m *chatCompletionsModel) readResponse(body io.Reader, yield func(*model.LLMResponse, error) bool) {
	var response chatResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		yield(nil, fmt.Errorf("chat completions: decode response: %w", err))
		return
	}
	if response.Error != nil {
		yield(nil, errors.New(response.Error.Message))
		return
	}
	if len(response.Choices) == 0 {
		yield(nil, errors.New("chat completions: response has no choices"))
		return
	}
	choice := response.Choices[0]
	parts, err := chatParts(choice.Message.Content, choice.Message.ToolCalls)
	if err != nil {
		yield(nil, err)
		return
	}
	yield(&model.LLMResponse{
		Content: &genai.Content{Role: string(genai.RoleModel), Parts: parts},
		UsageMetadata: usageMetadata(response.Usage),
		ModelVersion: firstNonEmpty(response.Model, m.name),
		TurnComplete: true,
		FinishReason: chatFinishReason(choice.FinishReason),
		CustomMetadata: map[string]any{"chat_completion_id": response.ID, "chat_completion_model": response.Model},
	}, nil)
}

func (m *chatCompletionsModel) readStream(ctx context.Context, body io.Reader, yield func(*model.LLMResponse, error) bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var text strings.Builder
	calls := map[int]*chatToolCall{}
	finish := ""
	modelName := m.name
	responseID := ""
	var usage chatUsage
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			yield(nil, fmt.Errorf("chat completions: decode stream chunk: %w", err))
			return
		}
		if chunk.Error != nil {
			yield(nil, errors.New(chunk.Error.Message))
			return
		}
		responseID = firstNonEmpty(chunk.ID, responseID)
		modelName = firstNonEmpty(chunk.Model, modelName)
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			finish = *choice.FinishReason
		}
		if choice.Delta.Content != "" {
			text.WriteString(choice.Delta.Content)
			if !yield(&model.LLMResponse{
				Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: choice.Delta.Content}}},
				Partial: true,
				ModelVersion: modelName,
				CustomMetadata: map[string]any{"chat_completion_id": responseID, "chat_completion_model": modelName},
			}, nil) {
				return
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			current := calls[delta.Index]
			if current == nil {
				current = &chatToolCall{Index: delta.Index, Type: "function"}
				calls[delta.Index] = current
			}
			if delta.ID != "" {
				current.ID = delta.ID
			}
			if delta.Function.Name != "" {
				current.Function.Name += delta.Function.Name
			}
			if delta.Function.Arguments != "" {
				current.Function.Arguments += delta.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		yield(nil, fmt.Errorf("chat completions: stream read: %w", err))
		return
	}
	ordered := make([]chatToolCall, 0, len(calls))
	for index := 0; index < len(calls); index++ {
		if call := calls[index]; call != nil {
			ordered = append(ordered, *call)
		}
	}
	parts, err := chatParts(text.String(), ordered)
	if err != nil {
		yield(nil, err)
		return
	}
	if len(parts) == 0 {
		yield(nil, errors.New("chat completions: stream returned no text or tool calls"))
		return
	}
	yield(&model.LLMResponse{
		Content: &genai.Content{Role: string(genai.RoleModel), Parts: parts},
		UsageMetadata: usageMetadata(usage),
		ModelVersion: modelName,
		TurnComplete: true,
		FinishReason: chatFinishReason(finish),
		CustomMetadata: map[string]any{"chat_completion_id": responseID, "chat_completion_model": modelName},
	}, nil)
}

func chatParts(text string, calls []chatToolCall) ([]*genai.Part, error) {
	var parts []*genai.Part
	if text != "" {
		parts = append(parts, &genai.Part{Text: text})
	}
	for _, call := range calls {
		if strings.TrimSpace(call.Function.Name) == "" {
			return nil, errors.New("chat completions: function call missing name")
		}
		args := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("chat completions: invalid function args for %s: %w", call.Function.Name, err)
			}
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Function.Name, Args: args}})
	}
	return parts, nil
}

func usageMetadata(usage chatUsage) *genai.GenerateContentResponseUsageMetadata {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: usage.PromptTokens, CandidatesTokenCount: usage.CompletionTokens, TotalTokenCount: usage.TotalTokens}
}

func chatFinishReason(reason string) genai.FinishReason {
	switch reason {
	case "", "stop", "tool_calls":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

var _ model.LLM = (*chatCompletionsModel)(nil)

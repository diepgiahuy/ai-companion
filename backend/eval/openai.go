package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type OpenAIConfig struct {
	Name         string
	Model        string
	Version      string
	Quantization string
	Runtime      string
	Region       string
	Endpoint     string
	APIKey       string
	Stream       bool
	Seed         *int64
	MaxTokens    int
	HTTPClient   *http.Client
}

type OpenAIProvider struct {
	config   OpenAIConfig
	endpoint string
	client   *http.Client
	metadata ProviderMetadata
}

func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("OpenAI-compatible model is required")
	}
	endpoint, err := normalizeChatEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	client := hardenedHTTPClient(cfg.HTTPClient, endpoint)
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "openai-compatible"
	}
	return &OpenAIProvider{
		config:   cfg,
		endpoint: endpoint,
		client:   client,
		metadata: ProviderMetadata{
			Name:         name,
			Model:        strings.TrimSpace(cfg.Model),
			Version:      strings.TrimSpace(cfg.Version),
			Quantization: strings.TrimSpace(cfg.Quantization),
			Runtime:      strings.TrimSpace(cfg.Runtime),
			Region:       strings.TrimSpace(cfg.Region),
			Endpoint:     redactEndpoint(endpoint),
		},
	}, nil
}

func (p *OpenAIProvider) Metadata() ProviderMetadata { return p.metadata }
func (p *OpenAIProvider) EvidenceClass() string      { return EvidenceClassProviderMeasured }

type chatRequest struct {
	Model          string           `json:"model"`
	Messages       []chatMessage    `json:"messages"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	ToolChoice     any              `json:"tool_choice,omitempty"`
	ResponseFormat any              `json:"response_format,omitempty"`
	Temperature    float64          `json:"temperature"`
	Seed           *int64           `json:"seed,omitempty"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Stream         bool             `json:"stream"`
	StreamOptions  any              `json:"stream_options,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type apiUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []apiToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *apiUsage `json:"usage,omitempty"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []apiToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *apiUsage `json:"usage,omitempty"`
}

func (p *OpenAIProvider) Evaluate(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	payload := p.buildRequest(req.Scenario)
	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("create chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.config.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(p.config.APIKey))
	}
	started := time.Now()
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("call chat endpoint: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return ProviderResponse{}, fmt.Errorf("chat endpoint returned %s", httpResp.Status)
	}
	if p.config.Stream {
		return readStreamingResponse(httpResp.Body, started)
	}
	return readJSONResponse(httpResp.Body, started)
}

func (p *OpenAIProvider) buildRequest(s Scenario) chatRequest {
	messages := []chatMessage{{Role: "system", Content: defaultSystemPrompt(s)}}
	for _, message := range s.History {
		messages = append(messages, chatMessage{Role: message.Role, Content: message.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: s.Input})
	request := chatRequest{
		Model:       strings.TrimSpace(p.config.Model),
		Messages:    messages,
		Tools:       s.Tools,
		Temperature: 0,
		Seed:        p.config.Seed,
		MaxTokens:   p.config.MaxTokens,
		Stream:      p.config.Stream,
	}
	if len(s.Tools) > 0 {
		request.ToolChoice = "auto"
	} else {
		request.ResponseFormat = map[string]string{"type": "json_object"}
	}
	if p.config.Stream {
		request.StreamOptions = map[string]bool{"include_usage": true}
	}
	return request
}

func defaultSystemPrompt(s Scenario) string {
	if strings.TrimSpace(s.System) != "" {
		return strings.TrimSpace(s.System)
	}
	return "You are an AI Companion benchmark target. Use supplied tools when appropriate. " +
		"When no tool call is needed, return exactly one JSON object with optional keys " +
		"answer (string), packs (array of strings), retrieved_ids (array of strings), and escalate (boolean). " +
		"Do not include markdown. Allowed routing packs are expense, budget, schedule, note, journal, voice, memory, market, and context."
}

func readJSONResponse(r io.Reader, started time.Time) (ProviderResponse, error) {
	data, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("read chat response: %w", err)
	}
	var response chatResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return ProviderResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(response.Choices) == 0 {
		return ProviderResponse{}, fmt.Errorf("chat response has no choices")
	}
	if err := validateAPIUsage(response.Usage); err != nil {
		return ProviderResponse{}, err
	}
	observation, warnings := normalizeChatOutput(response.Choices[0].Message.Content, response.Choices[0].Message.ToolCalls, response.Usage)
	return ProviderResponse{
		Observation: observation,
		Timing:      Timing{TotalUS: time.Since(started).Microseconds()},
		Warnings:    warnings,
	}, nil
}

func readStreamingResponse(r io.Reader, started time.Time) (ProviderResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var content strings.Builder
	toolCalls := make(map[int]*apiToolCall)
	var usage *apiUsage
	var ttft *int64
	completed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			completed = true
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return ProviderResponse{}, fmt.Errorf("decode chat stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			copyUsage := *chunk.Usage
			usage = &copyUsage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				markTTFT(&ttft, started)
				content.WriteString(choice.Delta.Content)
			}
			for _, delta := range choice.Delta.ToolCalls {
				markTTFT(&ttft, started)
				current := toolCalls[delta.Index]
				if current == nil {
					current = &apiToolCall{Index: delta.Index}
					toolCalls[delta.Index] = current
				}
				current.ID += delta.ID
				current.Function.Name += delta.Function.Name
				current.Function.Arguments += delta.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ProviderResponse{}, fmt.Errorf("read chat stream: %w", err)
	}
	if !completed {
		return ProviderResponse{}, fmt.Errorf("chat stream ended before [DONE]")
	}
	if err := validateAPIUsage(usage); err != nil {
		return ProviderResponse{}, err
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	ordered := make([]apiToolCall, 0, len(indexes))
	for _, index := range indexes {
		ordered = append(ordered, *toolCalls[index])
	}
	observation, warnings := normalizeChatOutput(content.String(), ordered, usage)
	return ProviderResponse{
		Observation: observation,
		Timing:      Timing{TTFTUS: ttft, TotalUS: time.Since(started).Microseconds()},
		Warnings:    warnings,
	}, nil
}

func markTTFT(target **int64, started time.Time) {
	if *target != nil {
		return
	}
	value := time.Since(started).Microseconds()
	*target = &value
}

func normalizeChatOutput(content string, calls []apiToolCall, usage *apiUsage) (Observation, []string) {
	observation := Observation{Text: strings.TrimSpace(content)}
	for _, call := range calls {
		arguments := json.RawMessage(strings.TrimSpace(call.Function.Arguments))
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		observation.ToolCalls = append(observation.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	if usage != nil {
		observation.Usage = &Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens}
	}
	if strings.TrimSpace(content) == "" {
		return observation, nil
	}
	var structured struct {
		Answer       string   `json:"answer"`
		Packs        []string `json:"packs"`
		RetrievedIDs []string `json:"retrieved_ids"`
		Escalate     *bool    `json:"escalate"`
	}
	if err := json.Unmarshal([]byte(content), &structured); err != nil {
		return observation, []string{"response content was not the structured observation JSON contract; text-only checks remain available"}
	}
	if structured.Answer != "" {
		observation.Text = structured.Answer
	}
	observation.Packs = structured.Packs
	observation.RetrievedIDs = structured.RetrievedIDs
	observation.Escalate = structured.Escalate
	return observation, nil
}

func normalizeChatEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse chat endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("chat endpoint must use http or https")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("chat endpoint host is required")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("chat endpoint must not contain URL userinfo")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("plain HTTP chat endpoint is allowed only on loopback")
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case path == "":
		path = "/v1/chat/completions"
	case strings.HasSuffix(path, "/v1"):
		path += "/chat/completions"
	case !strings.HasSuffix(path, "/chat/completions"):
		return "", fmt.Errorf("chat endpoint path must end with /v1 or /chat/completions")
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func hardenedHTTPClient(configured *http.Client, endpoint string) *http.Client {
	client := http.Client{Timeout: 2 * time.Minute}
	if configured != nil {
		client = *configured
		if client.Timeout <= 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	origin, _ := url.Parse(endpoint)
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != origin.Scheme || !strings.EqualFold(request.URL.Host, origin.Host) {
			return fmt.Errorf("chat endpoint redirect changed origin")
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many chat endpoint redirects")
		}
		if previousCheck != nil {
			return previousCheck(request, via)
		}
		return nil
	}
	return &client
}

func validateAPIUsage(usage *apiUsage) error {
	if usage == nil {
		return nil
	}
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 {
		return fmt.Errorf("chat endpoint returned negative token usage")
	}
	if usage.TotalTokens > 0 && usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		return fmt.Errorf("chat endpoint returned inconsistent token usage")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

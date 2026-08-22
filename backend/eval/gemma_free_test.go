package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGemmaFreeRejectsBillableModels(t *testing.T) {
	for _, model := range []string{"gemini-3.7-flash", "gemini-3.5-flash-lite", "gemini-2.5-flash"} {
		if _, err := NewGemmaFreeProvider(GemmaFreeConfig{Model: model, Version: "v1", APIKey: "test"}); err == nil {
			t.Fatalf("expected %s to be rejected", model)
		}
	}
	if !IsFreeOnlyGemmaModel("gemma-4-26b-a4b-it") || !IsFreeOnlyGemmaModel("gemma-4-31b-it") {
		t.Fatal("Gemma 4 hosted free-only models must remain allowlisted")
	}
}

func TestResolveGemmaFreeModelVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemma-4-26b-a4b-it" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Fatal("missing API key header")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "models/gemma-4-26b-a4b-it", "version": "004"})
	}))
	defer server.Close()
	version, err := ResolveGemmaFreeModelVersion(context.Background(), server.URL, "test-key", "gemma-4-26b-a4b-it", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if version != "004" {
		t.Fatalf("version=%q", version)
	}
}

func TestGemmaToolDeclarationsStripAdditionalProperties(t *testing.T) {
	definition := ToolDefinition{
		Function: ToolFunction{
			Name: "note.create",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"content": map[string]any{"type": "string"},
				},
			},
		},
	}
	aliases, declarations, err := gemmaToolDeclarations([]ToolDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || len(declarations) != 1 {
		t.Fatalf("unexpected declarations: %#v %#v", aliases, declarations)
	}
	params := declarations[0]["parameters"].(map[string]any)
	if _, found := params["additionalProperties"]; found {
		t.Fatal("Gemini-facing schema must strip additionalProperties")
	}
	alias := declarations[0]["name"].(string)
	if aliases[alias] != "note.create" {
		t.Fatalf("alias mapping lost canonical name: %#v", aliases)
	}
}

func TestGemmaFreeProviderParsesToolCallAndUsage(t *testing.T) {
	var calls atomic.Int32
	var expectedAlias string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Fatal("missing API key header")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		toolGroups := request["tools"].([]any)
		declarations := toolGroups[0].(map[string]any)["functionDeclarations"].([]any)
		expectedAlias = declarations[0].(map[string]any)["name"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"" + expectedAlias + "\",\"args\":{\"content\":\"buy batteries\"}}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":4,\"totalTokenCount\":14}}\n\n"))
	}))
	defer server.Close()

	provider, err := NewGemmaFreeProvider(GemmaFreeConfig{
		Model: "gemma-4-26b-a4b-it", Version: "004", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	definition := ToolDefinition{
		Function: ToolFunction{
			Name: "note.create",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{"type": "string"},
				},
				"required": []any{"content"},
			},
		},
	}
	response, err := provider.Evaluate(context.Background(), ProviderRequest{Scenario: Scenario{
		Input: "Note buy batteries",
		Tools: []ToolDefinition{definition},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests=%d; benchmark must not retry", calls.Load())
	}
	if len(response.Observation.ToolCalls) != 1 || response.Observation.ToolCalls[0].Name != "note.create" {
		t.Fatalf("tool calls=%#v", response.Observation.ToolCalls)
	}
	if !strings.Contains(string(response.Observation.ToolCalls[0].Arguments), "buy batteries") {
		t.Fatalf("arguments=%s", response.Observation.ToolCalls[0].Arguments)
	}
	if response.Observation.Usage == nil || response.Observation.Usage.TotalTokens != 14 {
		t.Fatalf("usage=%#v", response.Observation.Usage)
	}
	if response.Timing.TTFTUS == nil || response.Timing.TotalUS < *response.Timing.TTFTUS {
		t.Fatalf("timing=%#v", response.Timing)
	}
}

func TestGemmaFreeProviderDoesNotRetryQuotaFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "quota", http.StatusTooManyRequests)
	}))
	defer server.Close()
	provider, err := NewGemmaFreeProvider(GemmaFreeConfig{
		Model: "gemma-4-31b-it", Version: "004", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = provider.Evaluate(ctx, ProviderRequest{Scenario: Scenario{Input: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests=%d; quota failures must not be retried", calls.Load())
	}
}

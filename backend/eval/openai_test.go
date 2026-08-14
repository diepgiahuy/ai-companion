package eval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIEndpointAndRedirectsFailClosed(t *testing.T) {
	if _, err := normalizeChatEndpoint("https://user:secret@example.com/v1"); err == nil {
		t.Fatal("URL userinfo must be rejected")
	}
	if _, err := normalizeChatEndpoint("http://example.com/v1"); err == nil {
		t.Fatal("remote plain HTTP must be rejected")
	}

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-origin redirect reached its target")
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{Model: "candidate", Endpoint: origin.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Evaluate(context.Background(), ProviderRequest{Scenario: Scenario{Input: "hello"}}); err == nil || !strings.Contains(err.Error(), "redirect changed origin") {
		t.Fatalf("redirect error=%v", err)
	}
}

func TestOpenAIErrorBodyIsNotLeaked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private-provider-payload", http.StatusBadRequest)
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{Model: "candidate", Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Evaluate(context.Background(), ProviderRequest{Scenario: Scenario{Input: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") || strings.Contains(err.Error(), "private-provider-payload") {
		t.Fatalf("sanitized error=%v", err)
	}
}

func TestStreamingResponseRequiresCompletionAndValidUsage(t *testing.T) {
	started := time.Now()
	if _, err := readStreamingResponse(strings.NewReader(`data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n"), started); err == nil || !strings.Contains(err.Error(), "before [DONE]") {
		t.Fatalf("truncated stream error=%v", err)
	}

	negative := "data: {\"usage\":{\"prompt_tokens\":-1,\"completion_tokens\":0,\"total_tokens\":0}}\n\ndata: [DONE]\n\n"
	if _, err := readStreamingResponse(strings.NewReader(negative), started); err == nil || !strings.Contains(err.Error(), "negative token usage") {
		t.Fatalf("negative usage error=%v", err)
	}

	valid := "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"packs\\\":[\\\"note\\\"]}\"}}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n"
	response, err := readStreamingResponse(strings.NewReader(valid), started)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Observation.Packs) != 1 || response.Observation.Packs[0] != "note" || response.Observation.Usage == nil || response.Timing.TTFTUS == nil {
		t.Fatalf("response=%+v", response)
	}
}

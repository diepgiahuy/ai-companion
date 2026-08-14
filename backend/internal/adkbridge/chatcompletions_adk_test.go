//go:build adk

package adkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestChatCompletionsModelStreamsTextAndFinalToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path=%q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization=%q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		if body["model"] != "glm-4-flash" || body["stream"] != true {
			t.Errorf("request=%#v", body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"model\":\"glm-4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Để mình \"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"model\":\"glm-4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"safe_tool\",\"arguments\":\"{\\\"content\\\":\"}}]},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"model\":\"glm-4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"hello\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm, err := newChatCompletionsModel(Config{ModelName: "glm-4-flash", BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{{Role: string(genai.RoleUser), Parts: []*genai.Part{{Text: "save this"}}}}}
	var responses []*model.LLMResponse
	for response, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 2 || !responses[0].Partial || responses[1].Partial {
		t.Fatalf("responses=%+v", responses)
	}
	if got := responses[0].Content.Parts[0].Text; got != "Để mình " {
		t.Fatalf("partial=%q", got)
	}
	parts := responses[1].Content.Parts
	if len(parts) != 2 || parts[1].FunctionCall == nil {
		t.Fatalf("final parts=%+v", parts)
	}
	call := parts[1].FunctionCall
	if call.ID != "call-1" || call.Name != "safe_tool" || call.Args["content"] != "hello" {
		t.Fatalf("tool call=%+v", call)
	}
}

func TestBuildChatRequestPreservesToolCallResponsePair(t *testing.T) {
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: string(genai.RoleUser), Parts: []*genai.Part{{Text: "save note"}}},
			{Role: string(genai.RoleModel), Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "call-1", Name: "safe_note", Args: map[string]any{"content": "hello"}}}}},
			{Role: string(genai.RoleUser), Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "call-1", Name: "safe_note", Response: map[string]any{"ok": true}}}}},
		},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "safe_note", Description: "save note", ParametersJsonSchema: map[string]any{"type": "object"},
		}}}}},
	}
	body, err := buildChatRequest("glm-4-flash", req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages=%+v", body.Messages)
	}
	assistant := body.Messages[1]
	tool := body.Messages[2]
	if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant=%+v", assistant)
	}
	if tool.Role != "tool" || tool.ToolCallID != "call-1" || !strings.Contains(tool.Content, `"ok":true`) {
		t.Fatalf("tool response=%+v", tool)
	}
	if len(body.Tools) != 1 || body.Tools[0].Function.Name != "safe_note" {
		t.Fatalf("tools=%+v", body.Tools)
	}
}

func TestChatCompletionsRejectsInsecureRemoteBaseURL(t *testing.T) {
	if _, err := newChatCompletionsModel(Config{ModelName: "glm-4-flash", BaseURL: "http://example.com/v4"}); err == nil {
		t.Fatal("insecure remote Chat Completions endpoint unexpectedly accepted")
	}
}

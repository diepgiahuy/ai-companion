package agent

import (
	"context"
	"errors"
	"testing"
)

type deterministicGateway struct {
	requests []ModelRequest
	queue    []ModelResponse
	err      error
}

func (g *deterministicGateway) Complete(_ context.Context, request ModelRequest) (ModelResponse, error) {
	g.requests = append(g.requests, request)
	if g.err != nil {
		return ModelResponse{}, g.err
	}
	if len(g.queue) == 0 {
		return ModelResponse{}, errors.New("deterministic gateway script exhausted")
	}
	response := g.queue[0]
	g.queue = g.queue[1:]
	return response, nil
}

func TestModelGatewayCanReplaceHTTPProvider(t *testing.T) {
	gateway := &deterministicGateway{queue: []ModelResponse{{
		Message: chatMessage{Role: "assistant", Content: "deterministic reply"},
		Usage:   ModelUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
	}}}
	q := &Qwen{model: "fake-model", modelGateway: gateway}

	message, err := q.complete(context.Background(), []chatMessage{{Role: "user", Content: "hello"}}, nil, q.model)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "deterministic reply" {
		t.Fatalf("message = %q", message.Content)
	}
	if len(gateway.requests) != 1 || gateway.requests[0].Model != "fake-model" {
		t.Fatalf("requests = %+v", gateway.requests)
	}
	if gateway.requests[0].Temperature != .1 || gateway.requests[0].MaxTokens != 384 {
		t.Fatalf("gateway request lost runtime policy: %+v", gateway.requests[0])
	}
}

func TestModelGatewayReceivesToolPolicy(t *testing.T) {
	gateway := &deterministicGateway{queue: []ModelResponse{{Message: chatMessage{Role: "assistant", Content: "ok"}}}}
	q := &Qwen{model: "fake-model", modelGateway: gateway}
	tools := []toolDefinition{{Type: "function", Function: map[string]any{"name": "expense.create"}}}

	if _, err := q.complete(context.Background(), nil, tools, q.model); err != nil {
		t.Fatal(err)
	}
	request := gateway.requests[0]
	if request.ToolChoice != "auto" || !request.ParallelToolCalls {
		t.Fatalf("tool policy = %+v", request)
	}
}

func TestModelGatewayErrorPropagates(t *testing.T) {
	want := errors.New("provider unavailable")
	q := &Qwen{model: "fake-model", modelGateway: &deterministicGateway{err: want}}
	if _, err := q.complete(context.Background(), nil, nil, q.model); !errors.Is(err, want) {
		t.Fatalf("error = %v; want %v", err, want)
	}
}

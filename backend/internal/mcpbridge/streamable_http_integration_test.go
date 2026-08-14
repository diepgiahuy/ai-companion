//go:build mcp

package mcpbridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type integrationAuthorizer struct {
	allow bool
	calls atomic.Int32
}

func (a *integrationAuthorizer) Authorize(context.Context, capability.ToolDefinition, capability.ToolRequest) error {
	a.calls.Add(1)
	if !a.allow {
		return errors.New("integration policy denied")
	}
	return nil
}

func TestStreamableHTTPMCPFlowsThroughToolRegistryPolicy(t *testing.T) {
	var remoteCalls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "companion-test-mcp", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "Echo a bounded string",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"value": map[string]any{"type": "string", "maxLength": 64}},
			"required":             []string{"value"},
			"additionalProperties": false,
		},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return &mcp.CallToolResult{StructuredContent: map[string]any{"echo": req.Params.Arguments}}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	tlsServer := httptest.NewTLSServer(handler)
	defer tlsServer.Close()

	// secureHTTPClient clones http.DefaultTransport. Point it at the test
	// server's trusted transport for this test only so TLS verification remains
	// enabled rather than weakening the production endpoint policy.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = tlsServer.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()

	tools := capability.NewToolRegistry()
	resources := capability.NewResourceRegistry()
	manager, err := ConnectAndRegister(context.Background(), tools, resources, []ServerConfig{{
		Name:                "integration",
		Endpoint:            tlsServer.URL,
		Pack:                "external",
		AllowPrivateNetwork: true,
		Timeout:             3 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	definition, ok := tools.Definition("mcp.integration.echo")
	if !ok || definition.Risk != "external" || definition.Pack != "external" {
		t.Fatalf("registered definition=%+v ok=%v", definition, ok)
	}

	authorizer := &integrationAuthorizer{}
	tools.SetAuthorizer(authorizer)
	denied := tools.Execute(context.Background(), "mcp.integration.echo", capability.ToolRequest{Arguments: `{"value":"xin chao"}`})
	if !strings.Contains(denied.Content, `"ok":false`) || remoteCalls.Load() != 0 {
		t.Fatalf("denied result=%s remote_calls=%d", denied.Content, remoteCalls.Load())
	}

	authorizer.allow = true
	allowed := tools.Execute(context.Background(), "mcp.integration.echo", capability.ToolRequest{Arguments: `{"value":"xin chao"}`})
	if !strings.Contains(allowed.Content, `"ok":true`) || !strings.Contains(allowed.Content, `"source":"mcp"`) {
		t.Fatalf("allowed result=%s", allowed.Content)
	}
	if remoteCalls.Load() != 1 || authorizer.calls.Load() != 2 {
		t.Fatalf("remote_calls=%d authorize_calls=%d", remoteCalls.Load(), authorizer.calls.Load())
	}
}

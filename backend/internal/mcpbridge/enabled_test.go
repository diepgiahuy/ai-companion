//go:build mcp

package mcpbridge

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"companion-server/internal/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBridgeSession struct {
	tools     []*mcp.Tool
	resources []*mcp.Resource
	toolCalls int
	reads     int
	readJSON  string
}

func (f *fakeBridgeSession) Tools(_ context.Context, _ *mcp.ListToolsParams) iter.Seq2[*mcp.Tool, error] {
	return func(yield func(*mcp.Tool, error) bool) {
		for _, tool := range f.tools {
			if !yield(tool, nil) {
				return
			}
		}
	}
}
func (f *fakeBridgeSession) Resources(_ context.Context, _ *mcp.ListResourcesParams) iter.Seq2[*mcp.Resource, error] {
	return func(yield func(*mcp.Resource, error) bool) {
		for _, resource := range f.resources {
			if !yield(resource, nil) {
				return
			}
		}
	}
}
func (f *fakeBridgeSession) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.toolCalls++
	if params.Name != "echo" {
		return nil, context.Canceled
	}
	return &mcp.CallToolResult{}, nil
}
func (f *fakeBridgeSession) ReadResource(_ context.Context, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	f.reads++
	if params.URI != "memory://greeting" {
		return nil, context.Canceled
	}
	raw := f.readJSON
	if raw == "" {
		raw = `{"contents":[{"uri":"memory://greeting","mimeType":"text/plain","text":"xin chao"}]}`
	}
	var result mcp.ReadResourceResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func TestRegisterSessionExposesNamespacedToolAndResource(t *testing.T) {
	tools := capability.NewToolRegistry()
	resources := capability.NewResourceRegistry()
	session := &fakeBridgeSession{
		tools: []*mcp.Tool{{Name: "echo", Description: "Echo a value"}},
		resources: []*mcp.Resource{{URI: "memory://greeting", Name: "greeting", MIMEType: "text/plain", Description: "Greeting text"}},
	}
	if err := registerSession(context.Background(), tools, resources, ServerConfig{Name: "Demo Server"}, "Demo_Server", session); err != nil {
		t.Fatal(err)
	}

	result := tools.Execute(context.Background(), "mcp.Demo_Server.echo", capability.ToolRequest{Arguments: `{}`})
	if !strings.Contains(result.Content, `"ok":true`) || session.toolCalls != 1 {
		t.Fatalf("tool result=%s calls=%d", result.Content, session.toolCalls)
	}

	descriptors, err := resources.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("resources=%+v", descriptors)
	}
	if !strings.HasPrefix(descriptors[0].URI, "mcp-demo-server://resource/") {
		t.Fatalf("unexpected local URI=%q", descriptors[0].URI)
	}
	resource, err := resources.Read(context.Background(), descriptors[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Text != "xin chao" || resource.MIMEType != "text/plain" || session.reads != 1 {
		t.Fatalf("resource=%+v reads=%d", resource, session.reads)
	}
}

func TestExternalResourceRejectsUndiscoveredAndBlobOnlyContent(t *testing.T) {
	resources := capability.NewResourceRegistry()
	session := &fakeBridgeSession{resources: []*mcp.Resource{{URI: "memory://greeting", Name: "greeting"}}, readJSON: `{"contents":[{"uri":"memory://greeting","mimeType":"application/octet-stream","blob":"AQID"}]}`}
	provider := &externalResourceProvider{scheme: "mcp-demo", server: "demo", session: session, resources: map[string]remoteResource{}}
	local := localResourceURI("mcp-demo", "memory://greeting")
	provider.resources[local] = remoteResource{remoteURI: "memory://greeting", localURI: local, name: "greeting"}
	if err := resources.Register(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.Read(context.Background(), "mcp-demo://resource/not-discovered"); err == nil || !strings.Contains(err.Error(), "not discovered") {
		t.Fatalf("undiscovered error=%v", err)
	}
	if _, err := resources.Read(context.Background(), local); err == nil || !strings.Contains(err.Error(), "binary resource") {
		t.Fatalf("blob-only error=%v", err)
	}
}

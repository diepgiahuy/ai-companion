//go:build mcp

package mcpbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"net/url"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"companion-server/internal/capability"
)

type manager struct {
	mu       sync.Mutex
	sessions []*mcp.ClientSession
}

func (m *manager) Close() error {
	m.mu.Lock()
	sessions := append([]*mcp.ClientSession(nil), m.sessions...)
	m.sessions = nil
	m.mu.Unlock()
	var first error
	for _, session := range sessions {
		if err := session.Close(); err != nil && first == nil { first = err }
	}
	return first
}

type bridgeSession interface {
	Tools(context.Context, *mcp.ListToolsParams) iter.Seq2[*mcp.Tool, error]
	Resources(context.Context, *mcp.ListResourcesParams) iter.Seq2[*mcp.Resource, error]
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	ReadResource(context.Context, *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error)
}

type remoteResource struct {
	remoteURI   string
	localURI    string
	name        string
	description string
	mimeType    string
}

type externalResourceProvider struct {
	scheme    string
	server    string
	session   bridgeSession
	resources map[string]remoteResource
}

func (p *externalResourceProvider) Schemes() []string { return []string{p.scheme} }

func (p *externalResourceProvider) List(context.Context) ([]capability.ResourceDescriptor, error) {
	result := make([]capability.ResourceDescriptor, 0, len(p.resources))
	for _, resource := range p.resources {
		result = append(result, capability.ResourceDescriptor{URI: resource.localURI, Name: resource.name, Description: resource.description})
	}
	return result, nil
}

func (p *externalResourceProvider) Read(ctx context.Context, uri *url.URL) (capability.Resource, error) {
	if uri == nil || !strings.EqualFold(uri.Scheme, p.scheme) {
		return capability.Resource{}, fmt.Errorf("invalid external MCP resource URI")
	}
	resource, ok := p.resources[uri.String()]
	if !ok {
		return capability.Resource{}, fmt.Errorf("external MCP resource was not discovered")
	}
	result, err := p.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: resource.remoteURI})
	if err != nil { return capability.Resource{}, fmt.Errorf("external MCP resource read failed") }
	if result == nil || len(result.Contents) == 0 { return capability.Resource{}, fmt.Errorf("external MCP resource returned no content") }

	texts := make([]string, 0, len(result.Contents))
	mimeType := strings.TrimSpace(resource.mimeType)
	blobOnly := false
	for _, content := range result.Contents {
		if content == nil { continue }
		raw, err := json.Marshal(content)
		if err != nil { return capability.Resource{}, fmt.Errorf("encode external MCP resource content") }
		var wire struct {
			MIMEType string  `json:"mimeType"`
			Text     *string `json:"text"`
			Blob     string  `json:"blob"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil { return capability.Resource{}, fmt.Errorf("decode external MCP resource content") }
		if mimeType == "" && strings.TrimSpace(wire.MIMEType) != "" { mimeType = strings.TrimSpace(wire.MIMEType) }
		if wire.Text != nil { texts = append(texts, *wire.Text); continue }
		if wire.Blob != "" { blobOnly = true }
	}
	if len(texts) == 0 {
		if blobOnly { return capability.Resource{}, fmt.Errorf("external MCP binary resource is not supported by the text resource port") }
		return capability.Resource{}, fmt.Errorf("external MCP resource returned no text content")
	}
	if mimeType == "" { mimeType = "text/plain" }
	return capability.Resource{URI: resource.localURI, MIMEType: mimeType, Text: strings.Join(texts, "\n")}, nil
}

func connectAndRegister(ctx context.Context, tools *capability.ToolRegistry, resources *capability.ResourceRegistry, configs []ServerConfig) (Manager, error) {
	if tools == nil { return nil, fmt.Errorf("MCP tool registry is required") }
	if resources == nil { return nil, fmt.Errorf("MCP resource registry is required") }
	m := &manager{}
	for _, config := range configs {
		if err := m.connectServer(ctx, tools, resources, config); err != nil {
			_ = m.Close()
			return nil, err
		}
	}
	return m, nil
}

func (m *manager) connectServer(ctx context.Context, tools *capability.ToolRegistry, resources *capability.ResourceRegistry, config ServerConfig) error {
	name := sanitizeName(config.Name)
	if name == "" { return fmt.Errorf("MCP server name is required") }
	httpClient, err := secureHTTPClient(config)
	if err != nil { return fmt.Errorf("MCP server %s: %w", name, err) }
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-companion", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: strings.TrimSpace(config.Endpoint), HTTPClient: httpClient}, nil)
	if err != nil { return fmt.Errorf("connect MCP server %s: %w", name, err) }
	if err := registerSession(ctx, tools, resources, config, name, session); err != nil {
		_ = session.Close()
		return err
	}
	m.mu.Lock()
	m.sessions = append(m.sessions, session)
	m.mu.Unlock()
	return nil
}

func registerSession(ctx context.Context, registry *capability.ToolRegistry, resourceRegistry *capability.ResourceRegistry, config ServerConfig, name string, session bridgeSession) error {
	registeredTools := 0
	for remoteTool, err := range session.Tools(ctx, nil) {
		if err != nil { return fmt.Errorf("list MCP server %s tools: %w", name, err) }
		if remoteTool == nil || strings.TrimSpace(remoteTool.Name) == "" { continue }
		parameters, err := schemaMap(remoteTool.InputSchema)
		if err != nil { return fmt.Errorf("MCP server %s tool %s schema: %w", name, remoteTool.Name, err) }
		remoteName := remoteTool.Name
		localName := "mcp." + name + "." + sanitizeName(remoteName)
		pack := strings.TrimSpace(config.Pack)
		if pack == "" { pack = "external" }
		def := &capability.ToolDefinition{Name: localName, Description: strings.TrimSpace(remoteTool.Description), Parameters: parameters, Pack: pack, Risk: "external", Entitlement: strings.TrimSpace(config.Entitlement), FeatureKey: strings.TrimSpace(config.FeatureKey)}
		tool := capability.FunctionTool{ToolName: localName, ToolDefinition: def, Handler: func(callCtx context.Context, request capability.ToolRequest) capability.ToolResult {
			var arguments map[string]any
			rawArgs := strings.TrimSpace(request.Arguments)
			if rawArgs == "" { rawArgs = "{}" }
			if err := json.Unmarshal([]byte(rawArgs), &arguments); err != nil { return capability.Failure(fmt.Errorf("invalid external tool arguments")) }
			result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: remoteName, Arguments: arguments})
			if err != nil { return capability.Failure(fmt.Errorf("external MCP tool call failed")) }
			if result == nil || result.IsError { return capability.Failure(fmt.Errorf("external MCP tool returned an error")) }
			rawResult, err := json.Marshal(result)
			if err != nil { return capability.Failure(fmt.Errorf("encode external MCP tool result")) }
			return capability.Success(map[string]any{"source": "mcp", "server": name, "tool": remoteName, "data": json.RawMessage(rawResult)})
		}}
		if err := registry.Register(tool); err != nil { return fmt.Errorf("register MCP tool %s: %w", localName, err) }
		registeredTools++
	}

	provider := &externalResourceProvider{scheme: resourceScheme(name), server: name, session: session, resources: make(map[string]remoteResource)}
	for remote, err := range session.Resources(ctx, nil) {
		if err != nil { return fmt.Errorf("list MCP server %s resources: %w", name, err) }
		if remote == nil || strings.TrimSpace(remote.URI) == "" { continue }
		localURI := localResourceURI(provider.scheme, remote.URI)
		displayName := strings.TrimSpace(remote.Title)
		if displayName == "" { displayName = strings.TrimSpace(remote.Name) }
		if displayName == "" { displayName = remote.URI }
		provider.resources[localURI] = remoteResource{remoteURI: remote.URI, localURI: localURI, name: displayName, description: strings.TrimSpace(remote.Description), mimeType: strings.TrimSpace(remote.MIMEType)}
	}
	if len(provider.resources) > 0 {
		if err := resourceRegistry.Register(provider); err != nil { return fmt.Errorf("register MCP resources for %s: %w", name, err) }
	}
	if registeredTools == 0 && len(provider.resources) == 0 { return fmt.Errorf("MCP server %s exposed no usable tools or resources", name) }
	return nil
}

func localResourceURI(scheme, remoteURI string) string {
	return scheme + "://resource/" + base64.RawURLEncoding.EncodeToString([]byte(remoteURI))
}
func resourceScheme(name string) string {
	name = strings.ToLower(strings.ReplaceAll(sanitizeName(name), "_", "-"))
	return "mcp-" + name
}

func schemaMap(schema any) (map[string]any, error) {
	if schema == nil { return map[string]any{"type": "object", "additionalProperties": true}, nil }
	raw, err := json.Marshal(schema)
	if err != nil { return nil, err }
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil { return nil, err }
	if len(out) == 0 { return map[string]any{"type": "object", "additionalProperties": true}, nil }
	return out, nil
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastSep := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if valid { b.WriteRune(r); lastSep = false; continue }
		if !lastSep { b.WriteByte('_'); lastSep = true }
	}
	return strings.Trim(b.String(), "_")
}

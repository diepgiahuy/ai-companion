//go:build mcp

package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"
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
		if err := session.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func connectAndRegister(ctx context.Context, registry *capability.ToolRegistry, configs []ServerConfig) (Manager, error) {
	if registry == nil {
		return nil, fmt.Errorf("MCP tool registry is required")
	}
	m := &manager{}
	for _, config := range configs {
		if err := m.connectServer(ctx, registry, config); err != nil {
			_ = m.Close()
			return nil, err
		}
	}
	return m, nil
}

func (m *manager) connectServer(ctx context.Context, registry *capability.ToolRegistry, config ServerConfig) error {
	name := sanitizeName(config.Name)
	if name == "" {
		return fmt.Errorf("MCP server name is required")
	}
	httpClient, err := secureHTTPClient(config)
	if err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-companion", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   strings.TrimSpace(config.Endpoint),
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return fmt.Errorf("connect MCP server %s: %w", name, err)
	}

	registered := 0
	for remoteTool, err := range session.Tools(ctx, nil) {
		if err != nil {
			_ = session.Close()
			return fmt.Errorf("list MCP server %s tools: %w", name, err)
		}
		if remoteTool == nil || strings.TrimSpace(remoteTool.Name) == "" {
			continue
		}
		parameters, err := schemaMap(remoteTool.InputSchema)
		if err != nil {
			_ = session.Close()
			return fmt.Errorf("MCP server %s tool %s schema: %w", name, remoteTool.Name, err)
		}
		remoteName := remoteTool.Name
		localName := "mcp." + name + "." + sanitizeName(remoteName)
		pack := strings.TrimSpace(config.Pack)
		if pack == "" {
			pack = "external"
		}
		def := &capability.ToolDefinition{
			Name:        localName,
			Description: strings.TrimSpace(remoteTool.Description),
			Parameters:  parameters,
			Pack:        pack,
			Risk:        "external",
			Entitlement: strings.TrimSpace(config.Entitlement),
			FeatureKey:  strings.TrimSpace(config.FeatureKey),
		}
		tool := capability.FunctionTool{
			ToolName:       localName,
			ToolDefinition: def,
			Handler: func(callCtx context.Context, request capability.ToolRequest) capability.ToolResult {
				var arguments map[string]any
				rawArgs := strings.TrimSpace(request.Arguments)
				if rawArgs == "" {
					rawArgs = "{}"
				}
				if err := json.Unmarshal([]byte(rawArgs), &arguments); err != nil {
					return capability.Failure(fmt.Errorf("invalid external tool arguments"))
				}
				result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: remoteName, Arguments: arguments})
				if err != nil {
					return capability.Failure(fmt.Errorf("external MCP tool call failed"))
				}
				if result == nil || result.IsError {
					return capability.Failure(fmt.Errorf("external MCP tool returned an error"))
				}
				rawResult, err := json.Marshal(result)
				if err != nil {
					return capability.Failure(fmt.Errorf("encode external MCP tool result"))
				}
				return capability.Success(map[string]any{
					"source": "mcp",
					"server": name,
					"tool":   remoteName,
					"data":   json.RawMessage(rawResult),
				})
			},
		}
		if err := registry.Register(tool); err != nil {
			_ = session.Close()
			return fmt.Errorf("register MCP tool %s: %w", localName, err)
		}
		registered++
	}
	if registered == 0 {
		_ = session.Close()
		return fmt.Errorf("MCP server %s exposed no usable tools", name)
	}
	m.mu.Lock()
	m.sessions = append(m.sessions, session)
	m.mu.Unlock()
	return nil
}

func schemaMap(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "object", "additionalProperties": true}, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return map[string]any{"type": "object", "additionalProperties": true}, nil
	}
	return out, nil
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastSep := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastSep = false
			continue
		}
		if !lastSep {
			b.WriteByte('_')
			lastSep = true
		}
	}
	return strings.Trim(b.String(), "_")
}

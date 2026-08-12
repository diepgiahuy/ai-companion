package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/mcpbridge"
)

func configureMCP(registry *capability.ToolRegistry, startupTimeout time.Duration) (io.Closer, error) {
	raw := strings.TrimSpace(os.Getenv("COMPANION_MCP_SERVERS_JSON"))
	if raw == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var configs []mcpbridge.ServerConfig
	if err := decoder.Decode(&configs); err != nil {
		return nil, fmt.Errorf("decode COMPANION_MCP_SERVERS_JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("COMPANION_MCP_SERVERS_JSON contains trailing JSON")
		}
		return nil, fmt.Errorf("decode COMPANION_MCP_SERVERS_JSON trailing data: %w", err)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("COMPANION_MCP_SERVERS_JSON must contain at least one server")
	}
	if startupTimeout <= 0 || startupTimeout > 30*time.Second {
		startupTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	manager, err := mcpbridge.ConnectAndRegister(ctx, registry, configs)
	if err != nil {
		return nil, fmt.Errorf("connect MCP servers: %w", err)
	}
	return manager, nil
}

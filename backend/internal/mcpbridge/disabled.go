//go:build !mcp

package mcpbridge

import (
	"context"

	"companion-server/internal/capability"
)

type disabledManager struct{}
func (disabledManager) Close() error { return nil }

func connectAndRegister(_ context.Context, _ *capability.ToolRegistry, _ *capability.ResourceRegistry, configs []ServerConfig) (Manager, error) {
	if len(configs) == 0 { return disabledManager{}, nil }
	return nil, ErrNotBuilt
}

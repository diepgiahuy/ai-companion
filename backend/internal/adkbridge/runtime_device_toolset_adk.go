//go:build adk

package adkbridge

import (
	"context"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"companion-server/internal/capability"
)

// registryDeviceToolset projects the server-owned ToolRegistry into ADK for
// the authenticated current device. It decides visibility only. The returned
// adapters still execute through ToolRegistry.Execute(), which revalidates
// context availability, arguments and authorization.
type registryDeviceToolset struct {
	registry *capability.ToolRegistry
}

func (t registryDeviceToolset) Name() string { return "device_capabilities" }

func (t registryDeviceToolset) Tools(ctx adkagent.ReadonlyContext) ([]tool.Tool, error) {
	return t.toolsForContext(ctx)
}

func (t registryDeviceToolset) toolsForContext(ctx context.Context) ([]tool.Tool, error) {
	if t.registry == nil {
		return nil, nil
	}
	definitions := t.registry.DefinitionsForPacks([]string{"device"})
	out := make([]tool.Tool, 0, len(definitions))
	for _, definition := range definitions {
		if !t.registry.Available(ctx, definition.Name) {
			continue
		}
		adapted, err := adaptRegistryTool(t.registry, definition)
		if err != nil {
			return nil, err
		}
		out = append(out, adapted)
	}
	return out, nil
}

var _ tool.Toolset = registryDeviceToolset{}

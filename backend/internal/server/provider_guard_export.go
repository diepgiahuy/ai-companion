package server

import "companion-server/internal/pipeline"

// GuardProviderComponents applies one shared provider lifecycle guard to a
// process-level component set before sessions are created. A guard is therefore
// shared by all sessions and reconnects rather than recreated per WebSocket.
func GuardProviderComponents(components pipeline.Components) pipeline.Components {
	return guardProviderComponents(components, newProviderCallGuard(defaultProviderTimeouts()))
}

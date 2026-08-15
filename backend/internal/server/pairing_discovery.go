package server

// pairingDiscoveryTarget resolves the BLE-advertised pairing alias to the one
// currently authenticated device session. The alias is the server-owned
// WebSocket session_id: opaque, connection-scoped, and removed automatically
// when that session unregisters. Stable device IDs and credentials therefore
// never need to cross the BLE discovery boundary.
func (h *sessionHub) pairingDiscoveryTarget(discoveryID string) *session {
	if h == nil || discoveryID == "" {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sessions := range h.byDevice {
		for candidate := range sessions {
			if candidate != nil && candidate.id == discoveryID {
				return candidate
			}
		}
	}
	return nil
}

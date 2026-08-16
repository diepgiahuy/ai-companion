package server

import (
	"context"
	"fmt"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/protocol"
)

// DeviceOnline reports only whether the authenticated session hub currently has
// a live Protocol-v2 session for this owner/device pair. It does not infer
// connectivity from enrollment, desired state, or a previous report.
func (s *Server) DeviceOnline(userID, deviceID string) bool {
	if s == nil || s.hub == nil {
		return false
	}
	return len(s.hub.targets(userID, deviceID)) > 0
}

// DeliverRuntimeConfig sends the already-persisted desired revision over the
// one canonical Protocol-v2 settings transport. A successful write means only
// "delivered to a live session"; application success is recorded later from
// config.report and must never be inferred here.
func (s *Server) DeliverRuntimeConfig(ctx context.Context, userID, deviceID string, twin controlplane.Twin) (int, error) {
	if s == nil || s.hub == nil {
		return 0, nil
	}
	if twin.DesiredVersion <= 0 {
		return 0, fmt.Errorf("desired config version must be positive")
	}
	payload := protocol.ConfigUpdatePayload{
		ConfigVersion: twin.DesiredVersion,
		Config:        protocolConfig(twin.Desired),
	}
	targets := s.hub.targets(userID, deviceID)
	if len(targets) == 0 {
		return 0, nil
	}
	delivered := 0
	for _, target := range targets {
		if err := target.sendJSONMeta(ctx, protocol.ConfigUpdateType, protocol.Metadata{
			OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		}, payload); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

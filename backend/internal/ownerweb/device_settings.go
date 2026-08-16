package ownerweb

import (
	"context"
	"strings"

	"companion-server/internal/controlplane"
)

// DeviceSettingsDelivery is intentionally smaller than the server/session type.
// Owner Hub may observe live connectivity and request delivery of an already
// persisted desired revision, but it cannot mark a revision applied.
type DeviceSettingsDelivery interface {
	DeviceOnline(userID, deviceID string) bool
	DeliverRuntimeConfig(context.Context, string, string, controlplane.Twin) (int, error)
}

func (h *Handler) ownsDevice(ctx context.Context, userID, deviceID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || h == nil || h.deps.Store == nil {
		return false
	}
	devices, err := h.deps.Store.ListUserDevices(ctx, userID)
	if err != nil {
		return false
	}
	for _, device := range devices {
		if device.DeviceID == deviceID {
			return true
		}
	}
	return false
}

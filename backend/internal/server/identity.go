package server

import (
	"net/http"
	"strings"

	"companion-server/internal/domain"
)

// IdentityResolver supplies conversation-scoped transport metadata. Authenticated owner, tenant, plan and device claims are always overwritten from the enrolled per-device credential before a session starts.
type IdentityResolver interface {
	Resolve(request *http.Request, deviceID string) domain.Identity
}

type HeaderIdentityResolver struct{ DefaultUserID string }

func (r HeaderIdentityResolver) Resolve(request *http.Request, deviceID string) domain.Identity {
	userID := strings.TrimSpace(request.Header.Get("User-Id"))
	if userID == "" {
		userID = strings.TrimSpace(r.DefaultUserID)
		if userID == "" {
			userID = "default"
		}
	}
	threadID := strings.TrimSpace(request.Header.Get("Thread-Id"))
	if threadID == "" {
		threadID = strings.TrimSpace(deviceID)
		if threadID == "" {
			threadID = "default"
		}
	}
	return domain.Identity{UserID: userID, DeviceID: strings.TrimSpace(deviceID), ThreadID: threadID, TenantID: strings.TrimSpace(request.Header.Get("Tenant-Id")), Plan: strings.TrimSpace(request.Header.Get("Plan"))}
}

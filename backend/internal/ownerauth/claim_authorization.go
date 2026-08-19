package ownerauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AuthorizeClaimAuthorization validates a short-lived claim authorization without
// consuming it. Durable device-claim idempotency owns retry/replay semantics, so a
// lost response can retry until the authorization expires.
func (s *Service) AuthorizeClaimAuthorization(raw string) (ClaimAuthorization, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ClaimAuthorization{}, ErrInvalidClaim
	}
	key := tokenKey(raw)
	s.mu.Lock()
	claim, ok := s.claims[key]
	if ok && !claim.ExpiresAt.After(s.now()) {
		delete(s.claims, key)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return ClaimAuthorization{}, ErrInvalidClaim
	}
	return claim, nil
}

// MintBoundClaimAuthorization creates a short-lived browser claim authorization
// that is cryptographically bound to the exact bootstrap identity and device ID.
// The stored binding is a digest so neither identifier can be substituted later.
func (s *Service) MintBoundClaimAuthorization(rawSession, csrf, bootstrapID, deviceID string) (string, ClaimAuthorization, error) {
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if bootstrapID == "" || len(bootstrapID) > 128 || deviceID == "" || len(deviceID) > 128 {
		return "", ClaimAuthorization{}, fmt.Errorf("bootstrap_id and device_id are required and must be <=128 bytes")
	}
	return s.MintClaimAuthorization(rawSession, csrf, claimBinding(bootstrapID, deviceID))
}

// AuthorizeDeviceClaim verifies that raw authorizes this exact bootstrap/device
// pair and returns only the trusted owner identity needed by the claim service.
// Legacy browser authorizations remain process-local. Zero-typing approval
// authorizations are validated through the configured durable claim-session store
// so a backend restart or a request routed to another instance remains valid.
func (s *Service) AuthorizeDeviceClaim(raw, bootstrapID, deviceID string) (string, error) {
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if bootstrapID == "" || deviceID == "" {
		return "", ErrInvalidClaim
	}

	if claim, err := s.AuthorizeClaimAuthorization(raw); err == nil {
		want := claimBinding(bootstrapID, deviceID)
		if subtle.ConstantTimeCompare([]byte(claim.BootstrapID), []byte(want)) != 1 {
			return "", ErrInvalidClaim
		}
		return claim.UserID, nil
	}

	store := s.claimSessionStore()
	if store == nil {
		return "", ErrInvalidClaim
	}
	ownerUserID, err := store.AuthorizeClaim(context.Background(), raw, bootstrapID, deviceID, s.now())
	if err != nil || strings.TrimSpace(ownerUserID) == "" {
		return "", ErrInvalidClaim
	}
	return ownerUserID, nil
}

// HandleBoundClaimAuthorization is the product HTTP contract for minting claim
// authorizations. It deliberately requires both IDs before any long-lived device
// credential can be issued by the backend claim service.
func (s *Service) HandleBoundClaimAuthorization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		BootstrapID string `json:"bootstrap_id"`
		DeviceID    string `json:"device_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	raw, claim, err := s.MintBoundClaimAuthorization(
		sessionCookie(r),
		r.Header.Get("X-CSRF-Token"),
		request.BootstrapID,
		request.DeviceID,
	)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"claim_authorization": raw,
		"expires_at":          claim.ExpiresAt,
	})
}

func claimBinding(bootstrapID, deviceID string) string {
	digest := sha256.Sum256([]byte("companion-device-claim-v1\x00" + bootstrapID + "\x00" + deviceID))
	return hex.EncodeToString(digest[:])
}

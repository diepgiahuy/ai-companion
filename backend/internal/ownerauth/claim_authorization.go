package ownerauth

import "strings"

// AuthorizeClaimAuthorization validates a short-lived claim authorization without
// consuming it. Durable device-claim idempotency owns retry/replay semantics, so a
// lost response can retry until the authorization expires.
func (s *Service) AuthorizeClaimAuthorization(raw string) (ClaimAuthorization, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" { return ClaimAuthorization{}, ErrInvalidClaim }
	key := tokenKey(raw)
	s.mu.Lock()
	claim, ok := s.claims[key]
	if ok && !claim.ExpiresAt.After(s.now()) {
		delete(s.claims, key)
		ok = false
	}
	s.mu.Unlock()
	if !ok { return ClaimAuthorization{}, ErrInvalidClaim }
	return claim, nil
}

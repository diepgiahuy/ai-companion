package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

const (
	pairingDiscoveryPrefix       = "CP-"
	pairingDiscoverySlotDuration = 30 * time.Second
	pairingDiscoveryPastSlots    = 4
	pairingDiscoveryFutureSlots  = 1
	pairingDiscoveryDigestBytes  = 10
)

var pairingDiscoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// pairingDiscoveryID derives a short-lived BLE pseudonym from the already
// authenticated WebSocket session ID. The stable owner/device identity and the
// long-lived credential never cross the radio boundary. The alias is discovery
// only; normal session authentication and bilateral confirmation remain the
// authority for relationship creation.
func pairingDiscoveryID(sessionID string, at time.Time) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) < 8 || len(sessionID) > 128 {
		return "", fmt.Errorf("invalid pairing discovery session")
	}
	slot := at.UTC().Unix() / int64(pairingDiscoverySlotDuration/time.Second)
	mac := hmac.New(sha256.New, []byte(sessionID))
	_, _ = fmt.Fprintf(mac, "companion-pairing-v1:%d", slot)
	digest := mac.Sum(nil)
	return pairingDiscoveryPrefix + pairingDiscoveryEncoding.EncodeToString(digest[:pairingDiscoveryDigestBytes]), nil
}

func validPairingDiscoveryID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len(pairingDiscoveryPrefix)+16 || !strings.HasPrefix(value, pairingDiscoveryPrefix) {
		return false
	}
	for _, c := range value[len(pairingDiscoveryPrefix):] {
		if (c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7') {
			continue
		}
		return false
	}
	return true
}

// pairingDiscoveryTarget resolves only currently authenticated sessions and a
// bounded rolling time window. No alias is persisted. More than one match is
// ambiguous and therefore fails closed.
func (h *sessionHub) pairingDiscoveryTarget(discoveryID string) *session {
	if h == nil || !validPairingDiscoveryID(discoveryID) {
		return nil
	}
	now := time.Now().UTC()
	h.mu.RLock()
	defer h.mu.RUnlock()
	var matched *session
	for _, sessions := range h.byDevice {
		for candidate := range sessions {
			if candidate == nil {
				continue
			}
			candidateMatches := false
			for slotOffset := -pairingDiscoveryPastSlots; slotOffset <= pairingDiscoveryFutureSlots; slotOffset++ {
				expected, err := pairingDiscoveryID(candidate.id, now.Add(time.Duration(slotOffset)*pairingDiscoverySlotDuration))
				if err != nil {
					continue
				}
				if hmac.Equal([]byte(expected), []byte(discoveryID)) {
					candidateMatches = true
					break
				}
			}
			if !candidateMatches {
				continue
			}
			if matched != nil && matched != candidate {
				return nil
			}
			matched = candidate
		}
	}
	return matched
}

package pgstore

import (
	"context"
	"fmt"
	"time"

	"companion-server/internal/pairing"
)

// ResolvePairingCandidate deliberately performs a bounded control-plane scan of
// active credential verifiers for Production-v1's small device population. The
// rotating BLE pseudonym is never indexed as stable identity and is never an
// authentication credential. A future large-fleet deployment may replace this
// implementation behind the repository boundary with an ephemeral resolver.
func (s *Store) ResolvePairingCandidate(ctx context.Context, discoveryID string, now time.Time) (pairing.Participant, error) {
	if !pairing.ValidDiscoveryID(discoveryID) {
		return pairing.Participant{}, pairing.ErrDeviceUnavailable
	}
	rows, err := s.pool.Query(ctx, `
		SELECT device_id,user_id,token_sha256
		FROM device_credentials
		WHERE status='active'`)
	if err != nil {
		return pairing.Participant{}, fmt.Errorf("list active pairing candidates: %w", err)
	}
	defer rows.Close()

	var matched pairing.Participant
	for rows.Next() {
		var deviceID, userID, tokenSHA256 string
		if err := rows.Scan(&deviceID, &userID, &tokenSHA256); err != nil {
			return pairing.Participant{}, fmt.Errorf("scan pairing candidate: %w", err)
		}
		match := false
		for slotOffset := -pairing.DiscoveryPastSlots; slotOffset <= pairing.DiscoveryFutureSlots; slotOffset++ {
			expected, err := pairing.DiscoveryIDFromCredentialHash(
				tokenSHA256, now.Add(time.Duration(slotOffset)*pairing.DiscoverySlotDuration))
			if err != nil {
				return pairing.Participant{}, fmt.Errorf("derive pairing discovery id: %w", err)
			}
			if expected == discoveryID {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		candidate := pairing.Participant{UserID: userID, DeviceID: deviceID}
		if matched.DeviceID != "" && matched.DeviceID != candidate.DeviceID {
			// Collision or duplicated verifier: discovery cannot authorize a peer.
			return pairing.Participant{}, pairing.ErrDeviceUnavailable
		}
		matched = candidate
	}
	if err := rows.Err(); err != nil {
		return pairing.Participant{}, fmt.Errorf("iterate pairing candidates: %w", err)
	}
	if matched.DeviceID == "" {
		return pairing.Participant{}, pairing.ErrDeviceUnavailable
	}
	return matched, nil
}

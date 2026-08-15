package pairing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	DiscoveryPrefix       = "CP-"
	DiscoverySlotDuration = 30 * time.Second
	discoveryDigestBytes  = 10 // 80-bit rotating pseudonym; not authentication.
)

var discoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// DiscoveryIDFromCredentialHash derives the BLE pseudonym from the stored
// SHA-256 verifier, never from a stable device ID. The pseudonym is a coarse
// discovery hint only: backend device authentication and bilateral pairing
// confirmation remain authoritative.
func DiscoveryIDFromCredentialHash(tokenSHA256 string, at time.Time) (string, error) {
	tokenSHA256 = strings.TrimSpace(tokenSHA256)
	key, err := hex.DecodeString(tokenSHA256)
	if err != nil || len(key) != sha256.Size {
		return "", fmt.Errorf("device credential verifier must be a SHA-256 digest")
	}
	slot := at.UTC().Unix() / int64(DiscoverySlotDuration/time.Second)
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "companion-pairing-v1:%d", slot)
	digest := mac.Sum(nil)
	return DiscoveryPrefix + discoveryEncoding.EncodeToString(digest[:discoveryDigestBytes]), nil
}

func ValidDiscoveryID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len(DiscoveryPrefix)+16 || !strings.HasPrefix(value, DiscoveryPrefix) {
		return false
	}
	for _, c := range value[len(DiscoveryPrefix):] {
		if (c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7') {
			continue
		}
		return false
	}
	return true
}

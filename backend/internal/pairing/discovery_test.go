package pairing

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestDiscoveryIDRotatesAndValidates(t *testing.T) {
	digest := sha256.Sum256([]byte("credential-secret"))
	verifier := hex.EncodeToString(digest[:])
	base := time.Unix(1_800_000_000, 0).UTC()
	first, err := DiscoveryIDFromCredentialHash(verifier, base)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidDiscoveryID(first) {
		t.Fatalf("invalid generated discovery id %q", first)
	}
	sameSlot, err := DiscoveryIDFromCredentialHash(verifier, base.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sameSlot != first {
		t.Fatalf("same slot rotated: %q vs %q", first, sameSlot)
	}
	next, err := DiscoveryIDFromCredentialHash(verifier, base.Add(DiscoverySlotDuration))
	if err != nil {
		t.Fatal(err)
	}
	if next == first {
		t.Fatal("discovery id did not rotate across time slot")
	}
	if ValidDiscoveryID("device-stable-id") || ValidDiscoveryID("CP-AAAAAAAAAAAAAAA1") {
		t.Fatal("accepted non-canonical discovery id")
	}
	if _, err := DiscoveryIDFromCredentialHash("not-a-verifier", base); err == nil {
		t.Fatal("accepted invalid credential verifier")
	}
}

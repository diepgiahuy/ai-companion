package ownerauth

import (
	"testing"
	"time"
)

func TestHumanClaimCodeConcurrentSameDeviceRedemptionConverges(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	service, session, csrf := humanClaimTestService(&now)
	code, _, err := service.MintBoundHumanClaimCode(session, csrf, "bootstrap-race", "device-race")
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		auth string
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			auth, _, redeemErr := service.RedeemBoundHumanClaimCode(
				code, "bootstrap-race", "device-race", "stable-device-redemption-id",
			)
			results <- result{auth: auth, err: redeemErr}
		}()
	}
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent redemption errors: %v / %v", first.err, second.err)
	}
	if first.auth == "" || first.auth != second.auth {
		t.Fatalf("concurrent redemption diverged: %q / %q", first.auth, second.auth)
	}
}

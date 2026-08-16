package ownerauth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func humanClaimTestService(now *time.Time) (*Service, string, string) {
	rawSession := "owner-session"
	csrf := "owner-csrf"
	return &Service{
		cfg:      Config{ClaimTTL: 5 * time.Minute},
		now:      func() time.Time { return *now },
		logins:   make(map[string]loginTransaction),
		sessions: map[string]sessionRecord{tokenKey(rawSession): {
			Session:  Session{UserID: "owner-1", ExpiresAt: now.Add(time.Hour)},
			CSRFHash: sha256.Sum256([]byte(csrf)),
		}},
		claims: make(map[string]ClaimAuthorization),
	}, rawSession, csrf
}

func TestHumanClaimCodeRedeemsRetrySafeIntoBoundOpaqueAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	service, session, csrf := humanClaimTestService(&now)

	code, claim, err := service.MintBoundHumanClaimCode(session, csrf, "bootstrap-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != humanClaimCodeLength+1 || code[5] != '-' || !claim.ExpiresAt.After(now) {
		t.Fatalf("unexpected human code %q claim=%+v", code, claim)
	}
	if _, err := service.AuthorizeDeviceClaim(code, "bootstrap-1", "device-1"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("human code bypassed redemption boundary: %v", err)
	}
	if _, _, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-1", "device-2", "device-retry-1"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("wrong-device redemption err=%v, want ErrInvalidClaim", err)
	}

	authorization, redeemed, err := service.RedeemBoundHumanClaimCode(strings.ToLower(code), "bootstrap-1", "device-1", "device-retry-1")
	if err != nil {
		t.Fatal(err)
	}
	if authorization == "" || authorization == code || redeemed.UserID != "owner-1" {
		t.Fatalf("authorization=%q redeemed=%+v", authorization, redeemed)
	}

	// Lost HTTP response / reboot retry: same device retry identity gets the same
	// opaque authorization, not a second authorization and not a dead code.
	replayedAuthorization, replayed, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-1", "device-1", "device-retry-1")
	if err != nil || replayedAuthorization != authorization || replayed != redeemed {
		t.Fatalf("retry authorization=%q claim=%+v err=%v", replayedAuthorization, replayed, err)
	}
	if _, _, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-1", "device-1", "attacker-retry"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("different redemption identity replay err=%v, want ErrInvalidClaim", err)
	}

	owner, err := service.AuthorizeDeviceClaim(authorization, "bootstrap-1", "device-1")
	if err != nil || owner != "owner-1" {
		t.Fatalf("opaque authorization owner=%q err=%v", owner, err)
	}
	if _, err := service.AuthorizeDeviceClaim(authorization, "bootstrap-2", "device-1"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("redeemed authorization lost bootstrap binding: %v", err)
	}
}

func TestHumanClaimCodeRejectsExpiredAndRateLimitsOnlineGuessing(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	service, session, csrf := humanClaimTestService(&now)
	code, _, err := service.MintBoundHumanClaimCode(session, csrf, "bootstrap-expire", "device-expire")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if _, _, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-expire", "device-expire", "device-expire-retry"); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expired code err=%v, want ErrInvalidClaim", err)
	}

	remote := "198.51.100.77:4242"
	for i := 0; i < claimCodeAttemptsPerMinute; i++ {
		if !allowClaimCodeAttempt(remote, now) {
			t.Fatalf("attempt %d unexpectedly rate limited", i+1)
		}
	}
	if allowClaimCodeAttempt(remote, now) {
		t.Fatal("online guessing exceeded per-minute limit")
	}
	now = now.Add(time.Minute)
	if !allowClaimCodeAttempt(remote, now) {
		t.Fatal("rate-limit window did not reset")
	}
}

func TestHumanClaimCodeHTTPRequiresOwnerCSRFAndNeverReturnsCredential(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	service, session, csrf := humanClaimTestService(&now)

	unauthorized := httptest.NewRecorder()
	service.HandleHumanClaimCode(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/owner/device-claim-code", nil))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Sign in") {
		t.Fatalf("unauthorized page status=%d body=%q", unauthorized.Code, unauthorized.Body.String())
	}

	body := `{"bootstrap_id":"bootstrap-http","device_id":"device-http"}`
	missingCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-code", strings.NewReader(body))
	missingCSRF.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	missingRecorder := httptest.NewRecorder()
	service.HandleHumanClaimCode(missingRecorder, missingCSRF)
	if missingRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing CSRF status=%d", missingRecorder.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-code", strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	request.Header.Set("X-CSRF-Token", csrf)
	recorder := httptest.NewRecorder()
	service.HandleHumanClaimCode(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mint status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var minted map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	code, ok := minted["claim_code"].(string)
	if !ok || code == "" {
		t.Fatalf("mint response=%v", minted)
	}
	if _, exists := minted["device_credential"]; exists {
		t.Fatal("owner handoff exposed long-lived device credential")
	}

	redeemBody := `{"claim_code":"` + code + `","bootstrap_id":"bootstrap-http","device_id":"device-http","redemption_id":"device-retry-http"}`
	redeem := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-codes/redeem", strings.NewReader(redeemBody))
	redeem.RemoteAddr = "203.0.113.88:8080"
	redeemRecorder := httptest.NewRecorder()
	service.HandleHumanClaimCodeRedeem(redeemRecorder, redeem)
	if redeemRecorder.Code != http.StatusOK {
		t.Fatalf("redeem status=%d body=%q", redeemRecorder.Code, redeemRecorder.Body.String())
	}
	if strings.Contains(redeemRecorder.Body.String(), "device_credential") {
		t.Fatal("redemption exposed long-lived device credential")
	}

	replay := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-codes/redeem", strings.NewReader(redeemBody))
	replay.RemoteAddr = "203.0.113.88:8080"
	replayRecorder := httptest.NewRecorder()
	service.HandleHumanClaimCodeRedeem(replayRecorder, replay)
	if replayRecorder.Code != http.StatusOK || replayRecorder.Body.String() != redeemRecorder.Body.String() {
		t.Fatalf("idempotent replay status=%d body=%q first=%q", replayRecorder.Code, replayRecorder.Body.String(), redeemRecorder.Body.String())
	}

	attackerBody := `{"claim_code":"` + code + `","bootstrap_id":"bootstrap-http","device_id":"device-http","redemption_id":"different-retry"}`
	attacker := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-codes/redeem", strings.NewReader(attackerBody))
	attacker.RemoteAddr = "203.0.113.89:8080"
	attackerRecorder := httptest.NewRecorder()
	service.HandleHumanClaimCodeRedeem(attackerRecorder, attacker)
	if attackerRecorder.Code != http.StatusGone {
		t.Fatalf("different redemption replay status=%d body=%q", attackerRecorder.Code, attackerRecorder.Body.String())
	}
}

func TestClaimCodeStoreIntegration(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	store := NewMemoryClaimCodeStore()
	rawSession := "owner-session"
	csrf := "owner-csrf"
	service := &Service{
		cfg: Config{
			ClaimTTL:       5 * time.Minute,
			ClaimCodeStore: store,
		},
		now:      func() time.Time { return now },
		logins:   make(map[string]loginTransaction),
		sessions: map[string]sessionRecord{tokenKey(rawSession): {
			Session:  Session{UserID: "owner-store", ExpiresAt: now.Add(time.Hour)},
			CSRFHash: sha256.Sum256([]byte(csrf)),
		}},
		claims: make(map[string]ClaimAuthorization),
	}

	code, claim, err := service.MintBoundHumanClaimCode(rawSession, csrf, "bootstrap-store", "device-store")
	if err != nil {
		t.Fatal(err)
	}
	if !claim.ExpiresAt.After(now) {
		t.Fatal("invalid claim expiration")
	}

	auth, redeemed, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-store", "device-store", "retry-store-1")
	if err != nil {
		t.Fatal(err)
	}
	if auth == "" || redeemed.UserID != "owner-store" {
		t.Fatalf("auth=%q redeemed=%+v", auth, redeemed)
	}

	// Replay through store
	replayedAuth, replayedClaim, err := service.RedeemBoundHumanClaimCode(code, "bootstrap-store", "device-store", "retry-store-1")
	if err != nil || replayedAuth != auth || replayedClaim != redeemed {
		t.Fatalf("replay via store failed: auth=%q err=%v", replayedAuth, err)
	}

	// Rate limiter through store
	remote := "192.0.2.1:1234"
	for i := 0; i < claimCodeAttemptsPerMinute; i++ {
		if !service.allowClaimCodeAttempt(remote, now) {
			t.Fatalf("store rate limiter failed at attempt %d", i+1)
		}
	}
	if service.allowClaimCodeAttempt(remote, now) {
		t.Fatal("store rate limiter failed to block excess attempt")
	}
}


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

func handoffTestService(t *testing.T) (*Service, string, string, *time.Time) {
	t.Helper()
	service, err := New(Config{
		AuthorizationURL: "https://owner.example/authorize",
		TokenURL:         "https://owner.example/token",
		UserInfoURL:      "https://owner.example/userinfo",
		ClientID:         "companion-web",
		RedirectURL:      "https://companion.example/v1/owner/auth/callback",
		Scopes:           []string{"openid"},
		ClaimTTL:         5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	session := "owner-session"
	csrf := "owner-csrf"
	service.sessions[tokenKey(session)] = sessionRecord{
		Session:  Session{UserID: "owner-1", ExpiresAt: now.Add(time.Hour)},
		CSRFHash: sha256.Sum256([]byte(csrf)),
	}
	return service, session, csrf, &now
}

func TestHumanClaimCodeBoundOneTimeExpiryAndRateLimit(t *testing.T) {
	service, session, csrf, now := handoffTestService(t)
	handoff, err := NewHandoff(service)
	if err != nil {
		t.Fatal(err)
	}
	handoff.now = func() time.Time { return *now }

	code, expiresAt, err := handoff.Mint(session, csrf, "bootstrap-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 11 || code[5] != '-' || !expiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("code=%q expires=%s", code, expiresAt)
	}
	if _, _, err := handoff.Exchange("192.0.2.10", code, "bootstrap-1", "device-other"); !errors.Is(err, ErrInvalidHumanClaimCode) {
		t.Fatalf("wrong binding err=%v", err)
	}
	authorization, authExpiry, err := handoff.Exchange("192.0.2.10", code, "bootstrap-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if authorization == "" || authorization == code || !authExpiry.Equal(expiresAt) {
		t.Fatalf("authorization/expires not preserved")
	}
	owner, err := service.AuthorizeDeviceClaim(authorization, "bootstrap-1", "device-1")
	if err != nil || owner != "owner-1" {
		t.Fatalf("bound authorization owner=%q err=%v", owner, err)
	}
	if _, _, err := handoff.Exchange("192.0.2.10", code, "bootstrap-1", "device-1"); !errors.Is(err, ErrInvalidHumanClaimCode) {
		t.Fatalf("replayed code err=%v", err)
	}

	expiringCode, _, err := handoff.Mint(session, csrf, "bootstrap-expire", "device-expire")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(6 * time.Minute)
	if _, _, err := handoff.Exchange("192.0.2.11", expiringCode, "bootstrap-expire", "device-expire"); !errors.Is(err, ErrInvalidHumanClaimCode) {
		t.Fatalf("expired code err=%v", err)
	}

	for attempt := 0; attempt < claimExchangeLimit; attempt++ {
		if _, _, err := handoff.Exchange("192.0.2.12", "AAAAA-AAAAA", "bootstrap-x", "device-x"); !errors.Is(err, ErrInvalidHumanClaimCode) {
			t.Fatalf("attempt %d err=%v", attempt, err)
		}
	}
	if _, _, err := handoff.Exchange("192.0.2.12", "AAAAA-AAAAA", "bootstrap-x", "device-x"); !errors.Is(err, ErrHumanClaimRateLimited) {
		t.Fatalf("rate limit err=%v", err)
	}
}

func TestOwnerHandoffHTTPRequiresSessionCSRFAndNeverRendersCredential(t *testing.T) {
	service, session, csrf, now := handoffTestService(t)
	handoff, err := NewHandoff(service)
	if err != nil {
		t.Fatal(err)
	}
	handoff.now = func() time.Time { return *now }

	unauthorized := httptest.NewRecorder()
	handoff.HandleOwnerPage(unauthorized, httptest.NewRequest(http.MethodGet, "https://companion.example/v1/owner/device-claim", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized page status=%d", unauthorized.Code)
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "https://companion.example/v1/owner/device-claim", nil)
	pageRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	page := httptest.NewRecorder()
	handoff.HandleOwnerPage(page, pageRequest)
	if page.Code != http.StatusOK {
		t.Fatalf("page status=%d body=%s", page.Code, page.Body.String())
	}
	lower := strings.ToLower(page.Body.String())
	if strings.Contains(lower, "claim_authorization") || strings.Contains(lower, "device_credential") {
		t.Fatal("owner page rendered an opaque authorization or device credential field")
	}

	mintWithoutCSRF := httptest.NewRequest(http.MethodPost, "https://companion.example/v1/owner/claim-codes", strings.NewReader(`{"bootstrap_id":"boot","device_id":"dev"}`))
	mintWithoutCSRF.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	noCSRF := httptest.NewRecorder()
	handoff.HandleMint(noCSRF, mintWithoutCSRF)
	if noCSRF.Code != http.StatusUnauthorized {
		t.Fatalf("mint without csrf status=%d", noCSRF.Code)
	}

	mintRequest := httptest.NewRequest(http.MethodPost, "https://companion.example/v1/owner/claim-codes", strings.NewReader(`{"bootstrap_id":"boot","device_id":"dev"}`))
	mintRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	mintRequest.Header.Set("X-CSRF-Token", csrf)
	mint := httptest.NewRecorder()
	handoff.HandleMint(mint, mintRequest)
	if mint.Code != http.StatusOK {
		t.Fatalf("mint status=%d body=%s", mint.Code, mint.Body.String())
	}
	var minted map[string]any
	if err := json.Unmarshal(mint.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	code, _ := minted["claim_code"].(string)
	if code == "" || minted["claim_authorization"] != nil {
		t.Fatalf("mint response=%v", minted)
	}

	exchangeRequest := httptest.NewRequest(http.MethodPost, "https://companion.example/v1/owner/claim-code-exchanges", strings.NewReader(`{"claim_code":"`+code+`","bootstrap_id":"boot","device_id":"dev"}`))
	exchangeRequest.RemoteAddr = "192.0.2.50:43123"
	exchange := httptest.NewRecorder()
	handoff.HandleExchange(exchange, exchangeRequest)
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", exchange.Code, exchange.Body.String())
	}
	var exchanged map[string]any
	if err := json.Unmarshal(exchange.Body.Bytes(), &exchanged); err != nil {
		t.Fatal(err)
	}
	if exchanged["claim_authorization"] == nil || exchanged["claim_code"] != nil {
		t.Fatalf("exchange response=%v", exchanged)
	}

	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "https://companion.example/v1/owner/claim-code-exchanges", strings.NewReader(`{"claim_code":"`+code+`","bootstrap_id":"boot","device_id":"dev"}`))
	replayRequest.RemoteAddr = "192.0.2.50:43124"
	handoff.HandleExchange(replay, replayRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d", replay.Code)
	}
}

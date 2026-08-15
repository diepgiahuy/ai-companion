package ownerauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCEOwnerSessionAndOneTimeClaim(t *testing.T) {
	var expectedChallenge string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "provider-code" || r.Form.Get("client_id") != "companion-web" {
				t.Fatalf("unexpected token form: %v", r.Form)
			}
			verifier := r.Form.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			if base64.RawURLEncoding.EncodeToString(digest[:]) != expectedChallenge {
				t.Fatal("PKCE verifier does not match authorization challenge")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "provider-token", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer provider-token" {
				t.Fatalf("unexpected auth header %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sub": "owner-123"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	service, err := New(Config{
		AuthorizationURL: provider.URL + "/authorize",
		TokenURL:         provider.URL + "/token",
		UserInfoURL:      provider.URL + "/userinfo",
		ClientID:         "companion-web",
		RedirectURL:      "https://companion.example/v1/owner/auth/callback",
		Scopes:           []string{"openid", "profile"},
		LoginTTL:         time.Minute,
		SessionTTL:       time.Hour,
		ClaimTTL:         2 * time.Minute,
		HTTPClient:       provider.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	authorizationURL, err := service.BeginLogin()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" || query.Get("state") == "" || query.Get("code_challenge") == "" {
		t.Fatalf("invalid authorization request: %s", authorizationURL)
	}
	expectedChallenge = query.Get("code_challenge")

	rawSession, csrf, session, err := service.CompleteLogin(context.Background(), query.Get("state"), "provider-code")
	if err != nil {
		t.Fatal(err)
	}
	if session.UserID != "owner-123" || rawSession == "" || csrf == "" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if _, err := service.Authenticate(rawSession); err != nil {
		t.Fatalf("session authenticate: %v", err)
	}
	if _, err := service.AuthenticateMutation(rawSession, "wrong"); err != ErrUnauthorized {
		t.Fatalf("wrong csrf: got %v", err)
	}
	if _, err := service.AuthenticateMutation(rawSession, csrf); err != nil {
		t.Fatalf("csrf authenticate: %v", err)
	}

	claimToken, claim, err := service.MintClaimAuthorization(rawSession, csrf, "bootstrap-abc")
	if err != nil {
		t.Fatal(err)
	}
	if claim.UserID != "owner-123" || claim.BootstrapID != "bootstrap-abc" || claimToken == "" {
		t.Fatalf("unexpected claim: %+v", claim)
	}
	consumed, err := service.ConsumeClaimAuthorization(claimToken)
	if err != nil || consumed != claim {
		t.Fatalf("consume claim: %+v %v", consumed, err)
	}
	if _, err := service.ConsumeClaimAuthorization(claimToken); err != ErrInvalidClaim {
		t.Fatalf("claim replay should fail, got %v", err)
	}
	if _, _, _, err := service.CompleteLogin(context.Background(), query.Get("state"), "provider-code"); err != ErrInvalidState {
		t.Fatalf("oauth state replay should fail, got %v", err)
	}
	if err := service.Logout(rawSession, csrf); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(rawSession); err != ErrUnauthorized {
		t.Fatalf("logged out session should fail, got %v", err)
	}
}

func TestExpiryFailsClosed(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"access_token\":\"token\",\"token_type\":\"Bearer\"}"))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"sub\":\"owner-expiry\"}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	service, err := New(Config{
		AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
		UserInfoURL: provider.URL + "/userinfo", ClientID: "client",
		RedirectURL: "https://companion.example/callback", Scopes: []string{"openid"},
		LoginTTL: time.Minute, SessionTTL: time.Minute, ClaimTTL: time.Minute,
		HTTPClient: provider.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	authURL, err := service.BeginLogin()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	rawSession, csrf, _, err := service.CompleteLogin(context.Background(), parsed.Query().Get("state"), "code")
	if err != nil {
		t.Fatal(err)
	}
	claimToken, _, err := service.MintClaimAuthorization(rawSession, csrf, "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := service.Authenticate(rawSession); err != ErrUnauthorized {
		t.Fatalf("expired session should fail, got %v", err)
	}
	if _, err := service.ConsumeClaimAuthorization(claimToken); err != ErrInvalidClaim {
		t.Fatalf("expired claim should fail, got %v", err)
	}
}

func TestConfigurationRejectsInsecureProviderAndMissingOpenID(t *testing.T) {
	_, err := New(Config{
		AuthorizationURL: "http://idp.example/authorize", TokenURL: "https://idp.example/token",
		UserInfoURL: "https://idp.example/userinfo", ClientID: "client",
		RedirectURL: "https://companion.example/callback", Scopes: []string{"openid"},
	})
	if err == nil || !strings.Contains(err.Error(), "https required") {
		t.Fatalf("expected insecure URL rejection, got %v", err)
	}
	_, err = New(Config{
		AuthorizationURL: "https://idp.example/authorize", TokenURL: "https://idp.example/token",
		UserInfoURL: "https://idp.example/userinfo", ClientID: "client",
		RedirectURL: "https://companion.example/callback", Scopes: []string{"profile"},
	})
	if err == nil || !strings.Contains(err.Error(), "openid scope") {
		t.Fatalf("expected openid scope rejection, got %v", err)
	}
}

func TestHTTPMutationRequiresCSRF(t *testing.T) {
	service := &Service{
		cfg: Config{ClaimTTL: time.Minute}, now: func() time.Time { return time.Now().UTC() },
		logins: map[string]loginTransaction{}, sessions: map[string]sessionRecord{}, claims: map[string]ClaimAuthorization{},
	}
	rawSession := "session-token"
	csrf := "csrf-token"
	expires := time.Now().Add(time.Hour).UTC()
	service.sessions[tokenKey(rawSession)] = sessionRecord{Session: Session{UserID: "owner", ExpiresAt: expires}, CSRFHash: sha256.Sum256([]byte(csrf))}

	request := httptest.NewRequest(http.MethodPost, "/v1/owner/claim-authorizations", strings.NewReader("{\"bootstrap_id\":\"boot\"}"))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing csrf got status %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/owner/claim-authorizations", strings.NewReader("{\"bootstrap_id\":\"boot\"}"))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	request.Header.Set("X-CSRF-Token", csrf)
	response = httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("claim auth response status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

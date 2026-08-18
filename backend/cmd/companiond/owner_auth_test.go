package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnerAuthUnconfiguredFallsThrough(t *testing.T) {
	for _, key := range []string{
		"COMPANION_OWNER_OIDC_AUTH_URL", "COMPANION_OWNER_OIDC_TOKEN_URL",
		"COMPANION_OWNER_OIDC_USERINFO_URL", "COMPANION_OWNER_OIDC_CLIENT_ID",
		"COMPANION_OWNER_OIDC_CLIENT_SECRET", "COMPANION_OWNER_OIDC_REDIRECT_URL",
		"COMPANION_OWNER_OIDC_SCOPES",
	} {
		t.Setenv(key, "")
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := ownerAuthFromEnvironment(next, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unconfigured owner auth must fall through, got %d", response.Code)
	}
}

func TestOwnerAuthInvalidConfigurationFailsClosedOnlyForOwnerSurface(t *testing.T) {
	t.Setenv("COMPANION_OWNER_OIDC_CLIENT_ID", "configured-but-incomplete")
	t.Setenv("COMPANION_OWNER_OIDC_AUTH_URL", "")
	t.Setenv("COMPANION_OWNER_OIDC_TOKEN_URL", "")
	t.Setenv("COMPANION_OWNER_OIDC_USERINFO_URL", "")
	t.Setenv("COMPANION_OWNER_OIDC_REDIRECT_URL", "")
	t.Setenv("COMPANION_OWNER_OIDC_SCOPES", "openid")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := ownerAuthFromEnvironment(next, nil, nil, nil, nil, nil)

	ownerResponse := httptest.NewRecorder()
	handler.ServeHTTP(ownerResponse, httptest.NewRequest(http.MethodGet, "/v1/owner/session", nil))
	if ownerResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid configured owner auth must fail closed, got %d", ownerResponse.Code)
	}

	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthResponse.Code != http.StatusNoContent {
		t.Fatalf("non-owner route should preserve existing handler, got %d", healthResponse.Code)
	}
}

func TestOwnerScopesDefaultsAndParses(t *testing.T) {
	defaults := ownerScopes("")
	if len(defaults) != 2 || defaults[0] != "openid" || defaults[1] != "profile" {
		t.Fatalf("unexpected default scopes: %v", defaults)
	}
	parsed := ownerScopes("openid,profile email\tcustom")
	if len(parsed) != 4 || parsed[0] != "openid" || parsed[3] != "custom" {
		t.Fatalf("unexpected parsed scopes: %v", parsed)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"companion-server/internal/pgstore"
)

func clearOwnerOIDCEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"COMPANION_OWNER_OIDC_AUTH_URL", "COMPANION_OWNER_OIDC_TOKEN_URL",
		"COMPANION_OWNER_OIDC_USERINFO_URL", "COMPANION_OWNER_OIDC_CLIENT_ID",
		"COMPANION_OWNER_OIDC_CLIENT_SECRET", "COMPANION_OWNER_OIDC_REDIRECT_URL",
		"COMPANION_OWNER_OIDC_SCOPES",
	} {
		t.Setenv(key, "")
	}
}

func TestOwnerAuthUnconfiguredFallsThrough(t *testing.T) {
	clearOwnerOIDCEnv(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := ownerAuthFromEnvironment(next, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unconfigured owner auth must fall through for non-owner routes, got %d", response.Code)
	}
}

func TestOwnerAuthUnconfiguredFailsClosedForOwnerSurfaceWithStore(t *testing.T) {
	clearOwnerOIDCEnv(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := ownerAuthFromEnvironment(next, new(pgstore.Store), nil, nil, nil, nil)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/owner/dashboard"},
		{method: http.MethodPost, path: "/v1/owner/data/budget"},
		{method: http.MethodPost, path: "/v1/device-claim-sessions"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("unconfigured owner auth must fail closed for %s %s, got %d", tc.method, tc.path, response.Code)
			}
		})
	}

	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthResponse.Code != http.StatusNoContent {
		t.Fatalf("non-owner route should preserve existing handler, got %d", healthResponse.Code)
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

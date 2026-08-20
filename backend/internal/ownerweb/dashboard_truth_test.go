package ownerweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardUsesSimplifiedTruthfulProductSurface(t *testing.T) {
	for _, view := range []string{"home", "companion", "personal", "settings"} {
		if !strings.Contains(dashboardHTML, `data-view="`+view+`"`) {
			t.Fatalf("dashboard missing top-level %s view", view)
		}
	}

	for _, required := range []string{
		"Requested",
		"Applied",
		"Smart VAD",
		"Monthly budget",
		"Savings goal",
		"Not set",
		"Voice Memos",
		"Reminders & Timers",
		"Save privacy settings",
		"Settings synced",
		"Applying settings",
		"Will apply when Companion reconnects",
		"Couldn't apply settings",
		"Device report is stale",
		"Status unavailable",
	} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("dashboard missing required truthful UI marker %q", required)
		}
	}

	for _, forbidden := range []string{
		"/v1/owner/data/device/claim",
		"Claim Device",
		"v2.4.1",
		"SHA256 verified",
		"160.5 KiB",
		"PSRAM Codec",
		"10000000",
		"toggleAudio(",
		"checkOTA(",
		"triggerOTA(",
		"custom_phrase",
	} {
		if strings.Contains(dashboardHTML, forbidden) {
			t.Fatalf("dashboard contains stale/fabricated UI marker %q", forbidden)
		}
	}

	if strings.Contains(dashboardHTML, "fetch('/v1/owner/data/") || strings.Contains(dashboardHTML, `fetch("/v1/owner/data/`) {
		t.Fatal("Owner Hub data mutations must not bypass the canonical CSRF-aware mutate helper")
	}
	for _, mutation := range []string{
		"mutate('/v1/owner/data/budget'",
		"mutate('/v1/owner/data/savings-goal'",
		"mutate('/v1/owner/data/privacy'",
		"mutate('/v1/owner/data/device/config'",
	} {
		if !strings.Contains(dashboardHTML, mutation) {
			t.Fatalf("dashboard missing canonical mutation path %q", mutation)
		}
	}
}

func TestLegacyDashboardClaimRouteIsGone(t *testing.T) {
	handler := NewHandler(Dependencies{})
	req := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/claim", strings.NewReader(`{"claim_code":"ABC123"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("legacy claim route status=%d want=404", w.Code)
	}
}

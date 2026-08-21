package ownerweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardUsesApprovedSimpleConsumerSurface(t *testing.T) {
	for _, view := range []string{"home", "companion", "personal", "settings"} {
		if !strings.Contains(dashboardHTML, `data-view="`+view+`"`) {
			t.Fatalf("dashboard missing top-level %s view", view)
		}
	}

	for _, required := range []string{
		"Quick add",
		"Requested",
		"Applied",
		"Smart VAD",
		"VAD threshold",
		"End silence",
		"Minimum speech",
		"save-vad-threshold",
		"save-vad-silence",
		"save-vad-min-speech",
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
		`<dialog id="edit-sheet"`,
		"Current authoritative values are loaded before editing.",
		"Saving…",
		"Saved",
		":focus-visible",
		"min-height:44px",
		"@media(max-width:640px)",
		`id="ota-seconds"`,
		`[3600,'1 hour']`,
		`[604800,'7 days']`,
		"value>=3600&&value<=604800",
		"destructiveLabel(state)",
		`Cancel timer \"${clipped(item?.title||'Timer')}\"?`,
		`Delete voice memo \"${clipped(item?.transcript||'Voice memo')}\"`,
	} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("dashboard missing required truthful UI marker %q", required)
		}
	}

	for _, forbidden := range []string{
		">Knowledge<",
		"Google Drive",
		"Google Docs",
		"Google Sheets",
		"Notion",
		"coming soon",
		"/v1/owner/data/device/claim",
		"Claim Device",
		"v2.4.1",
		"SHA256 verified",
		"160.5 KiB",
		"PSRAM Codec",
		"toggleAudio(",
		"checkOTA(",
		"triggerOTA(",
		"custom_phrase",
		`id="ota-seconds" class="input`,
	} {
		if strings.Contains(dashboardHTML, forbidden) {
			t.Fatalf("dashboard contains stale or candidate UI marker %q", forbidden)
		}
	}

	if strings.Contains(dashboardHTML, "fetch('/v1/owner/data/") || strings.Contains(dashboardHTML, `fetch("/v1/owner/data/`) {
		t.Fatal("Owner Hub data mutations must not bypass the canonical CSRF-aware mutate helper")
	}
	for _, mutation := range []string{
		"mutate('/v1/owner/data/expenses','PATCH'",
		"mutate('/v1/owner/data/notes','PATCH'",
		"mutate('/v1/owner/data/journal','PATCH'",
		"mutate('/v1/owner/data/reminders','PATCH'",
		"mutate('/v1/owner/data/reminders/cancel','POST'",
		"mutate('/v1/owner/data/budget','POST'",
		"mutate('/v1/owner/data/savings-goal','POST'",
		"mutate('/v1/owner/data/privacy','POST'",
		"mutate('/v1/owner/data/device/config','POST'",
	} {
		if !strings.Contains(dashboardHTML, mutation) {
			t.Fatalf("dashboard missing canonical mutation path %q", mutation)
		}
	}

	if !strings.Contains(dashboardHTML, "v.media_url") {
		t.Fatal("dashboard missing voice memo media_url playback binding")
	}
	if !strings.Contains(dashboardHTML, `<audio controls preload="none"`) {
		t.Fatal("dashboard missing audio playback control")
	}
	if strings.Contains(dashboardHTML, ".path") || strings.Contains(dashboardHTML, "['path']") || strings.Contains(dashboardHTML, `["path"]`) {
		t.Fatal("dashboard must never reference storage path")
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

package ownerweb

import (
	"strings"
	"testing"
)

func TestDashboardSettingsOutcomeUsesAuthoritativeResponse(t *testing.T) {
	if strings.Contains(dashboardHTML, optimisticSettingsOutcome) {
		t.Fatal("dashboard still contains optimistic settings success")
	}
	for _, marker := range []string{"if (!res.ok || !data?.ok)", "settings_status?.state", "desired ${desired} • reported ${reported}"} {
		if !strings.Contains(dashboardHTML, marker) {
			t.Fatalf("dashboard missing authoritative settings marker %q", marker)
		}
	}
}

func TestAuthoritativeSettingsOutcomeFailsClosedOnTemplateDrift(t *testing.T) {
	if _, err := enforceAuthoritativeSettingsOutcome("<html>no settings mutation here</html>"); err == nil {
		t.Fatal("template drift was accepted")
	}
}

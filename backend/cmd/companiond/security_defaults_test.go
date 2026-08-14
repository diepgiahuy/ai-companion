package main

import (
	"os"
	"testing"
)

func TestProductionForcesOTASignaturesEvenWhenOperatorSetsFalse(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "production")
	t.Setenv("OTA_REQUIRE_SIGNATURE", "false")
	applyProductionSecurityDefaults()
	if got := os.Getenv("OTA_REQUIRE_SIGNATURE"); got != "true" {
		t.Fatalf("OTA_REQUIRE_SIGNATURE=%q; want true", got)
	}
}

func TestDevelopmentAndTestDoNotMutateFixtureOTASetting(t *testing.T) {
	for _, profile := range []string{"development", "test"} {
		t.Run(profile, func(t *testing.T) {
			t.Setenv("COMPANION_PROFILE", profile)
			t.Setenv("OTA_REQUIRE_SIGNATURE", "false")
			applyProductionSecurityDefaults()
			if got := os.Getenv("OTA_REQUIRE_SIGNATURE"); got != "false" {
				t.Fatalf("OTA_REQUIRE_SIGNATURE=%q; want false for %s fixture", got, profile)
			}
		})
	}
}

package main

import (
	"os"
	"strings"
)

// Production has no runtime switch that can disable OTA manifest signatures.
// Development/test retain explicit flexibility for isolated fixtures only.
func applyProductionSecurityDefaults() {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("COMPANION_PROFILE")), "production") {
		_ = os.Setenv("OTA_REQUIRE_SIGNATURE", "true")
	}
}

func init() {
	applyProductionSecurityDefaults()
}

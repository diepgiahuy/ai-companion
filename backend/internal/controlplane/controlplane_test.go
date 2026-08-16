package controlplane

import (
	"testing"
)

func TestValidateRuntimeConfig(t *testing.T) {
	goodInterval := 7200
	cfg := RuntimeConfig{
		OTAPollIntervalSeconds: &goodInterval,
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}

	badInterval := 500
	badCfg := RuntimeConfig{
		OTAPollIntervalSeconds: &badInterval,
	}
	if err := Validate(badCfg); err == nil {
		t.Fatalf("expected error for interval < 3600s, got nil")
	}

	tooLargeInterval := 700000
	tooLargeCfg := RuntimeConfig{
		OTAPollIntervalSeconds: &tooLargeInterval,
	}
	if err := Validate(tooLargeCfg); err == nil {
		t.Fatalf("expected error for interval > 604800s, got nil")
	}
}

func TestMergeRuntimeConfig(t *testing.T) {
	intervalA := 3600
	intervalB := 14400
	base := RuntimeConfig{
		OTAPollIntervalSeconds: &intervalA,
	}
	patch := RuntimeConfig{
		OTAPollIntervalSeconds: &intervalB,
	}
	merged := merge(base, patch)
	if merged.OTAPollIntervalSeconds == nil || *merged.OTAPollIntervalSeconds != 14400 {
		t.Fatalf("expected merged OTAPollIntervalSeconds to be 14400, got %v", merged.OTAPollIntervalSeconds)
	}
}

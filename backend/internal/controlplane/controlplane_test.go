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
		WakeModel:              WakeModelHeyBin,
	}
	patch := RuntimeConfig{
		OTAPollIntervalSeconds: &intervalB,
		WakeModel:              WakeModelDisabled,
	}
	merged := merge(base, patch)
	if merged.OTAPollIntervalSeconds == nil || *merged.OTAPollIntervalSeconds != 14400 {
		t.Fatalf("expected merged OTAPollIntervalSeconds to be 14400, got %v", merged.OTAPollIntervalSeconds)
	}
	if merged.WakeModel != WakeModelDisabled {
		t.Fatalf("expected merged WakeModel to be disabled, got %v", merged.WakeModel)
	}
}

func TestValidateWakeModel(t *testing.T) {
	for _, model := range []string{"", WakeModelHeyBin, WakeModelDisabled} {
		if err := Validate(RuntimeConfig{WakeModel: model}); err != nil {
			t.Fatalf("expected %q wake_model to be valid, got: %v", model, err)
		}
	}
	for _, model := range []string{"wn9_hiesp", "alexa", "hey whatever", string(make([]byte, 65))} {
		if err := Validate(RuntimeConfig{WakeModel: model}); err == nil {
			t.Fatalf("expected unsupported wake_model %q to fail", model)
		}
	}
}

func TestValidateWakeThreshold(t *testing.T) {
	good := 0.60
	if err := Validate(RuntimeConfig{WakeThreshold: &good}); err != nil {
		t.Fatalf("expected valid wake_threshold, got: %v", err)
	}
	tooLow := 0.39
	if err := Validate(RuntimeConfig{WakeThreshold: &tooLow}); err == nil {
		t.Fatalf("expected error for wake_threshold < 0.40")
	}
	tooHigh := 1.0
	if err := Validate(RuntimeConfig{WakeThreshold: &tooHigh}); err == nil {
		t.Fatalf("expected error for wake_threshold > 0.9999")
	}
}

func TestDeriveTwinStatus(t *testing.T) {
	if got := DeriveTwinStatus(Twin{}, false, false); got != TwinStatusUnknown {
		t.Fatalf("empty twin status = %v, want %v", got, TwinStatusUnknown)
	}
	if got := DeriveTwinStatus(Twin{DeviceID: "d1"}, false, true); got != TwinStatusRejected {
		t.Fatalf("rejected twin status = %v, want %v", got, TwinStatusRejected)
	}
	if got := DeriveTwinStatus(Twin{DeviceID: "d1", DesiredVersion: 2, ReportedVersion: 3}, true, false); got != TwinStatusStale {
		t.Fatalf("stale twin status = %v, want %v", got, TwinStatusStale)
	}
	if got := DeriveTwinStatus(Twin{DeviceID: "d1", DesiredVersion: 2, ReportedVersion: 2}, true, false); got != TwinStatusApplied {
		t.Fatalf("applied twin status = %v, want %v", got, TwinStatusApplied)
	}
	if got := DeriveTwinStatus(Twin{DeviceID: "d1", DesiredVersion: 3, ReportedVersion: 2}, true, false); got != TwinStatusRequested {
		t.Fatalf("online pending twin status = %v, want %v", got, TwinStatusRequested)
	}
	if got := DeriveTwinStatus(Twin{DeviceID: "d1", DesiredVersion: 3, ReportedVersion: 2}, false, false); got != TwinStatusOffline {
		t.Fatalf("offline pending twin status = %v, want %v", got, TwinStatusOffline)
	}
}

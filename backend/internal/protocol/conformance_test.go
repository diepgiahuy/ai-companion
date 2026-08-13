package protocol

import (
	"strings"
	"testing"
)

func TestConfigVersionUsesCrossRuntimeExactIntegerRange(t *testing.T) {
	valid := testDeviceConfig()
	for _, version := range []int64{0, 9_007_199_254_740_991} {
		if err := (ConfigUpdatePayload{ConfigVersion: version, Config: valid}).Validate(); err != nil {
			t.Fatalf("valid config_version %d rejected: %v", version, err)
		}
	}
	if err := (ConfigUpdatePayload{ConfigVersion: 9_007_199_254_740_992, Config: valid}).Validate(); err == nil {
		t.Fatal("config_version outside cross-runtime exact JSON integer range was accepted")
	}
}

func TestUICardPayloadRequiresJSONObject(t *testing.T) {
	if err := (UICardPayload{UI: map[string]any{"kind": "text"}}).Validate(); err != nil {
		t.Fatalf("object ui rejected: %v", err)
	}
	for _, value := range []any{"text", []string{"text"}, 1, true} {
		if err := (UICardPayload{UI: value}).Validate(); err == nil || !strings.Contains(err.Error(), "JSON object") {
			t.Fatalf("non-object ui %#v accepted or wrong error: %v", value, err)
		}
	}
}

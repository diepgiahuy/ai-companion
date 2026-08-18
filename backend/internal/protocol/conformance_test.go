package protocol

import (
	"strings"
	"testing"
)

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

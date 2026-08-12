package adkbridge

import "testing"

func TestTextDeltaTrackerUsesADKPartialSemantics(t *testing.T) {
	var tracker textDeltaTracker
	for _, tc := range []struct {
		text    string
		partial bool
		want    string
	}{
		{text: "Hel", partial: true, want: "Hel"},
		{text: "lo", partial: true, want: "lo"},
		// A repeated suffix is still a legitimate delta and must not be dropped.
		{text: "lo", partial: true, want: "lo"},
		{text: "Hellolo!", partial: false, want: "!"},
		{text: "Hellolo!", partial: false, want: ""},
	} {
		if got := tracker.Delta(tc.text, tc.partial); got != tc.want {
			t.Fatalf("Delta(%q, partial=%v)=%q, want %q", tc.text, tc.partial, got, tc.want)
		}
	}
}

func TestTextDeltaTrackerHandlesNonStreamingFinal(t *testing.T) {
	var tracker textDeltaTracker
	if got := tracker.Delta("Xin chào", false); got != "Xin chào" {
		t.Fatalf("got %q", got)
	}
}

func TestTextDeltaTrackerSuppressesConflictingFinalRewrite(t *testing.T) {
	var tracker textDeltaTracker
	if got := tracker.Delta("The cat", true); got != "The cat" {
		t.Fatalf("got %q", got)
	}
	if got := tracker.Delta("The dog", false); got != "" {
		t.Fatalf("conflicting final should not duplicate spoken text, got %q", got)
	}
}

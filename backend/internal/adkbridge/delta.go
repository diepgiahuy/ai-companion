package adkbridge

import "strings"

// textDeltaTracker normalizes ADK SSE text events for the realtime pipeline.
// ADK marks unfinished streaming text with Partial=true; those parts are real
// incremental chunks and must be forwarded verbatim, even when a chunk happens
// to equal a suffix emitted earlier. A later non-partial response may contain
// the complete text, so only its not-yet-emitted suffix is forwarded.
type textDeltaTracker struct {
	emitted string
}

func (t *textDeltaTracker) Delta(candidate string, partial bool) string {
	if candidate == "" {
		return ""
	}
	if partial {
		t.emitted += candidate
		return candidate
	}
	if t.emitted == "" {
		t.emitted = candidate
		return candidate
	}
	if candidate == t.emitted || strings.HasPrefix(t.emitted, candidate) {
		return ""
	}
	if strings.HasPrefix(candidate, t.emitted) {
		delta := candidate[len(t.emitted):]
		t.emitted = candidate
		return delta
	}

	// Once partial text has been released to sentence segmentation/TTS it cannot
	// be safely rewritten. If a provider returns a materially different final
	// snapshot, suppress it rather than speaking duplicate/contradictory text.
	// Provider-parity tests in CP-SW4 will surface such mismatches explicitly.
	return ""
}

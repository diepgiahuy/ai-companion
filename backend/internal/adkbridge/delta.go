package adkbridge

import "strings"

// textDeltaTracker normalizes ADK SSE text events for the realtime pipeline.
// ADK marks unfinished streaming text with Partial=true; those parts are real
// incremental chunks and must be forwarded verbatim, even when a chunk happens
// to equal a suffix emitted earlier. A later non-partial response may contain
// the complete text, so only its not-yet-emitted suffix is forwarded.
type textDeltaTracker struct {
	builder strings.Builder
	emitted string
}

func (t *textDeltaTracker) Delta(candidate string, partial bool) string {
	if candidate == "" {
		return ""
	}
	if partial {
		t.builder.WriteString(candidate)
		t.emitted = ""
		return candidate
	}
	if t.builder.Len() == 0 {
		t.builder.WriteString(candidate)
		t.emitted = candidate
		return candidate
	}
	current := t.current()
	if candidate == current || strings.HasPrefix(current, candidate) {
		return ""
	}
	if strings.HasPrefix(candidate, current) {
		delta := candidate[len(current):]
		t.builder.WriteString(delta)
		t.emitted = candidate
		return delta
	}

	// Once partial text has been released to sentence segmentation/TTS it cannot
	// be safely rewritten. If a provider returns a materially different final
	// snapshot, suppress it rather than speaking duplicate/contradictory text.
	// Provider-parity tests in CP-SW4 will surface such mismatches explicitly.
	return ""
}

func (t *textDeltaTracker) current() string {
	if t.emitted == "" && t.builder.Len() > 0 {
		t.emitted = t.builder.String()
	}
	return t.emitted
}

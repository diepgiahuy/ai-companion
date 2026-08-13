package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type panicRecorder struct{}

func (panicRecorder) TryRecord(Event) bool { panic("exporter failed") }

func TestCorrelatorPseudonymizesClientControlledIDs(t *testing.T) {
	correlator := NewCorrelator("server-session-seed")
	raw := "patient@example.com"
	first := correlator.Opaque(raw)
	second := correlator.Opaque(raw)
	other := correlator.Opaque("another-turn")
	if first != second {
		t.Fatalf("same identifier must correlate within a session: %q != %q", first, second)
	}
	if first == other {
		t.Fatal("different identifiers must not collapse")
	}
	if !strings.HasPrefix(first, "c_") || len(first) != 34 {
		t.Fatalf("unexpected pseudonym format %q", first)
	}
	if strings.Contains(first, raw) {
		t.Fatalf("raw client identifier leaked into pseudonym %q", first)
	}
	if correlator.Opaque("") != "" {
		t.Fatal("empty correlation id must stay empty")
	}
}

func TestDifferentCorrelatorsDoNotCreateStableCrossSessionIdentifier(t *testing.T) {
	left := NewCorrelator("session-a")
	right := NewCorrelator("session-b")
	if left.Opaque("same-client-id") == right.Opaque("same-client-id") {
		t.Fatal("correlation pseudonym must not be stable across runtime sessions")
	}
}

func TestRingRecorderCarriesCorrelationAndBoundsCapacity(t *testing.T) {
	recorder := NewRingRecorder(1)
	ctx := WithRecorder(context.Background(), recorder)
	ctx = WithCorrelation(ctx, Correlation{SessionID: "c_0123456789abcdef0123456789abcdef", TurnID: "c_fedcba9876543210fedcba9876543210", GenerationID: 2})
	if !Record(ctx, Event{Name: EventTurnStart}) {
		t.Fatal("first event should be accepted")
	}
	if Record(ctx, Event{Name: EventTurnEnd}) {
		t.Fatal("event beyond capacity must be dropped")
	}
	snapshot := recorder.Snapshot()
	if snapshot.SchemaVersion != SchemaVersion || len(snapshot.Events) != 1 || snapshot.Dropped != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := snapshot.Events[0].Correlation; got.SessionID != "c_0123456789abcdef0123456789abcdef" || got.TurnID != "c_fedcba9876543210fedcba9876543210" || got.GenerationID != 2 {
		t.Fatalf("correlation drifted: %+v", got)
	}
}

func TestRingRecorderContentionDropsWithoutBlocking(t *testing.T) {
	recorder := NewRingRecorder(4)
	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- recorder.TryRecord(Event{Name: EventTurnStart}) }()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("contended recorder must drop instead of waiting")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("TryRecord blocked under contention")
	}
}

func TestRecorderPanicCannotEscapeApplicationPath(t *testing.T) {
	if RecordTo(panicRecorder{}, Event{Name: EventTurnStart}) {
		t.Fatal("panicking recorder must be reported as not accepted")
	}
}

func TestSnapshotJSONContainsOnlyTypedSafeFields(t *testing.T) {
	recorder := NewRingRecorder(4)
	RecordTo(recorder, Event{Name: EventToolEnd, ToolName: "expense.log", ToolRisk: "write", Outcome: "ok"})
	var buf bytes.Buffer
	if err := WriteSnapshot(&buf, recorder.Snapshot()); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	encoded := buf.String()
	for _, forbidden := range []string{"transcript", "arguments", "credential", "user_id", "device_id", "audio"} {
		if bytes.Contains([]byte(encoded), []byte(forbidden)) {
			t.Fatalf("snapshot exposed forbidden field %q: %s", forbidden, encoded)
		}
	}
}

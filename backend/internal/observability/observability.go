package observability

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const SchemaVersion = 1

type EventName string

const (
	EventSessionReady    EventName = "session.ready"
	EventSessionEnd      EventName = "session.end"
	EventTurnStart       EventName = "turn.start"
	EventTurnStage       EventName = "turn.stage"
	EventTurnEnd         EventName = "turn.end"
	EventTurnInterrupted EventName = "turn.interrupted"
	EventQueueFull       EventName = "queue.full"
	EventToolEnd         EventName = "tool.end"
)

type Correlation struct {
	SessionID    string `json:"session_id,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
	GenerationID uint64 `json:"generation_id,omitempty"`
}

type Event struct {
	SchemaVersion int         `json:"schema_version"`
	Name          EventName   `json:"name"`
	At            time.Time   `json:"at"`
	DurationMS    int64       `json:"duration_ms,omitempty"`
	Outcome       string      `json:"outcome,omitempty"`
	Stage         string      `json:"stage,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	ToolName      string      `json:"tool_name,omitempty"`
	ToolRisk      string      `json:"tool_risk,omitempty"`
	Queue         string      `json:"queue,omitempty"`
	QueueCapacity int         `json:"queue_capacity,omitempty"`
	Correlation   Correlation `json:"correlation,omitempty"`
}

// Recorder implementations must be bounded and non-blocking. Product code
// never performs network or disk exporter I/O through this interface.
type Recorder interface {
	TryRecord(Event) bool
}

type nopRecorder struct{}

func (nopRecorder) TryRecord(Event) bool { return true }
func Nop() Recorder                      { return nopRecorder{} }

type recorderKey struct{}
type correlationKey struct{}

func WithRecorder(ctx context.Context, recorder Recorder) context.Context {
	if recorder == nil {
		recorder = Nop()
	}
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func WithCorrelation(ctx context.Context, correlation Correlation) context.Context {
	return context.WithValue(ctx, correlationKey{}, correlation)
}

func RecorderFrom(ctx context.Context) Recorder {
	if ctx == nil {
		return Nop()
	}
	if recorder, ok := ctx.Value(recorderKey{}).(Recorder); ok && recorder != nil {
		return recorder
	}
	return Nop()
}

func CorrelationFrom(ctx context.Context) Correlation {
	if ctx == nil {
		return Correlation{}
	}
	correlation, _ := ctx.Value(correlationKey{}).(Correlation)
	return correlation
}

func Record(ctx context.Context, event Event) bool {
	if event.Correlation == (Correlation{}) {
		event.Correlation = CorrelationFrom(ctx)
	}
	return RecordTo(RecorderFrom(ctx), event)
}

func RecordTo(recorder Recorder, event Event) (accepted bool) {
	// Observability is strictly auxiliary. A buggy future exporter must not turn
	// a voice/tool/domain success into an application failure or process panic.
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	if recorder == nil {
		return true
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = SchemaVersion
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	return recorder.TryRecord(event)
}

// RingRecorder is the CI/development implementation. It never overwrites old
// events. Contention or capacity exhaustion becomes a counted drop rather than
// a realtime-path stall.
type RingRecorder struct {
	mu       sync.Mutex
	capacity int
	events   []Event
	dropped  atomic.Uint64
}

func NewRingRecorder(capacity int) *RingRecorder {
	if capacity <= 0 {
		capacity = 2048
	}
	return &RingRecorder{capacity: capacity, events: make([]Event, 0, capacity)}
}

func (r *RingRecorder) TryRecord(event Event) bool {
	if r == nil {
		return false
	}
	if !r.mu.TryLock() {
		r.dropped.Add(1)
		return false
	}
	defer r.mu.Unlock()
	if len(r.events) >= r.capacity {
		r.dropped.Add(1)
		return false
	}
	r.events = append(r.events, event)
	return true
}

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Events        []Event   `json:"events"`
	Dropped       uint64    `json:"dropped"`
}

func (r *RingRecorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), Events: []Event{}}
	}
	r.mu.Lock()
	events := append([]Event(nil), r.events...)
	r.mu.Unlock()
	return Snapshot{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), Events: events, Dropped: r.dropped.Load()}
}

func WriteSnapshot(writer io.Writer, snapshot Snapshot) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

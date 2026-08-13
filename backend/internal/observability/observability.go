package observability

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// Correlator turns protocol/runtime identifiers into ephemeral process-local
// pseudonyms. Client-controlled turn IDs therefore cannot smuggle raw text or
// PII into telemetry while events from one runtime remain joinable. The key is
// never exported or persisted.
type Correlator struct {
	key [32]byte
}

func NewCorrelator(fallbackSeed string) *Correlator {
	correlator := &Correlator{}
	if _, err := rand.Read(correlator.key[:]); err != nil {
		// Observability must never make the application unavailable. The fallback
		// remains process-local and still prevents storing raw identifiers; OS
		// entropy is the normal path.
		correlator.key = sha256.Sum256([]byte("companion-observability:" + fallbackSeed))
	}
	return correlator
}

func (c *Correlator) Opaque(value string) string {
	if c == nil || value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(value))
	sum := mac.Sum(nil)
	return "c_" + hex.EncodeToString(sum[:16])
}

// Pseudonymize namespaces turn identifiers by their raw session identifier so
// reusing the same client turn ID in another session does not create a stable
// cross-session identifier. GenerationID is already server-owned and bounded.
func (c *Correlator) Pseudonymize(correlation Correlation) Correlation {
	rawSession := correlation.SessionID
	if rawSession != "" {
		correlation.SessionID = c.Opaque("session\x00" + rawSession)
	}
	if correlation.TurnID != "" {
		correlation.TurnID = c.Opaque("turn\x00" + rawSession + "\x00" + correlation.TurnID)
	}
	return correlation
}

var processCorrelator = NewCorrelator(time.Now().UTC().Format(time.RFC3339Nano))

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
	// Privacy is enforced before the Recorder boundary, not left to individual
	// exporters. No recorder implementation receives raw protocol correlation IDs.
	event.Correlation = processCorrelator.Pseudonymize(event.Correlation)
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

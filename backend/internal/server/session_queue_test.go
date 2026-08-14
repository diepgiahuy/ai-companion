package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSessionSendFailsFastWhenControlQueueIsFull(t *testing.T) {
	s := &session{
		id:            "queue-control",
		controlWrites: make(chan outbound, 1),
		mediaWrites:   make(chan outbound, 1),
	}
	s.controlWrites <- outbound{kind: websocket.MessageText, data: []byte("occupied")}

	started := time.Now()
	err := s.send(context.Background(), outbound{kind: websocket.MessageText, data: []byte("next")})
	if err == nil || !strings.Contains(err.Error(), "control write queue is full") {
		t.Fatalf("error=%v; want full control queue", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("full control queue blocked for %v", elapsed)
	}
	if got := len(s.controlWrites); got != 1 {
		t.Fatalf("control queue length=%d; want original item only", got)
	}
}

func TestSessionSendFailsFastWhenMediaQueueIsFull(t *testing.T) {
	s := &session{
		id:            "queue-media",
		controlWrites: make(chan outbound, 1),
		mediaWrites:   make(chan outbound, 1),
	}
	s.mediaWrites <- outbound{kind: websocket.MessageBinary, data: []byte{1}}

	started := time.Now()
	err := s.send(context.Background(), outbound{kind: websocket.MessageBinary, data: []byte{2}})
	if err == nil || !strings.Contains(err.Error(), "audio write queue is full") {
		t.Fatalf("error=%v; want full media queue", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("full media queue blocked for %v", elapsed)
	}
	if got := len(s.mediaWrites); got != 1 {
		t.Fatalf("media queue length=%d; want original item only", got)
	}
}

func TestSessionSendHonorsCancellationEvenWithAvailableCapacity(t *testing.T) {
	s := &session{
		id:            "queue-cancel",
		controlWrites: make(chan outbound, 1),
		mediaWrites:   make(chan outbound, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.send(ctx, outbound{kind: websocket.MessageText, data: []byte("ignored")}); err != context.Canceled {
		t.Fatalf("error=%v; want context canceled", err)
	}
	if got := len(s.controlWrites); got != 0 {
		t.Fatalf("canceled send enqueued %d messages", got)
	}
}

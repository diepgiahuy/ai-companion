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

func TestSessionSendHonorsCancellationWhenQueueIsFull(t *testing.T) {
	s := &session{
		id:            "queue-cancel-full",
		controlWrites: make(chan outbound, 1),
		mediaWrites:   make(chan outbound, 1),
	}
	s.controlWrites <- outbound{kind: websocket.MessageText, data: []byte("occupied")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.send(ctx, outbound{kind: websocket.MessageText, data: []byte("ignored")}); err != context.Canceled {
		t.Fatalf("error=%v; want context canceled", err)
	}
	if got := len(s.controlWrites); got != 1 {
		t.Fatalf("canceled full-queue send changed queue length to %d", got)
	}
}

func TestSessionSendHonorsCancellationWhenQueueHasCapacity(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := &session{
			id:            "queue-cancel-capacity",
			controlWrites: make(chan outbound, 1),
			mediaWrites:   make(chan outbound, 1),
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := s.send(ctx, outbound{kind: websocket.MessageText, data: []byte("must-not-enqueue")}); err != context.Canceled {
			t.Fatalf("iteration=%d error=%v; want context canceled", i, err)
		}
		if got := len(s.controlWrites); got != 0 {
			t.Fatalf("iteration=%d canceled send enqueued despite spare capacity", i)
		}
	}
}

func TestSessionSendTurnMediaWaitsForTransientQueueCapacity(t *testing.T) {
	s := &session{
		id:          "queue-media-backpressure",
		mediaWrites: make(chan outbound, 1),
	}
	current := &turn{generation: 7}
	s.mediaWrites <- outbound{kind: websocket.MessageBinary, data: []byte{1}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.sendTurnMedia(ctx, current, outbound{kind: websocket.MessageBinary, data: []byte{2}})
	}()

	select {
	case err := <-done:
		t.Fatalf("media producer returned before queue capacity became available: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	<-s.mediaWrites
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("media producer failed after queue capacity became available: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("media producer did not resume after queue capacity became available")
	}

	message := <-s.mediaWrites
	if !message.turnScoped || message.generation != current.generation {
		t.Fatalf("queued media scope=%v generation=%d; want scoped generation %d", message.turnScoped, message.generation, current.generation)
	}
	if len(message.data) != 1 || message.data[0] != 2 {
		t.Fatalf("queued media data=%v; want [2]", message.data)
	}
}

func TestSessionSendTurnMediaHonorsCancellation(t *testing.T) {
	s := &session{
		id:          "queue-media-backpressure-cancel",
		mediaWrites: make(chan outbound, 1),
	}
	current := &turn{generation: 3}
	s.mediaWrites <- outbound{kind: websocket.MessageBinary, data: []byte{1}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.sendTurnMedia(ctx, current, outbound{kind: websocket.MessageBinary, data: []byte{2}})
	if err != context.Canceled {
		t.Fatalf("error=%v; want context canceled", err)
	}
	if got := len(s.mediaWrites); got != 1 {
		t.Fatalf("canceled media send changed queue length to %d", got)
	}
}

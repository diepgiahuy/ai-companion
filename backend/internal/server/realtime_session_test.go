package server

import (
	"context"
	"testing"

	"github.com/coder/websocket"
)

func TestNewTurnInvalidatesQueuedOutputFromPreviousGeneration(t *testing.T) {
	s := &session{
		controlWrites: make(chan outbound, 4),
		audioWrites:   make(chan outbound, 4),
	}
	if err := s.startTurn(context.Background(), "turn-1"); err != nil {
		t.Fatal(err)
	}
	old := s.active
	if old == nil {
		t.Fatal("missing active turn")
	}
	queued := outbound{kind: websocket.MessageBinary, data: []byte{1, 2, 3}, ctx: old.ctx, generation: old.generation}
	if !s.outboundCurrent(queued) {
		t.Fatal("current generation was rejected")
	}

	if err := s.startTurn(context.Background(), "turn-2"); err != nil {
		t.Fatal(err)
	}
	if s.outboundCurrent(queued) {
		t.Fatal("stale queued audio from turn-1 remained valid after turn-2 started")
	}
	if old.ctx.Err() == nil {
		t.Fatal("old turn context was not cancelled")
	}
}

func TestAbortInvalidatesQueuedPlaybackAfterTurnProcessingFinished(t *testing.T) {
	s := &session{
		controlWrites: make(chan outbound, 4),
		audioWrites:   make(chan outbound, 4),
	}
	if err := s.startTurn(context.Background(), "turn-1"); err != nil {
		t.Fatal(err)
	}
	current := s.active
	queued := outbound{kind: websocket.MessageBinary, data: []byte{1}, ctx: current.ctx, generation: current.generation}

	// Model/TTS work can finish before the socket writer drains all queued audio.
	s.mu.Lock()
	s.active = nil
	s.mu.Unlock()
	if !s.outboundCurrent(queued) {
		t.Fatal("finished turn output should stay valid until interrupted")
	}

	s.cancelActive()
	if s.outboundCurrent(queued) {
		t.Fatal("abort did not invalidate queued playback")
	}
}

func TestTurnWriteQueueBackpressureIsBounded(t *testing.T) {
	s := &session{
		controlWrites: make(chan outbound, 1),
		audioWrites:   make(chan outbound, 1),
	}
	if err := s.startTurn(context.Background(), "turn-1"); err != nil {
		t.Fatal(err)
	}
	current := s.active
	if err := s.sendTurn(current, outbound{kind: websocket.MessageBinary, data: []byte{1}}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := s.sendTurn(current, outbound{kind: websocket.MessageBinary, data: []byte{2}}); err == nil {
		t.Fatal("expected bounded audio queue to reject overflow")
	}
}

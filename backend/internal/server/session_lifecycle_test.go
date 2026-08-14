package server

import (
	"testing"
	"time"
)

func TestWaitForTurnIsImmediateForListeningAndBoundedForHungProcessing(t *testing.T) {
	if !waitForTurn(&turn{}, 5*time.Millisecond) {
		t.Fatal("listening turn should join immediately")
	}
	hung := &turn{done: make(chan struct{})}
	started := time.Now()
	if waitForTurn(hung, 20*time.Millisecond) {
		t.Fatal("hung turn unexpectedly joined")
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 250*time.Millisecond {
		t.Fatalf("bounded join elapsed=%v", elapsed)
	}
	done := &turn{done: make(chan struct{})}
	close(done.done)
	if !waitForTurn(done, time.Second) {
		t.Fatal("completed turn should join immediately")
	}
}

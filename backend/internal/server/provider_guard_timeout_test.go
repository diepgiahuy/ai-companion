package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type blockingTestASR struct {
	started atomic.Int32
	release <-chan struct{}
}

func (a *blockingTestASR) Transcribe(context.Context, []byte) (string, error) {
	a.started.Add(1)
	<-a.release
	return "ok", nil
}

func TestProviderGuardBoundsIgnoringCallAndPreventsRepeatGrowth(t *testing.T) {
	release := make(chan struct{})
	inner := &blockingTestASR{release: release}
	guard := newProviderCallGuard(providerTimeouts{ASR: 20 * time.Millisecond, Agent: time.Second, TTS: time.Second})
	wrapped := guardedASR{inner: inner, guard: guard}

	if _, err := wrapped.Transcribe(context.Background(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first call error = %v; want deadline exceeded", err)
	}
	if got := inner.started.Load(); got != 1 {
		t.Fatalf("provider starts = %d; want 1", got)
	}
	if _, err := wrapped.Transcribe(context.Background(), nil); !errors.Is(err, errProviderStageSaturated) {
		t.Fatalf("second call error = %v; want saturated stage", err)
	}
	if got := inner.started.Load(); got != 1 {
		t.Fatalf("saturated retry started another provider call: %d", got)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for len(guard.asr) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(guard.asr) != 0 {
		t.Fatal("provider stage did not recover after blocked call returned")
	}
	if _, err := wrapped.Transcribe(context.Background(), nil); err != nil {
		t.Fatalf("provider stage did not recover: %v", err)
	}
}

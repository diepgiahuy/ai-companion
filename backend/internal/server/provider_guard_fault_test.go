package server

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type lateFrameTTS struct {
	release     <-chan struct{}
	callbackErr chan<- error
}

func (t lateFrameTTS) Synthesize(_ context.Context, _ string, emit func([]byte) error) error {
	<-t.release
	err := emit([]byte{1, 2, 3})
	t.callbackErr <- err
	return err
}

func TestProviderGuardDropsLateTTSFrameAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	callbackErr := make(chan error, 1)
	guard := newProviderCallGuard(providerTimeouts{ASR: time.Second, Agent: time.Second, TTS: 20 * time.Millisecond})
	wrapped := guardedTTS{inner: lateFrameTTS{release: release, callbackErr: callbackErr}, guard: guard}

	var delivered atomic.Int32
	err := wrapped.Synthesize(context.Background(), "hello", func([]byte) error {
		delivered.Add(1)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Synthesize error=%v; want lifecycle deadline", err)
	}

	close(release)
	select {
	case err := <-callbackErr:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("late callback error=%v; want cancelled provider context", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider did not finish late callback")
	}
	if delivered.Load() != 0 {
		t.Fatalf("late stale TTS frames delivered=%d; want zero", delivered.Load())
	}
}

type panicASR struct{}

func (panicASR) Transcribe(context.Context, []byte) (string, error) {
	panic("provider exploded")
}

type okASR struct{}

func (okASR) Transcribe(context.Context, []byte) (string, error) { return "ok", nil }

func TestProviderGuardConvertsPanicAndReleasesStage(t *testing.T) {
	guard := newProviderCallGuard(providerTimeouts{ASR: time.Second, Agent: time.Second, TTS: time.Second})
	wrapped := guardedASR{inner: panicASR{}, guard: guard}
	if _, err := wrapped.Transcribe(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "provider panic") {
		t.Fatalf("panic error=%v; want provider panic error", err)
	}

	// A panic must not leave the process-scoped stage permanently saturated.
	wrapped = guardedASR{inner: okASR{}, guard: guard}
	text, err := wrapped.Transcribe(context.Background(), nil)
	if err != nil || text != "ok" {
		t.Fatalf("stage did not recover after panic: text=%q err=%v", text, err)
	}
}

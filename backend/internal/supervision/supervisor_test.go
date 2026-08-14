package supervision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCriticalWorkerFailureCancelsSupervisor(t *testing.T) {
	s := New(context.Background(), testLogger())
	want := errors.New("boom")
	s.Go("critical", true, func(context.Context) error { return want })
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("critical worker did not cancel supervisor")
	}
	if cause := s.Cause(); cause == nil || !strings.Contains(cause.Error(), "critical") || !errors.Is(cause, want) {
		t.Fatalf("cause = %v; want wrapped critical worker error", cause)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNonCriticalFailureReportsWithoutCancelling(t *testing.T) {
	s := New(context.Background(), testLogger())
	s.Go("optional", false, func(context.Context) error { return errors.New("optional failed") })
	select {
	case err := <-s.Errors():
		if err == nil || !strings.Contains(err.Error(), "optional") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("non-critical failure was not reported")
	}
	select {
	case <-s.Done():
		t.Fatal("non-critical worker cancelled supervisor")
	default:
	}
	s.Stop(nil)
}

func TestPanicIsContainedAndCancelsCriticalWorker(t *testing.T) {
	s := New(context.Background(), testLogger())
	s.Go("panic-worker", true, func(context.Context) error { panic("kaboom") })
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("panic did not cancel supervisor")
	}
	if cause := s.Cause(); cause == nil || !strings.Contains(cause.Error(), "panic-worker panic") {
		t.Fatalf("unexpected cause: %v", cause)
	}
}

func TestWaitIsBoundedForIgnoringWorker(t *testing.T) {
	s := New(context.Background(), testLogger())
	release := make(chan struct{})
	s.Go("ignores-cancel", false, func(context.Context) error {
		<-release
		return nil
	})
	s.Stop(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v; want deadline exceeded", err)
	}
	close(release)
}

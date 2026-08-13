package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockTTSCancellationStopsRemainingFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames := 0
	started := time.Now()
	err := (MockTTS{Frames: 10, FrameDelay: 20 * time.Millisecond}).Synthesize(ctx, "test", func([]byte) error {
		frames++
		if frames == 1 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if frames != 1 {
		t.Fatalf("cancellation emitted %d frames; want exactly 1", frames)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("mock TTS cancellation was not prompt: %v", elapsed)
	}
}

func TestMockTTSDefaultDelayStillCompletes(t *testing.T) {
	frames := 0
	if err := (MockTTS{Frames: 2}).Synthesize(context.Background(), "test", func([]byte) error {
		frames++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if frames != 2 {
		t.Fatalf("got %d frames; want 2", frames)
	}
}

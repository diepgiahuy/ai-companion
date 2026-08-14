package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"companion-server/internal/pipeline"
)

type closeFunc func() error
func (f closeFunc) Close() error { return f() }

type closingASR struct{ closeFunc }
func (closingASR) Transcribe(context.Context, []byte) (string, error) { return "", nil }

type closingAgent struct{ closeFunc }
func (closingAgent) Respond(context.Context, string) (string, error) { return "", nil }

type closingTTS struct{ closeFunc }
func (closingTTS) Synthesize(context.Context, string, func([]byte) error) error { return nil }

func TestProviderComponentClosersOwnAllClosableProviderStages(t *testing.T) {
	components := pipeline.Components{
		ASR: closingASR{closeFunc(func() error { return nil })},
		Agent: closingAgent{closeFunc(func() error { return nil })},
		TTS: closingTTS{closeFunc(func() error { return nil })},
	}
	got := providerComponentClosers(components)
	if len(got) != 3 || got[0].name != "asr" || got[1].name != "agent" || got[2].name != "tts" {
		t.Fatalf("closers=%+v", got)
	}
}

func TestRuntimeResourceCloseIsBoundedWhenCloserIgnoresShutdown(t *testing.T) {
	blocked := make(chan struct{})
	closer := closeFunc(func() error { <-blocked; return nil })
	started := time.Now()
	err := closeRuntimeResourcesBounded(nil, 20*time.Millisecond, namedCloser{name: "mcp", closer: closer})
	close(blocked)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v; want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded close took %v", elapsed)
	}
}

func TestRuntimeResourceClosePropagatesCloserError(t *testing.T) {
	want := errors.New("close failed")
	err := closeRuntimeResourcesBounded(nil, time.Second, namedCloser{name: "provider", closer: closeFunc(func() error { return want })})
	if !errors.Is(err, want) {
		t.Fatalf("error=%v; want %v", err, want)
	}
}

var _ io.Closer = closeFunc(nil)

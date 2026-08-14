package server

import (
	"context"
	"testing"
	"time"

	"companion-server/internal/pipeline"
)

type wiringBlockingASR struct {
	release <-chan struct{}
}

func (a wiringBlockingASR) Transcribe(context.Context, []byte) (string, error) {
	<-a.release
	return "ok", nil
}

func TestGuardProviderComponentsAppliesSharedProductionGuard(t *testing.T) {
	release := make(chan struct{})
	components := GuardProviderComponents(pipeline.Components{ASR: wiringBlockingASR{release: release}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := components.ASR.Transcribe(ctx, nil); err == nil {
		t.Fatal("guarded ASR must honor an already-cancelled context")
	}

	close(release)

	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := components.ASR.Transcribe(ctx, nil); err != nil {
		t.Fatalf("guarded ASR did not recover: %v", err)
	}
}

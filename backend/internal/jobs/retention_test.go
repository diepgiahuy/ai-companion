package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"companion-server/internal/privacy"
	"github.com/riverqueue/river"
)

type retentionStub struct {
	report privacy.RetentionReport
	err    error
	ctx    context.Context
}

func (s *retentionStub) ApplyRetention(ctx context.Context) (privacy.RetentionReport, error) {
	s.ctx = ctx
	return s.report, s.err
}

func TestRetentionWorkerDeletesOrphansAndPropagatesFailures(t *testing.T) {
	service := &retentionStub{report: privacy.RetentionReport{ConversationRows: 1, OrphanPaths: []string{"one", "two"}}}
	var removed []string
	worker := &RetentionWorker{Service: service, Remove: func(path string) error {
		removed = append(removed, path)
		return nil
	}, TimeoutDuration: time.Minute}
	if err := worker.Work(context.Background(), &river.Job[RetentionArgs]{}); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0] != "one" || removed[1] != "two" {
		t.Fatalf("removed=%v", removed)
	}
	service.err = errors.New("database unavailable")
	if err := worker.Work(context.Background(), &river.Job[RetentionArgs]{}); err == nil {
		t.Fatal("retention repository error was swallowed")
	}
}

func TestRetentionWorkerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := &retentionStub{}
	worker := &RetentionWorker{Service: service, TimeoutDuration: time.Minute}
	if err := worker.Work(ctx, &river.Job[RetentionArgs]{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if service.ctx != nil {
		t.Fatal("cancelled job reached retention service")
	}
}

func TestRuntimeConfigAndRetentionInsertPolicy(t *testing.T) {
	config, err := (Config{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if config.RetentionInterval != 6*time.Hour || config.JobTimeout != 10*time.Minute || config.RescueAfter != 20*time.Minute || config.SoftStopTimeout != 8*time.Second {
		t.Fatalf("config=%+v", config)
	}
	if _, err := (Config{JobTimeout: time.Minute, RescueAfter: time.Second}).normalized(); err == nil {
		t.Fatal("rescue-before-timeout configuration was accepted")
	}
	opts := retentionInsertOpts(6 * time.Hour)
	if opts.Queue != QueueMaintenance || opts.MaxAttempts != 5 || opts.UniqueOpts.ByPeriod != 6*time.Hour {
		t.Fatalf("opts=%+v", opts)
	}
}

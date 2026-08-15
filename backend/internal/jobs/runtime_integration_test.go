package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"companion-server/internal/privacy"
	"github.com/jackc/pgx/v5/pgxpool"
)

type controlledRetention struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	started   chan struct{}
	block     bool
	cancelled chan struct{}
}

func (s *controlledRetention) ApplyRetention(ctx context.Context) (privacy.RetentionReport, error) {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.block {
		<-ctx.Done()
		if s.cancelled != nil {
			close(s.cancelled)
		}
		return privacy.RetentionReport{}, ctx.Err()
	}
	if attempt <= s.failUntil {
		return privacy.RetentionReport{}, fmt.Errorf("retryable attempt %d", attempt)
	}
	return privacy.RetentionReport{ConversationRows: 1}, nil
}

func (s *controlledRetention) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func riverTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("COMPANION_RIVER_TEST_DSN")
	if dsn == "" {
		t.Skip("COMPANION_RIVER_TEST_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DELETE FROM river.river_job`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestIntegrationRiverTransactionalEnqueueUniquenessRetryAndGracefulStop(t *testing.T) {
	pool := riverTestPool(t)
	service := &controlledRetention{failUntil: 1}
	runtime, err := New(context.Background(), pool, service, nil, Config{
		RetentionInterval: time.Hour, JobTimeout: 2 * time.Second,
		RescueAfter: 3 * time.Second, SoftStopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := runtime.InsertRetentionTx(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river.river_job WHERE id=$1`, rolledBack.JobID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back job count=%d err=%v", count, err)
	}

	committed, err := runtime.EnqueueRetention(context.Background())
	if err != nil || committed.UniqueSkipped {
		t.Fatalf("committed=%+v err=%v", committed, err)
	}
	duplicate, err := runtime.EnqueueRetention(context.Background())
	if err != nil || !duplicate.UniqueSkipped || duplicate.JobID != committed.JobID {
		t.Fatalf("duplicate=%+v committed=%+v err=%v", duplicate, committed, err)
	}

	run := startRiverForTest(t, runtime)
	waitFor(t, 8*time.Second, func() bool {
		snapshot := runtime.MetricsSnapshot()
		return snapshot.RetryAttempts >= 1 && snapshot.Completed >= 1
	})
	if service.Attempts() != 2 {
		t.Fatalf("attempts=%d want=2", service.Attempts())
	}
	if err := run.stop(); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRiverRescuesStuckJobAfterRestart(t *testing.T) {
	pool := riverTestPool(t)
	service := &controlledRetention{}
	runtime, err := New(context.Background(), pool, service, nil, Config{
		RetentionInterval: time.Hour, JobTimeout: 100 * time.Millisecond,
		RescueAfter: 200 * time.Millisecond, SoftStopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := runtime.EnqueueRetention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE river.river_job SET state='running',attempt=1,attempted_at=now()-interval '1 hour',attempted_by=ARRAY['crashed-client'] WHERE id=$1`, inserted.JobID); err != nil {
		t.Fatal(err)
	}
	run := startRiverForTest(t, runtime)
	waitFor(t, 8*time.Second, func() bool { return runtime.MetricsSnapshot().Completed >= 1 })
	if service.Attempts() != 1 {
		t.Fatalf("rescued attempts=%d want=1", service.Attempts())
	}
	if err := run.stop(); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRiverSoftStopCancelsContextBoundJob(t *testing.T) {
	pool := riverTestPool(t)
	service := &controlledRetention{started: make(chan struct{}, 1), block: true, cancelled: make(chan struct{})}
	runtime, err := New(context.Background(), pool, service, nil, Config{
		RetentionInterval: time.Hour, JobTimeout: 2 * time.Second,
		RescueAfter: 3 * time.Second, SoftStopTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := startRiverForTest(t, runtime)
	if _, err := runtime.EnqueueRetention(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.started:
	case <-time.After(5 * time.Second):
		t.Fatal("retention job did not start")
	}
	run.cancel()
	select {
	case <-service.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("soft stop did not cancel retention context")
	}
	if err := run.stop(); err != nil {
		t.Fatal(err)
	}
}

type testRiverRun struct {
	runtime *Runtime
	cancel  context.CancelFunc
	done    <-chan error
	once    sync.Once
	err     error
}

func startRiverForTest(t *testing.T, runtime *Runtime) *testRiverRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	run := &testRiverRun{runtime: runtime, cancel: cancel, done: done}
	t.Cleanup(func() {
		if err := run.stop(); err != nil {
			t.Errorf("stop River test runtime: %v", err)
		}
	})
	return run
}

func (r *testRiverRun) stop() error {
	r.once.Do(func() {
		r.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.runtime.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.err = errors.Join(r.err, err)
		}
		select {
		case err := <-r.done:
			if err != nil && !errors.Is(err, context.Canceled) {
				r.err = errors.Join(r.err, err)
			}
		case <-ctx.Done():
			r.err = errors.Join(r.err, fmt.Errorf("River runtime did not stop: %w", ctx.Err()))
		}
	})
	return r.err
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

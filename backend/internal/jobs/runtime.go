package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const (
	Schema           = "river"
	QueueMaintenance = "maintenance"
	PeriodicJobID    = "companion-retention-cleanup-v1"
)

type Config struct {
	RetentionInterval time.Duration
	JobTimeout        time.Duration
	SoftStopTimeout   time.Duration
	RescueAfter       time.Duration
	RunOnStart        bool
}

func (c Config) normalized() (Config, error) {
	if c.RetentionInterval <= 0 {
		c.RetentionInterval = 6 * time.Hour
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 10 * time.Minute
	}
	if c.SoftStopTimeout <= 0 {
		c.SoftStopTimeout = 8 * time.Second
	}
	if c.RescueAfter <= 0 {
		c.RescueAfter = 20 * time.Minute
	}
	if c.RescueAfter < c.JobTimeout {
		return Config{}, fmt.Errorf("River rescue-after must be >= job timeout")
	}
	return c, nil
}

type EnqueueResult struct {
	JobID         int64 `json:"job_id"`
	UniqueSkipped bool  `json:"unique_skipped"`
}

type Runtime struct {
	pool         *pgxpool.Pool
	client       *river.Client[pgx.Tx]
	metrics      *Metrics
	interval     time.Duration
	events       <-chan *river.Event
	cancelEvents func()
	started      atomic.Bool
}

func New(ctx context.Context, pool *pgxpool.Pool, service RetentionService, logger *slog.Logger, config Config) (*Runtime, error) {
	if pool == nil {
		return nil, fmt.Errorf("PostgreSQL pool is required for River")
	}
	if service == nil {
		return nil, fmt.Errorf("retention service is required for River")
	}
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: Schema, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("initialize River schema validator: %w", err)
	}
	validation, err := migrator.Validate(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("validate River schema: %w", err)
	}
	if !validation.OK {
		return nil, fmt.Errorf("River schema is not current: %s", strings.Join(validation.Messages, "; "))
	}

	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &RetentionWorker{Service: service, Logger: logger, TimeoutDuration: config.JobTimeout}); err != nil {
		return nil, fmt.Errorf("register retention worker: %w", err)
	}
	insertOpts := retentionInsertOpts(config.RetentionInterval)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:            Schema,
		Logger:            logger,
		Workers:           workers,
		Queues:            map[string]river.QueueConfig{QueueMaintenance: {MaxWorkers: 1}},
		ReindexerSchedule: river.NeverSchedule(),
		PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(
			river.PeriodicInterval(config.RetentionInterval),
			func() (river.JobArgs, *river.InsertOpts) { opts := insertOpts; return RetentionArgs{}, &opts },
			&river.PeriodicJobOpts{ID: PeriodicJobID, RunOnStart: config.RunOnStart},
		)},
		JobTimeout:           config.JobTimeout,
		RescueStuckJobsAfter: config.RescueAfter,
		SoftStopTimeout:      config.SoftStopTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize River client: %w", err)
	}
	events, cancelEvents := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed, river.EventKindJobCancelled)
	return &Runtime{pool: pool, client: client, metrics: &Metrics{}, interval: config.RetentionInterval, events: events, cancelEvents: cancelEvents}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return fmt.Errorf("River runtime already started")
	}
	defer r.cancelEvents()
	if err := r.client.Start(ctx); err != nil {
		return fmt.Errorf("start River runtime: %w", err)
	}
	for {
		select {
		case event, ok := <-r.events:
			if ok {
				r.metrics.Record(event)
			} else {
				r.events = nil
			}
		case <-r.client.Stopped():
			if ctx.Err() == nil {
				return fmt.Errorf("River runtime stopped unexpectedly")
			}
			return nil
		}
	}
}

func (r *Runtime) Stop(ctx context.Context) error { return r.client.Stop(ctx) }

func (r *Runtime) MetricsSnapshot() MetricsSnapshot { return r.metrics.Snapshot() }

func (r *Runtime) EnqueueRetention(ctx context.Context) (EnqueueResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return EnqueueResult{}, err
	}
	defer tx.Rollback(ctx)
	result, err := r.InsertRetentionTx(ctx, tx)
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EnqueueResult{}, err
	}
	r.metrics.recordEnqueue(result.UniqueSkipped)
	return result, nil
}

func (r *Runtime) InsertRetentionTx(ctx context.Context, tx pgx.Tx) (EnqueueResult, error) {
	if tx == nil {
		return EnqueueResult{}, fmt.Errorf("PostgreSQL transaction is required")
	}
	inserted, err := r.client.InsertTx(ctx, tx, RetentionArgs{}, ptr(retentionInsertOpts(r.interval)))
	if err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{JobID: inserted.Job.ID, UniqueSkipped: inserted.UniqueSkippedAsDuplicate}, nil
}

func retentionInsertOpts(interval time.Duration) river.InsertOpts {
	return river.InsertOpts{
		Queue: QueueMaintenance, MaxAttempts: 5, Tags: []string{"companion", "retention"},
		UniqueOpts: river.UniqueOpts{ByPeriod: interval},
	}
}

func ptr[T any](value T) *T { return &value }

var _ interface {
	EnqueueRetention(context.Context) (EnqueueResult, error)
	MetricsSnapshot() MetricsSnapshot
} = (*Runtime)(nil)

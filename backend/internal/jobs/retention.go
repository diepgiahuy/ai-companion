package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"companion-server/internal/privacy"
	"github.com/riverqueue/river"
)

const RetentionKind = "companion_retention_cleanup"

type RetentionArgs struct{}

func (RetentionArgs) Kind() string { return RetentionKind }

type RetentionService interface {
	ApplyRetention(context.Context) (privacy.RetentionReport, error)
}

type RetentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	Service         RetentionService
	Remove          func(string) error
	Logger          *slog.Logger
	TimeoutDuration time.Duration
}

func (w *RetentionWorker) Timeout(*river.Job[RetentionArgs]) time.Duration {
	return w.TimeoutDuration
}

func (w *RetentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	if w.Service == nil {
		return fmt.Errorf("retention service is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := w.Service.ApplyRetention(ctx)
	if err != nil {
		return fmt.Errorf("apply retention: %w", err)
	}
	remove := w.Remove
	if remove == nil {
		remove = os.Remove
	}
	for _, path := range report.OrphanPaths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove retained voice file: %w", err)
		}
	}
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, "retention job completed",
		"conversation_rows", report.ConversationRows,
		"memory_rows", report.MemoryRows,
		"voice_memo_rows", report.VoiceMemoRows,
		"orphan_files", len(report.OrphanPaths),
	)
	return nil
}

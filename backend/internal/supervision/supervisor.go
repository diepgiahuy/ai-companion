package supervision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Supervisor owns process-level workers under one child context. Critical
// failures cancel the group so the composition root can begin shutdown.
type Supervisor struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	logger *slog.Logger

	wg      sync.WaitGroup
	errOnce sync.Once
	errCh   chan error
}

func New(parent context.Context, logger *slog.Logger) *Supervisor {
	if parent == nil {
		parent = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &Supervisor{ctx: ctx, cancel: cancel, logger: logger, errCh: make(chan error, 1)}
}

func (s *Supervisor) Context() context.Context { return s.ctx }
func (s *Supervisor) Done() <-chan struct{}     { return s.ctx.Done() }
func (s *Supervisor) Errors() <-chan error      { return s.errCh }
func (s *Supervisor) Cause() error              { return context.Cause(s.ctx) }

func (s *Supervisor) Go(name string, critical bool, fn func(context.Context) error) {
	if fn == nil {
		panic("supervision: nil worker")
	}
	if name == "" {
		name = "unnamed"
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				err := fmt.Errorf("worker %s panic: %v", name, recovered)
				s.logger.Error("supervised worker panicked", "worker", name, "error", err, "stack", string(debug.Stack()))
				s.report(err, critical)
			}
		}()

		err := fn(s.ctx)
		if err == nil || errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && s.ctx.Err() != nil) {
			return
		}
		s.logger.Error("supervised worker stopped", "worker", name, "error", err, "critical", critical)
		s.report(fmt.Errorf("worker %s: %w", name, err), critical)
	}()
}

func (s *Supervisor) report(err error, critical bool) {
	if err == nil {
		return
	}
	select {
	case s.errCh <- err:
	default:
	}
	if critical {
		s.errOnce.Do(func() { s.cancel(err) })
	}
}

func (s *Supervisor) Stop(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	s.cancel(cause)
}

// Wait joins workers until ctx expires. A cancellation-ignoring worker therefore
// becomes a bounded shutdown error instead of hanging the process forever.
func (s *Supervisor) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("supervisor shutdown: %w", ctx.Err())
	}
}

package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"companion-server/internal/domain"
)

type SchedulerRepository interface {
	ClaimDueReminders(context.Context, time.Time, int) ([]domain.ScheduledItem, error)
	RecoverDispatchingReminders(context.Context) (int64, error)
	MarkReminderSent(context.Context, int64, time.Time) error
	ReleaseReminder(context.Context, int64) error
	AcknowledgeReminder(context.Context, string, string, int64) error
	NextReminder(context.Context, string, string, time.Time) (domain.ScheduledItem, bool, error)
}

type reminderScheduler struct {
	data     SchedulerRepository
	hub      *sessionHub
	interval time.Duration
	location *time.Location
	logger   *slog.Logger
	mu       sync.Mutex
	lastNext map[string]string
}

func newReminderScheduler(data SchedulerRepository, hub *sessionHub, interval time.Duration, location *time.Location, logger *slog.Logger) *reminderScheduler {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if location == nil {
		location = time.Local
	}
	return &reminderScheduler{data: data, hub: hub, interval: interval, location: location, logger: logger, lastNext: map[string]string{}}
}
func (s *reminderScheduler) run(ctx context.Context) {
	if n, err := s.data.RecoverDispatchingReminders(ctx); err != nil {
		s.logger.Warn("reminder recovery failed", "error", err)
	} else if n > 0 {
		s.logger.Info("recovered reminder deliveries", "count", n)
	}
	s.tick(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}
func (s *reminderScheduler) tick(ctx context.Context) {
	if err := s.dispatchDue(ctx); err != nil {
		s.logger.Warn("reminder dispatch failed", "error", err)
	}
	if err := s.refreshSchedules(ctx); err != nil {
		s.logger.Warn("schedule refresh failed", "error", err)
	}
}
func (s *reminderScheduler) dispatchDue(ctx context.Context) error {
	items, err := s.data.ClaimDueReminders(ctx, time.Now(), 32)
	if err != nil {
		return err
	}
	for _, item := range items {
		pushCtx, cancel := context.WithTimeout(ctx, time.Second)
		sent := s.hub.pushAlarm(pushCtx, item)
		cancel()
		if sent == 0 {
			if err := s.data.ReleaseReminder(ctx, item.ID); err != nil {
				return err
			}
			continue
		}
		delay := retryDelay(item.Attempts + 1)
		if err := s.data.MarkReminderSent(ctx, item.ID, time.Now().Add(delay)); err != nil {
			return err
		}
		s.logger.Info("reminder sent; awaiting ack", "id", item.ID, "device_id", item.DeviceID, "sessions", sent, "retry_in", delay.String())
	}
	return nil
}
func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 5 * time.Second * time.Duration(1<<min(attempt-1, 4))
	if d > time.Minute {
		d = time.Minute
	}
	return d
}
func (s *reminderScheduler) refreshSchedules(ctx context.Context) error {
	for _, identity := range s.hub.identities() {
		userID, deviceID := identity.UserID, identity.DeviceID
		next, ok, err := s.data.NextReminder(ctx, userID, deviceID, time.Now())
		if err != nil {
			return err
		}
		key, summary, fireAt := "", "", ""
		if ok {
			local := next.FireAt.In(s.location)
			key = local.Format(time.RFC3339) + "|" + next.Title
			summary = local.Format("15:04") + " " + next.Title
			fireAt = next.FireAt.UTC().Format(time.RFC3339)
		}
		cacheKey := userID + "\x00" + deviceID
		s.mu.Lock()
		prev, known := s.lastNext[cacheKey]
		if known && prev == key {
			s.mu.Unlock()
			continue
		}
		s.lastNext[cacheKey] = key
		s.mu.Unlock()
		pushCtx, cancel := context.WithTimeout(ctx, time.Second)
		s.hub.pushSchedule(pushCtx, userID, deviceID, summary, fireAt)
		cancel()
	}
	return nil
}

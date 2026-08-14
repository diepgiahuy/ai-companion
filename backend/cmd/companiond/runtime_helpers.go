package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/events"
	"companion-server/internal/market"
	"companion-server/internal/observability"
	"companion-server/internal/privacy"
	"companion-server/internal/runtimeconfig"
	promptpkg "companion-server/prompts"
)

func configureObservability(logger *slog.Logger) (observability.Recorder, func()) {
	path := strings.TrimSpace(os.Getenv("COMPANION_OBSERVABILITY_FILE"))
	if path == "" {
		return observability.Nop(), func() {}
	}
	capacity := 4096
	if raw := strings.TrimSpace(os.Getenv("COMPANION_OBSERVABILITY_CAPACITY")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			capacity = parsed
		}
	}
	recorder := observability.NewRingRecorder(capacity)
	flush := func() {
		snapshot := recorder.Snapshot()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			logger.Error("create observability snapshot directory", "error", err)
			return
		}
		file, err := os.Create(path)
		if err != nil {
			logger.Error("create observability snapshot", "error", err)
			return
		}
		defer file.Close()
		if err := observability.WriteSnapshot(file, snapshot); err != nil {
			logger.Error("write observability snapshot", "error", err)
			return
		}
		logger.Info("observability snapshot written", "events", len(snapshot.Events), "dropped", snapshot.Dropped)
	}
	return recorder, flush
}

func loadPromptBundle(cfg runtimeconfig.Config) (*promptpkg.Bundle, error) {
	if cfg.LLM.PromptDir != "" {
		return promptpkg.LoadDirectory(cfg.LLM.PromptDir)
	}
	return promptpkg.LoadDefault()
}

func value(name, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}

func runOutbox(ctx context.Context, out events.Outbox, logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = events.Dispatch(ctx, out, func(_ context.Context, e events.Event) error {
				logger.Debug("domain event", "type", e.Type, "subject", e.Subject, "event_id", e.ID)
				return nil
			}, now, 50)
		}
	}
}

type marketWatchRepo interface {
	market.WatchRepository
	domain.ScheduleRepository
}

func runMarketWatcher(ctx context.Context, repo marketWatchRepo, quotes *market.Service, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	check := func(now time.Time) {
		watches, e := repo.EnabledMarketWatches(ctx, 200)
		if e != nil {
			logger.Warn("market watches load failed", "error", e)
			return
		}
		for _, w := range watches {
			q, e := quotes.Quote(ctx, w.Provider, w.Symbol, w.Currency)
			if e != nil {
				logger.Warn("market watch quote failed", "watch_id", w.ID, "error", e)
				continue
			}
			state := market.Matches(w, q.Price)
			if state && !w.LastState {
				title := fmt.Sprintf("%s %.4f %s %s %.4f", w.Symbol, q.Price, q.Currency, w.Operator, w.Threshold)
				if atomicRepo, ok := repo.(interface {
					TriggerMarketWatch(context.Context, market.Watch, string, time.Time) (bool, error)
				}); ok {
					if _, e := atomicRepo.TriggerMarketWatch(ctx, w, title, now); e != nil {
						logger.Warn("market alert transaction failed", "watch_id", w.ID, "error", e)
					}
				} else {
					key := fmt.Sprintf("market-watch:%d:%d", w.ID, now.Unix())
					if e := repo.CreateReminderForDevice(ctx, w.UserID, key, w.DeviceID, title, now); e != nil {
						logger.Warn("market alert schedule failed", "watch_id", w.ID, "error", e)
						continue
					}
					_ = repo.SetMarketWatchState(ctx, w.ID, true)
				}
			} else if state != w.LastState {
				_ = repo.SetMarketWatchState(ctx, w.ID, state)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			check(now)
		}
	}
}

func runRetention(ctx context.Context, svc *privacy.Service, logger *slog.Logger) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	apply := func() {
		report, err := svc.ApplyRetention(ctx)
		if err != nil {
			logger.Warn("retention failed", "error", err)
			return
		}
		for _, path := range report.OrphanPaths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				logger.Warn("retained voice file cleanup failed", "path", path, "error", err)
			}
		}
		if report.ConversationRows+report.MemoryRows+report.VoiceMemoRows > 0 {
			logger.Info("retention applied", "conversation_rows", report.ConversationRows, "memory_rows", report.MemoryRows, "voice_memo_rows", report.VoiceMemoRows)
		}
	}
	apply()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply()
		}
	}
}

func seedFeatureCatalog(ctx context.Context, c *controlplane.FeatureCatalog, logger *slog.Logger) {
	modules := []controlplane.FeatureModule{
		{ID: "core.finance", Version: 1, Lifecycle: "released", Execution: "native", MinProtocol: 1, Tools: []string{"expense.*", "budget.*"}, Resources: []string{"expenses://*", "budget://*"}, UICards: []string{"expense_summary"}, Locales: []string{"vi-VN", "en-US"}, Implementation: "native.finance"},
		{ID: "core.schedule", Version: 1, Lifecycle: "released", Execution: "native", MinProtocol: 1, Tools: []string{"timer.*", "reminder.*"}, Resources: []string{"timers://*", "reminders://*"}, Implementation: "native.schedule"},
		{ID: "memory.long_term", Version: 1, Lifecycle: "beta", Execution: "native", MinProtocol: 1, Tools: []string{"memory.*"}, ConfigKeys: []string{"memory.long_term"}, Implementation: "native.memory"},
		{ID: "market.live", Version: 1, Lifecycle: "beta", Execution: "native", MinProtocol: 1, Tools: []string{"market.*"}, UICards: []string{"market_price"}, Implementation: "native.market"},
	}
	for _, m := range modules {
		if err := c.Put(ctx, m); err != nil {
			logger.Warn("feature catalog seed failed", "feature", m.ID, "error", err)
		}
	}
}

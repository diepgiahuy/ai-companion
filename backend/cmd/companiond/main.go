package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"companion-server/internal/agent"
	"companion-server/internal/capability"
	"companion-server/internal/contextengine"
	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
	"companion-server/internal/events"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/pipeline"
	"companion-server/internal/policy"
	"companion-server/internal/privacy"
	conversationprovider "companion-server/internal/providers/conversation"
	resourceprovider "companion-server/internal/providers/resources"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/server"
	"companion-server/internal/store"
	"companion-server/internal/usage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	address := value("COMPANION_ADDRESS", ":8000")
	databasePath := value("COMPANION_DATABASE", "companion.db")

	data, err := store.Open(databasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer data.Close()

	components := pipeline.Components{
		ASR:    pipeline.MockASR{Transcript: os.Getenv("MOCK_TRANSCRIPT")},
		TTS:    pipeline.MockTTS{},
		Codecs: pipeline.OpusFactory{},
	}
	timezone := value("COMPANION_TIMEZONE", "Asia/Ho_Chi_Minh")
	location, err := time.LoadLocation(timezone)
	if err != nil {
		logger.Error("load timezone", "error", err)
		os.Exit(1)
	}
	var conversationCache conversationctx.Cache
	switch value("COMPANION_CONTEXT_CACHE", "memory") {
	case "none":
		conversationCache = conversationctx.NoopCache{}
	default:
		conversationCache = conversationctx.NewMemoryCache(30*time.Minute, 100)
	}
	conversationService := conversationctx.New(
		conversationprovider.NewSQLite(data),
		conversationCache,
	)

	// Production-shaped control plane kept local for the POC: device twin,
	// remote config and feature evaluation are provider boundaries, not hard-coded
	// into the voice runtime.
	b := true
	vad := 450
	silence := 800
	minSpeech := 250
	idle := 5000
	alarm := 10000
	control := controlplane.New(data, controlplane.RuntimeConfig{SmartVADEnabled: &b, VADThreshold: &vad, VADSilenceMS: &silence, VADMinSpeechMS: &minSpeech, IdleAfterMS: &idle, AlarmVisibleMS: &alarm, Locale: "vi-VN", Timezone: timezone, VoiceKey: "default"})
	for _, f := range []controlplane.Flag{{Key: "memory.long_term", Enabled: true, Rollout: 100}, {Key: "market.live", Enabled: true, Rollout: 100}} {
		_ = data.EnsureFlag(context.Background(), f)
	}
	features := controlplane.NewFeatures(data)
	otaPublicKey, err := controlplane.DecodeEd25519PublicKey(os.Getenv("OTA_PUBLIC_KEY"))
	if err != nil {
		logger.Error("decode OTA public key", "error", err)
		os.Exit(1)
	}
	requireOTASignature := value("OTA_REQUIRE_SIGNATURE", "false") == "true"
	if requireOTASignature && len(otaPublicKey) == 0 {
		logger.Error("OTA_REQUIRE_SIGNATURE needs OTA_PUBLIC_KEY")
		os.Exit(1)
	}
	firmwareService := controlplane.NewFirmware(data, otaPublicKey, requireOTASignature)
	privacyService := privacy.New(data)
	featureCatalog := controlplane.NewFeatureCatalog(data)
	seedFeatureCatalog(context.Background(), featureCatalog, logger)
	var embedding memory.EmbeddingProvider = memory.HashEmbedding{Dimensions: 96}
	if base := os.Getenv("EMBEDDING_BASE_URL"); base != "" {
		embedding = memory.OpenAIEmbedding{BaseURL: base, APIKey: os.Getenv("EMBEDDING_API_KEY"), Model: value("EMBEDDING_MODEL", "text-embedding"), Client: &http.Client{Timeout: 20 * time.Second}}
	}
	memoryService := memory.New(data, embedding)
	httpClient := &http.Client{Timeout: 8 * time.Second}
	marketProviders := []market.Provider{market.CoinGecko{APIKey: os.Getenv("COINGECKO_API_KEY"), Client: httpClient}}
	if k := os.Getenv("TWELVEDATA_API_KEY"); k != "" {
		marketProviders = append(marketProviders, market.TwelveData{APIKey: k, Client: httpClient})
	}
	if k := os.Getenv("ALPHAVANTAGE_API_KEY"); k != "" {
		marketProviders = append(marketProviders, market.AlphaVantageGold{APIKey: k, Client: httpClient})
	}
	if value("PNJ_GOLD_ENABLED", "false") == "true" {
		marketProviders = append(marketProviders, market.PNJGold{Client: httpClient})
	}
	marketService := market.New(30*time.Second, marketProviders...)

	resourceRegistry := capability.NewResourceRegistry()
	if err := resourceRegistry.Register(resourceprovider.NewNative(data, conversationService, location)); err != nil {
		logger.Error("register native resources", "error", err)
		os.Exit(1)
	}
	toolRegistry := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(toolRegistry, toolprovider.NativeDependencies{
		Store: data, Conversation: conversationService, Resources: resourceRegistry,
		VoicePrivacy:  privacyService,
		RecordingsDir: value("COMPANION_RECORDINGS_DIR", "data/recordings"),
	}); err != nil {
		logger.Error("register native tools", "error", err)
		os.Exit(1)
	}
	if err := toolprovider.RegisterPlatform(toolRegistry, toolprovider.PlatformDependencies{Memory: memoryService, Market: marketService, MarketWatches: data}); err != nil {
		logger.Error("register platform tools", "error", err)
		os.Exit(1)
	}
	toolRegistry.SetAuthorizer(policy.Authorizer{Features: features, Entitlements: data, Privacy: privacyService})

	monthlyLLMLimit, _ := strconv.ParseInt(value("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "0"), 10, 64)
	usageGuard := usage.Guard{Reader: data, MonthlyLimit: monthlyLLMLimit}
	qwenBaseURL := os.Getenv("QWEN_BASE_URL")
	if qwenBaseURL == "" {
		components.Agent = pipeline.MockAgent{}
		logger.Warn("QWEN_BASE_URL is empty; using deterministic mock agent")
	} else {
		qwen, err := agent.NewQwen(
			qwenBaseURL,
			os.Getenv("QWEN_API_KEY"),
			value("QWEN_MODEL", "Qwen/Qwen3-4B-Instruct-2507"),
			timezone,
			data,
			agent.WithConversation(conversationService),
			agent.WithToolRegistry(toolRegistry),
			agent.WithContextPlanner(contextengine.New(resourceRegistry)),
			agent.WithUsageMeter(data),
			agent.WithUsageGuard(usageGuard),
			agent.WithModelSelector(agent.KeywordModelSelector{Fast: value("QWEN_FAST_MODEL", value("QWEN_MODEL", "Qwen/Qwen3-4B-Instruct-2507")), Reasoning: os.Getenv("QWEN_REASONING_MODEL")}),
		)
		if err != nil {
			logger.Error("initialize Qwen", "error", err)
			os.Exit(1)
		}
		components.Agent = qwen
	}

	serverOptions := []server.Option{
		server.WithStore(data), server.WithLocation(location),
		server.WithIdentityResolver(server.HeaderIdentityResolver{DefaultUserID: value("COMPANION_DEFAULT_USER_ID", "default")}),
		server.WithControlPlane(control), server.WithFirmwareService(firmwareService), server.WithPrivacyService(privacyService), server.WithFeatureCatalog(featureCatalog), server.WithAdminToken(os.Getenv("COMPANION_ADMIN_TOKEN")),
		server.WithDeviceCredentialManager(data), server.WithEntitlementManager(data),
	}
	if value("COMPANION_DEVICE_AUTH", "legacy") == "database" {
		serverOptions = append(serverOptions, server.WithDeviceAuthenticator(data))
	}
	service := server.New(components, os.Getenv("COMPANION_DEVICE_TOKEN"), logger, serverOptions...)
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go service.RunBackground(rootCtx)
	_ = data.RecoverOutbox(rootCtx)
	go runOutbox(rootCtx, data, logger)
	go runMarketWatcher(rootCtx, data, marketService, logger)
	go runRetention(rootCtx, privacyService, logger)

	httpServer := &http.Server{
		Addr:              address,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("companion server listening", "address", address)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	<-rootCtx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
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

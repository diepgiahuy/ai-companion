package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"companion-server/internal/adkbridge"
	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/devicecap"
	"companion-server/internal/domain"
	"companion-server/internal/events"
	"companion-server/internal/jobs"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/observability"
	"companion-server/internal/policy"
	"companion-server/internal/privacy"
	resourceprovider "companion-server/internal/providers/resources"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/runtimeconfig"
	"companion-server/internal/server"
	"companion-server/internal/supervision"
	"companion-server/internal/usage"
	"companion-server/internal/voicemail"
	promptpkg "companion-server/prompts"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	observer, flushObservability := configureObservability(logger)
	defer flushObservability()
	runtimeCfg, err := runtimeconfig.Load()
	if err != nil {
		logger.Error("load runtime configuration", "error", err)
		os.Exit(1)
	}
	promptBundle, err := loadPromptBundle(runtimeCfg)
	if err != nil {
		logger.Error("load prompt bundle", "error", err)
		os.Exit(1)
	}
	address := value("COMPANION_ADDRESS", ":8000")
	databaseCtx, cancelDatabase := context.WithTimeout(context.Background(), 15*time.Second)
	data, err := openProductDatabase(databaseCtx, runtimeCfg.Profile)
	cancelDatabase()
	if err != nil {
		logger.Error("open authoritative PostgreSQL database", "error", err)
		os.Exit(1)
	}
	defer data.Close()

	components, err := configureSpeechComponents(runtimeCfg)
	if err != nil {
		logger.Error("configure speech providers", "error", err, "profile", os.Getenv("COMPANION_SPEECH_PROFILE"))
		os.Exit(1)
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
	conversationService := conversationctx.New(data, conversationCache)

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
	voiceMailBlobs, err := voicemail.NewFileSystem(value("COMPANION_VOICE_MAIL_DIR", "data/voice-mail"))
	if err != nil {
		logger.Error("initialize voice mail blob store", "error", err)
		os.Exit(1)
	}
	voiceMailService, err := voicemail.New(data, voiceMailBlobs)
	if err != nil {
		logger.Error("initialize voice mail service", "error", err)
		os.Exit(1)
	}
	jobConfig, err := loadJobConfig()
	if err != nil {
		logger.Error("load River job configuration", "error", err)
		os.Exit(1)
	}
	jobCtx, cancelJobs := context.WithTimeout(context.Background(), 15*time.Second)
	jobRuntime, err := jobs.New(jobCtx, data.Pool(), maintenanceService{privacy: privacyService, voiceMail: voiceMailService}, logger, jobConfig)
	cancelJobs()
	if err != nil {
		logger.Error("initialize River runtime", "error", err, "hint", "run companion-river-migrate up with the migration/admin database URL")
		os.Exit(1)
	}
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
		VoicePrivacy: privacyService, RecordingsDir: value("COMPANION_RECORDINGS_DIR", "data/recordings"),
	}); err != nil {
		logger.Error("register native tools", "error", err)
		os.Exit(1)
	}
	if err := toolprovider.RegisterPlatform(toolRegistry, toolprovider.PlatformDependencies{Memory: memoryService, Market: marketService, MarketWatches: data}); err != nil {
		logger.Error("register platform tools", "error", err)
		os.Exit(1)
	}
	deviceCapabilities := devicecap.NewRouter()
	if err := devicecap.RegisterTools(toolRegistry, deviceCapabilities); err != nil {
		logger.Error("register authenticated device capability tools", "error", err)
		os.Exit(1)
	}
	toolRegistry.SetAuthorizer(policy.Authorizer{Features: features, Entitlements: data, Privacy: privacyService, Confirmations: deviceCapabilities})
	mcpCloser, err := configureMCP(toolRegistry, resourceRegistry, 10*time.Second)
	if err != nil {
		logger.Error("initialize external MCP capabilities", "error", err)
		os.Exit(1)
	}

	usageGuard := usage.Guard{Reader: data, MonthlyLimit: runtimeCfg.LLM.MonthlyTokenLimit}
	adkBaseURL := strings.TrimSpace(os.Getenv("ADK_OPENAI_BASE_URL"))
	adkModel := strings.TrimSpace(os.Getenv("ADK_MODEL"))
	if adkBaseURL == "" || adkModel == "" {
		logger.Error("ADK product runtime requires explicit ADK_OPENAI_BASE_URL and ADK_MODEL; no legacy/mock fallback is available")
		os.Exit(1)
	}
	adkPrompt, err := promptBundle.Render(promptpkg.RenderInput{
		Locale: "vi-VN", CurrentTime: time.Now().In(location), Timezone: timezone,
		Persona: runtimeCfg.LLM.Persona,
		Packs:   []string{"finance", "schedule", "memory", "personal-data", "voice", "context", "external-data"},
	})
	if err != nil {
		logger.Error("render ADK prompt", "error", err)
		os.Exit(1)
	}
	adkAgent, err := adkbridge.New(adkbridge.Config{
		AppName: "companion", ModelName: adkModel, BaseURL: adkBaseURL,
		APIKey: os.Getenv("ADK_OPENAI_API_KEY"), Instruction: adkPrompt.Text,
		PromptVersion: adkPrompt.ID + "@" + adkPrompt.Version + "#" + adkPrompt.Fingerprint,
		HTTPClient:    &http.Client{Timeout: runtimeCfg.LLM.HTTPTimeout}, Tools: toolRegistry,
		Conversation: conversationService, HistoryLimit: 12, UsageGuard: usageGuard, UsageMeter: data,
	})
	if err != nil {
		if mcpCloser != nil {
			_ = closeRuntimeResourcesBounded(logger, 2*time.Second, namedCloser{name: "mcp", closer: mcpCloser})
		}
		logger.Error("initialize ADK runtime", "error", err, "hint", "build with -tags=adk; ADK_MODEL_PROTOCOL must be responses or chat_completions")
		os.Exit(1)
	}
	components.Agent = adkAgent
	providerClosers := providerComponentClosers(components)
	components = server.GuardProviderComponents(components)

	serverOptions := []server.Option{
		server.WithStore(data), server.WithLocation(location),
		server.WithIdentityResolver(server.HeaderIdentityResolver{DefaultUserID: value("COMPANION_DEFAULT_USER_ID", "default")}),
		server.WithControlPlane(control), server.WithFirmwareService(firmwareService), server.WithPrivacyService(privacyService), server.WithFeatureCatalog(featureCatalog), server.WithAdminToken(os.Getenv("COMPANION_ADMIN_TOKEN")),
		server.WithDeviceCredentialManager(data), server.WithEntitlementManager(data), server.WithDeviceAuthenticator(data),
		server.WithDeviceCapabilities(deviceCapabilities), server.WithPairingRepository(data), server.WithObservabilityRecorder(observer), server.WithJobControl(jobRuntime),
		server.WithVoiceMail(voiceMailService),
	}
	service := server.New(components, logger, serverOptions...)
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	supervisor := supervision.New(rootCtx, logger)

	supervisor.Go("server-background", false, func(ctx context.Context) error { service.RunBackground(ctx); return nil })
	supervisor.Go("river", true, jobRuntime.Run)
	if err := data.RecoverOutbox(supervisor.Context()); err != nil {
		logger.Warn("recover outbox dispatch state failed", "error", err)
	}
	supervisor.Go("outbox", false, func(ctx context.Context) error { runOutbox(ctx, data, service.HandleEvent, logger); return nil })
	supervisor.Go("market-watcher", false, func(ctx context.Context) error { runMarketWatcher(ctx, data, marketService, logger); return nil })

	httpServer := &http.Server{
		Addr: address, Handler: ownerAuthFromEnvironment(deviceOriginGuard(service.Handler()), data), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second,
	}
	supervisor.Go("http-server", true, func(context.Context) error {
		logger.Info("companion server listening", "address", address)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	select {
	case <-rootCtx.Done():
	case <-supervisor.Done():
		if cause := supervisor.Cause(); cause != nil {
			logger.Error("runtime supervisor requested shutdown", "error", cause)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful HTTP shutdown failed", "error", err)
	}
	if err := jobRuntime.Stop(shutdownCtx); err != nil {
		logger.Error("graceful River shutdown failed", "error", err)
	}
	supervisor.Stop(context.Canceled)
	if err := supervisor.Wait(shutdownCtx); err != nil {
		logger.Error("graceful runtime shutdown failed", "error", err)
	}
	resources := append([]namedCloser{}, providerClosers...)
	resources = append(resources, namedCloser{name: "mcp", closer: mcpCloser})
	if err := closeRuntimeResourcesBounded(logger, 2*time.Second, resources...); err != nil {
		logger.Error("bounded runtime resource shutdown failed", "error", err)
	}
}

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

func runOutbox(ctx context.Context, out events.Outbox, handler events.Handler, logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := events.Dispatch(ctx, out, func(eventCtx context.Context, e events.Event) error {
				logger.Debug("domain event", "type", e.Type, "subject", e.Subject, "event_id", e.ID)
				if handler != nil {
					return handler(eventCtx, e)
				}
				return nil
			}, now, 50); err != nil {
				logger.Warn("outbox dispatch failed", "error", err)
			}
		}
	}
}

type maintenanceService struct {
	privacy   *privacy.Service
	voiceMail *voicemail.Service
}

func (s maintenanceService) ApplyRetention(ctx context.Context) (privacy.RetentionReport, error) {
	report, err := s.privacy.ApplyRetention(ctx)
	if err != nil {
		return report, err
	}
	_, err = s.voiceMail.Cleanup(ctx, 100)
	return report, err
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

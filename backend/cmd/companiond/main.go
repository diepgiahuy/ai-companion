package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"companion-server/internal/adkbridge"
	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/policy"
	"companion-server/internal/privacy"
	conversationprovider "companion-server/internal/providers/conversation"
	resourceprovider "companion-server/internal/providers/resources"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/runtimeconfig"
	"companion-server/internal/server"
	"companion-server/internal/store"
	"companion-server/internal/usage"
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
	databasePath := value("COMPANION_DATABASE", "companion.db")

	data, err := store.Open(databasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer data.Close()

	components, err := configureSpeechComponents(runtimeCfg)
	if err != nil {
		logger.Error("configure speech providers", "error", err, "profile", runtimeCfg.Profile, "speech_profile", os.Getenv("COMPANION_SPEECH_PROFILE"))
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
	conversationService := conversationctx.New(
		conversationprovider.NewSQLite(data),
		conversationCache,
	)

	b := true
	vad := 450
	silence := 800
	minSpeech := 250
	idle := 5000
	alarm := 10000
	control := controlplane.New(data, controlplane.RuntimeConfig{
		SmartVADEnabled: &b,
		VADThreshold:    &vad,
		VADSilenceMS:    &silence,
		VADMinSpeechMS:  &minSpeech,
		IdleAfterMS:     &idle,
		AlarmVisibleMS:  &alarm,
		Locale:          "vi-VN",
		Timezone:        timezone,
		VoiceKey:        "default",
	})
	for _, f := range []controlplane.Flag{
		{Key: "memory.long_term", Enabled: true, Rollout: 100},
		{Key: "market.live", Enabled: true, Rollout: 100},
	} {
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
		embedding = memory.OpenAIEmbedding{
			BaseURL: base,
			APIKey:  os.Getenv("EMBEDDING_API_KEY"),
			Model:   value("EMBEDDING_MODEL", "text-embedding"),
			Client:  &http.Client{Timeout: 20 * time.Second},
		}
	}
	memoryService := memory.New(data, embedding)
	httpClient := &http.Client{Timeout: 8 * time.Second}
	marketProviders := []market.Provider{
		market.CoinGecko{APIKey: os.Getenv("COINGECKO_API_KEY"), Client: httpClient},
	}
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
		Store:         data,
		Conversation:  conversationService,
		Resources:     resourceRegistry,
		VoicePrivacy:  privacyService,
		RecordingsDir: value("COMPANION_RECORDINGS_DIR", "data/recordings"),
	}); err != nil {
		logger.Error("register native tools", "error", err)
		os.Exit(1)
	}
	if err := toolprovider.RegisterPlatform(toolRegistry, toolprovider.PlatformDependencies{
		Memory: memoryService, Market: marketService, MarketWatches: data,
	}); err != nil {
		logger.Error("register platform tools", "error", err)
		os.Exit(1)
	}
	toolRegistry.SetAuthorizer(policy.Authorizer{Features: features, Entitlements: data, Privacy: privacyService})

	monthlyLLMLimit, _ := strconv.ParseInt(value("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "0"), 10, 64)
	usageGuard := usage.Guard{Reader: data, MonthlyLimit: monthlyLLMLimit}
	adkBaseURL := strings.TrimSpace(os.Getenv("ADK_OPENAI_BASE_URL"))
	adkModel := strings.TrimSpace(os.Getenv("ADK_MODEL"))
	if adkBaseURL == "" || adkModel == "" {
		logger.Error("ADK product runtime requires explicit ADK_OPENAI_BASE_URL and ADK_MODEL; no legacy/mock fallback is available")
		os.Exit(1)
	}
	adkPrompt, err := promptBundle.Render(promptpkg.RenderInput{
		Locale:      "vi-VN",
		CurrentTime: time.Now().In(location),
		Timezone:    timezone,
		Persona:     runtimeCfg.LLM.Persona,
		Packs:       []string{"finance", "schedule", "memory", "personal-data", "voice", "context", "external-data"},
	})
	if err != nil {
		logger.Error("render ADK prompt", "error", err)
		os.Exit(1)
	}
	adkAgent, err := adkbridge.New(adkbridge.Config{
		AppName:       "companion",
		ModelName:     adkModel,
		ModelProtocol: strings.TrimSpace(os.Getenv("ADK_MODEL_PROTOCOL")),
		BaseURL:       adkBaseURL,
		APIKey:        os.Getenv("ADK_OPENAI_API_KEY"),
		Instruction:   adkPrompt.Text,
		PromptVersion: adkPrompt.ID + "@" + adkPrompt.Version + "#" + adkPrompt.Fingerprint,
		HTTPClient:    &http.Client{Timeout: runtimeCfg.LLM.HTTPTimeout},
		Tools:         toolRegistry,
		Conversation:  conversationService,
		HistoryLimit:  12,
		UsageGuard:    usageGuard,
		UsageMeter:    data,
	})
	if err != nil {
		logger.Error("initialize ADK runtime", "error", err, "hint", "build with -tags=adk and set ADK_MODEL_PROTOCOL=responses or chat_completions")
		os.Exit(1)
	}
	components.Agent = adkAgent

	serverOptions := []server.Option{
		server.WithStore(data),
		server.WithLocation(location),
		server.WithIdentityResolver(server.HeaderIdentityResolver{DefaultUserID: value("COMPANION_DEFAULT_USER_ID", "default")}),
		server.WithControlPlane(control),
		server.WithFirmwareService(firmwareService),
		server.WithPrivacyService(privacyService),
		server.WithFeatureCatalog(featureCatalog),
		server.WithAdminToken(os.Getenv("COMPANION_ADMIN_TOKEN")),
		server.WithDeviceCredentialManager(data),
		server.WithEntitlementManager(data),
		server.WithDeviceAuthenticator(data),
		server.WithObservabilityRecorder(observer),
	}
	service := server.New(components, logger, serverOptions...)
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

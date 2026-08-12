package runtimeconfig

import "testing"

func TestProductionRejectsMockProviders(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "production")
	t.Setenv("COMPANION_ALLOW_MOCK_PROVIDERS", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected production mock rejection")
	}
}

func TestLoadLLMSettings(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "development")
	t.Setenv("COMPANION_ALLOW_MOCK_PROVIDERS", "true")
	t.Setenv("COMPANION_LLM_HTTP_TIMEOUT", "12s")
	t.Setenv("COMPANION_LLM_TEMPERATURE", "0.25")
	t.Setenv("COMPANION_LLM_MAX_TOKENS", "512")
	t.Setenv("COMPANION_LLM_MAX_TOOL_ROUNDS", "5")
	t.Setenv("COMPANION_MODEL_ROUTER", "semantic")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.HTTPTimeout.String() != "12s" || cfg.LLM.Temperature != 0.25 || cfg.LLM.MaxTokens != 512 || cfg.LLM.MaxToolRounds != 5 || cfg.LLM.Router != "semantic" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

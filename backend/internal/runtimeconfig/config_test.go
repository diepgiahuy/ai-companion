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
	t.Setenv("COMPANION_PROMPT_DIR", "/tmp/prompts")
	t.Setenv("COMPANION_PERSONA", "desk-companion")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.HTTPTimeout.String() != "12s" || cfg.LLM.PromptDir != "/tmp/prompts" || cfg.LLM.Persona != "desk-companion" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

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
	t.Setenv("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "123456")
	t.Setenv("COMPANION_PROMPT_DIR", "/tmp/prompts")
	t.Setenv("COMPANION_PERSONA", "desk-companion")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.HTTPTimeout.String() != "12s" || cfg.LLM.MonthlyTokenLimit != 123456 || cfg.LLM.PromptDir != "/tmp/prompts" || cfg.LLM.Persona != "desk-companion" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestInvalidMonthlyLLMTokenLimitFailsClosed(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "development")
	for _, value := range []string{"100k", "-1", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", value)
			if _, err := Load(); err == nil {
				t.Fatalf("expected invalid monthly token limit %q to fail", value)
			}
		})
	}
}

func TestZeroMonthlyLLMTokenLimitExplicitlyDisablesQuota(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "development")
	t.Setenv("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.MonthlyTokenLimit != 0 {
		t.Fatalf("monthly token limit = %d, want 0", cfg.LLM.MonthlyTokenLimit)
	}
}

func TestProductionRequiresSignedOTA(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "production")
	t.Setenv("COMPANION_ALLOW_MOCK_PROVIDERS", "false")
	t.Setenv("OTA_REQUIRE_SIGNATURE", "false")
	if _, err := Load(); err == nil {
		t.Fatal("expected unsigned OTA rejection in production")
	}

	t.Setenv("OTA_REQUIRE_SIGNATURE", "true")
	if _, err := Load(); err != nil {
		t.Fatalf("expected signed OTA production config to load: %v", err)
	}
}

func TestInvalidOTARequireSignatureFailsClosed(t *testing.T) {
	t.Setenv("COMPANION_PROFILE", "development")
	t.Setenv("OTA_REQUIRE_SIGNATURE", "tru")
	if _, err := Load(); err == nil {
		t.Fatal("expected malformed OTA_REQUIRE_SIGNATURE to fail")
	}
}

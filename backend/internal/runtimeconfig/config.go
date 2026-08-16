package runtimeconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Profile string

const (
	ProfileDevelopment Profile = "development"
	ProfileTest        Profile = "test"
	ProfileProduction  Profile = "production"
)

type LLM struct {
	HTTPTimeout       time.Duration
	MonthlyTokenLimit int64
	PromptDir         string
	Persona           string
}

type Config struct {
	Profile   Profile
	AllowMock bool
	LLM       LLM
}

func Load() (Config, error) {
	profile := Profile(strings.ToLower(strings.TrimSpace(env("COMPANION_PROFILE", string(ProfileDevelopment)))))
	if profile != ProfileDevelopment && profile != ProfileTest && profile != ProfileProduction {
		return Config{}, fmt.Errorf("COMPANION_PROFILE must be development, test, or production")
	}

	timeout, err := time.ParseDuration(env("COMPANION_LLM_HTTP_TIMEOUT", "45s"))
	if err != nil || timeout <= 0 || timeout > 5*time.Minute {
		return Config{}, fmt.Errorf("COMPANION_LLM_HTTP_TIMEOUT must be >0 and <=5m")
	}

	monthlyTokenLimit, err := strconv.ParseInt(env("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "0"), 10, 64)
	if err != nil || monthlyTokenLimit < 0 {
		return Config{}, fmt.Errorf("COMPANION_MONTHLY_LLM_TOKEN_LIMIT must be a non-negative integer")
	}

	defaultMock := "true"
	if profile == ProfileProduction {
		defaultMock = "false"
	}
	allowMock, err := strconv.ParseBool(env("COMPANION_ALLOW_MOCK_PROVIDERS", defaultMock))
	if err != nil {
		return Config{}, fmt.Errorf("COMPANION_ALLOW_MOCK_PROVIDERS must be boolean")
	}
	if profile == ProfileProduction && allowMock {
		return Config{}, fmt.Errorf("production profile cannot enable mock providers")
	}

	otaRequireSignature, err := strconv.ParseBool(env("OTA_REQUIRE_SIGNATURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("OTA_REQUIRE_SIGNATURE must be boolean")
	}
	if profile == ProfileProduction && !otaRequireSignature {
		return Config{}, fmt.Errorf("production profile requires OTA_REQUIRE_SIGNATURE=true")
	}

	return Config{
		Profile:   profile,
		AllowMock: allowMock,
		LLM: LLM{
			HTTPTimeout:       timeout,
			MonthlyTokenLimit: monthlyTokenLimit,
			PromptDir:         strings.TrimSpace(os.Getenv("COMPANION_PROMPT_DIR")),
			Persona:           strings.TrimSpace(os.Getenv("COMPANION_PERSONA")),
		},
	}, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

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
	HTTPTimeout time.Duration
	PromptDir   string
	Persona     string
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

	return Config{
		Profile:   profile,
		AllowMock: allowMock,
		LLM: LLM{
			HTTPTimeout: timeout,
			PromptDir:   strings.TrimSpace(os.Getenv("COMPANION_PROMPT_DIR")),
			Persona:     strings.TrimSpace(os.Getenv("COMPANION_PERSONA")),
		},
	}, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

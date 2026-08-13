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
	HTTPTimeout   time.Duration
	Temperature   float64
	MaxTokens     int
	MaxToolRounds int
	PromptDir     string
	Persona       string
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
	temperature, err := strconv.ParseFloat(env("COMPANION_LLM_TEMPERATURE", "0.1"), 64)
	if err != nil || temperature < 0 || temperature > 2 {
		return Config{}, fmt.Errorf("COMPANION_LLM_TEMPERATURE must be between 0 and 2")
	}
	maxTokens, err := strconv.Atoi(env("COMPANION_LLM_MAX_TOKENS", "384"))
	if err != nil || maxTokens < 16 || maxTokens > 32768 {
		return Config{}, fmt.Errorf("COMPANION_LLM_MAX_TOKENS must be between 16 and 32768")
	}
	maxRounds, err := strconv.Atoi(env("COMPANION_LLM_MAX_TOOL_ROUNDS", "3"))
	if err != nil || maxRounds < 1 || maxRounds > 12 {
		return Config{}, fmt.Errorf("COMPANION_LLM_MAX_TOOL_ROUNDS must be between 1 and 12")
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
			HTTPTimeout:   timeout,
			Temperature:   temperature,
			MaxTokens:     maxTokens,
			MaxToolRounds: maxRounds,
			PromptDir:     strings.TrimSpace(os.Getenv("COMPANION_PROMPT_DIR")),
			Persona:       strings.TrimSpace(os.Getenv("COMPANION_PERSONA")),
		},
	}, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

package agent

import (
	"fmt"
	"time"
)

type GenerationConfig struct {
	HTTPTimeout   time.Duration
	Temperature   float64
	MaxTokens     int
	MaxToolRounds int
}

func DefaultGenerationConfig() GenerationConfig {
	return GenerationConfig{
		HTTPTimeout:   45 * time.Second,
		Temperature:   0.1,
		MaxTokens:     384,
		MaxToolRounds: 3,
	}
}

func (c GenerationConfig) Validate() error {
	if c.HTTPTimeout <= 0 || c.HTTPTimeout > 5*time.Minute {
		return fmt.Errorf("HTTP timeout must be >0 and <=5m")
	}
	if c.Temperature < 0 || c.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if c.MaxTokens < 16 || c.MaxTokens > 32768 {
		return fmt.Errorf("max tokens must be between 16 and 32768")
	}
	if c.MaxToolRounds < 1 || c.MaxToolRounds > 12 {
		return fmt.Errorf("max tool rounds must be between 1 and 12")
	}
	return nil
}

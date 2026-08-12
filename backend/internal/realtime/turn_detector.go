package realtime

import (
	"strings"
	"unicode/utf8"
)

type TurnDetectorConfig struct {
	MinSpeechMS        int
	MinSilenceMS       int
	BaseSilenceMS      int
	MaxSilenceMS       int
	ShortUtteranceRunes int
	LongUtteranceRunes int
	ShortExtraMS       int
	LongReductionMS    int
	StableReductionMS  int
	TerminalReductionMS int
}

func DefaultTurnDetectorConfig() TurnDetectorConfig {
	return TurnDetectorConfig{
		MinSpeechMS:         250,
		MinSilenceMS:        350,
		BaseSilenceMS:       800,
		MaxSilenceMS:        1400,
		ShortUtteranceRunes: 8,
		LongUtteranceRunes:  32,
		ShortExtraMS:        300,
		LongReductionMS:     150,
		StableReductionMS:   100,
		TerminalReductionMS: 200,
	}
}

type TurnSignal struct {
	PartialText      string
	Stable           bool
	SpeechDurationMS int
	SilenceMS        int
}

type TurnDecision struct {
	Finalize          bool
	RequiredSilenceMS int
	Reason            string
}

type TurnDetector struct {
	config TurnDetectorConfig
}

func NewTurnDetector(config TurnDetectorConfig) TurnDetector {
	defaults := DefaultTurnDetectorConfig()
	if config.MinSpeechMS <= 0 {
		config.MinSpeechMS = defaults.MinSpeechMS
	}
	if config.MinSilenceMS <= 0 {
		config.MinSilenceMS = defaults.MinSilenceMS
	}
	if config.BaseSilenceMS <= 0 {
		config.BaseSilenceMS = defaults.BaseSilenceMS
	}
	if config.MaxSilenceMS < config.BaseSilenceMS {
		config.MaxSilenceMS = defaults.MaxSilenceMS
	}
	if config.ShortUtteranceRunes <= 0 {
		config.ShortUtteranceRunes = defaults.ShortUtteranceRunes
	}
	if config.LongUtteranceRunes <= config.ShortUtteranceRunes {
		config.LongUtteranceRunes = defaults.LongUtteranceRunes
	}
	if config.ShortExtraMS <= 0 {
		config.ShortExtraMS = defaults.ShortExtraMS
	}
	if config.LongReductionMS <= 0 {
		config.LongReductionMS = defaults.LongReductionMS
	}
	if config.StableReductionMS <= 0 {
		config.StableReductionMS = defaults.StableReductionMS
	}
	if config.TerminalReductionMS <= 0 {
		config.TerminalReductionMS = defaults.TerminalReductionMS
	}
	return TurnDetector{config: config}
}

func (d TurnDetector) Decide(signal TurnSignal) TurnDecision {
	if signal.SpeechDurationMS < d.config.MinSpeechMS {
		return TurnDecision{RequiredSilenceMS: d.config.BaseSilenceMS, Reason: "minimum_speech_not_met"}
	}
	text := strings.TrimSpace(signal.PartialText)
	if text == "" {
		return TurnDecision{RequiredSilenceMS: d.config.MaxSilenceMS, Reason: "no_partial_transcript"}
	}

	required := d.config.BaseSilenceMS
	runes := utf8.RuneCountInString(text)
	switch {
	case runes <= d.config.ShortUtteranceRunes:
		required += d.config.ShortExtraMS
	case runes >= d.config.LongUtteranceRunes:
		required -= d.config.LongReductionMS
	}
	if signal.Stable {
		required -= d.config.StableReductionMS
	}
	if hasTerminalPunctuation(text) {
		required -= d.config.TerminalReductionMS
	}
	required = clampInt(required, d.config.MinSilenceMS, d.config.MaxSilenceMS)
	if signal.SilenceMS >= required {
		return TurnDecision{Finalize: true, RequiredSilenceMS: required, Reason: "dynamic_silence_met"}
	}
	return TurnDecision{RequiredSilenceMS: required, Reason: "waiting_for_silence"}
}

func hasTerminalPunctuation(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(text)
	switch r {
	case '.', '?', '!', '。', '？', '！':
		return true
	default:
		return false
	}
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

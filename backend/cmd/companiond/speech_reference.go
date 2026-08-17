package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"companion-server/internal/pipeline"
	"companion-server/internal/runtimeconfig"
	"companion-server/internal/speech"
)

const (
	speechProfileMock               = "mock"
	speechProfileReferenceLocal     = "reference-local"
	speechProfileReferenceStreaming = "reference-streaming"
)

func configureSpeechComponents(cfg runtimeconfig.Config) (pipeline.Components, error) {
	profile := strings.ToLower(strings.TrimSpace(os.Getenv("COMPANION_SPEECH_PROFILE")))
	if profile == "" {
		if cfg.AllowMock {
			profile = speechProfileMock
		} else {
			return pipeline.Components{}, errors.New("COMPANION_SPEECH_PROFILE is required when mock providers are disabled")
		}
	}
	components := pipeline.Components{Codecs: pipeline.OpusFactory{}}
	switch profile {
	case speechProfileMock:
		if !cfg.AllowMock {
			return pipeline.Components{}, errors.New("mock speech profile is forbidden when COMPANION_ALLOW_MOCK_PROVIDERS=false")
		}
		components.ASR = pipeline.MockASR{Transcript: os.Getenv("MOCK_TRANSCRIPT")}
		components.TTS = pipeline.MockTTS{}
		return components, nil

	case speechProfileReferenceLocal:
		funASR, err := speech.NewFunASR(speech.FunASRConfig{
			BaseURL:     strings.TrimSpace(os.Getenv("FUNASR_BASE_URL")),
			Model:       strings.TrimSpace(os.Getenv("FUNASR_MODEL")),
			Language:    strings.TrimSpace(os.Getenv("FUNASR_LANGUAGE")),
			MaxPCMBytes: envInt("FUNASR_MAX_PCM_BYTES", 4*1024*1024),
		})
		if err != nil {
			return pipeline.Components{}, fmt.Errorf("configure FunASR: %w", err)
		}
		edgeCommand := value("EDGE_TTS_COMMAND", "edge-tts")
		ffmpegCommand := value("EDGE_TTS_FFMPEG_COMMAND", "ffmpeg")
		if err := requireExecutable(edgeCommand); err != nil {
			return pipeline.Components{}, fmt.Errorf("configure EdgeTTS executable: %w", err)
		}
		if err := requireExecutable(ffmpegCommand); err != nil {
			return pipeline.Components{}, fmt.Errorf("configure EdgeTTS decoder: %w", err)
		}
		edge, err := speech.NewEdgeTTS(speech.EdgeTTSConfig{
			Command:       edgeCommand,
			FFmpegCommand: ffmpegCommand,
			Voice:         value("EDGE_TTS_VOICE", "vi-VN-HoaiMyNeural"),
			Rate:          value("EDGE_TTS_RATE", "+0%"),
			Volume:        value("EDGE_TTS_VOLUME", "+0%"),
			Pitch:         value("EDGE_TTS_PITCH", "+0Hz"),
			MaxMP3Bytes:   envInt("EDGE_TTS_MAX_MP3_BYTES", 16*1024*1024),
			MaxPCMBytes:   envInt("EDGE_TTS_MAX_PCM_BYTES", 32*1024*1024),
		})
		if err != nil {
			return pipeline.Components{}, fmt.Errorf("configure EdgeTTS: %w", err)
		}
		adapter := speech.PipelineAdapter{
			ASRProvider: funASR,
			TTSProvider: edge,
			ASRFormat:   speech.AudioFormat{SampleRate: 16000, Channels: 1},
			TTSFormat:   speech.AudioFormat{SampleRate: 24000, Channels: 1},
			Locale:      value("COMPANION_SPEECH_LOCALE", "vi-VN"),
			Voice:       value("EDGE_TTS_VOICE", "vi-VN-HoaiMyNeural"),
		}
		if err := adapter.Validate(); err != nil {
			return pipeline.Components{}, err
		}
		components.ASR = adapter
		components.TTS = adapter
		return components, nil

	case speechProfileReferenceStreaming:
		xunfei, err := speech.NewXunfeiStreamASR(speech.XunfeiStreamASRConfig{
			URL:               os.Getenv("XUNFEI_ASR_URL"),
			AppID:             os.Getenv("XUNFEI_ASR_APP_ID"),
			APIKey:            os.Getenv("XUNFEI_ASR_API_KEY"),
			APISecret:         os.Getenv("XUNFEI_ASR_API_SECRET"),
			Language:          value("XUNFEI_ASR_LANGUAGE", "zh_cn"),
			Accent:            value("XUNFEI_ASR_ACCENT", "mandarin"),
			Domain:            value("XUNFEI_ASR_DOMAIN", "iat"),
			VADMS:             envInt("XUNFEI_ASR_VAD_MS", 1000),
			DynamicCorrection: envBool("XUNFEI_ASR_DYNAMIC_CORRECTION", true),
		})
		if err != nil {
			return pipeline.Components{}, fmt.Errorf("configure Xunfei ASR: %w", err)
		}
		huoshan, err := speech.NewHuoshanDoubleStreamTTS(speech.HuoshanDoubleStreamTTSConfig{
			URL:         os.Getenv("HUOSHAN_TTS_URL"),
			AppID:       os.Getenv("HUOSHAN_TTS_APP_ID"),
			AccessToken: os.Getenv("HUOSHAN_TTS_ACCESS_TOKEN"),
			ResourceID:  os.Getenv("HUOSHAN_TTS_RESOURCE_ID"),
			Speaker:     os.Getenv("HUOSHAN_TTS_SPEAKER"),
			AudioParams: map[string]any{
				"speech_rate":   envInt("HUOSHAN_TTS_SPEECH_RATE", 0),
				"loudness_rate": envInt("HUOSHAN_TTS_LOUDNESS_RATE", 0),
			},
		})
		if err != nil {
			return pipeline.Components{}, fmt.Errorf("configure Huoshan TTS: %w", err)
		}
		adapter := speech.PipelineAdapter{
			ASRProvider: xunfei,
			TTSProvider: huoshan,
			ASRFormat:   speech.AudioFormat{SampleRate: 16000, Channels: 1},
			TTSFormat:   speech.AudioFormat{SampleRate: envInt("HUOSHAN_TTS_SAMPLE_RATE", 24000), Channels: 1},
			Locale:      value("COMPANION_SPEECH_LOCALE", "vi-VN"),
			Voice:       os.Getenv("HUOSHAN_TTS_SPEAKER"),
		}
		if err := adapter.Validate(); err != nil {
			return pipeline.Components{}, err
		}
		components.ASR = adapter
		components.TTS = adapter
		return components, nil

	default:
		return pipeline.Components{}, fmt.Errorf("unsupported COMPANION_SPEECH_PROFILE %q", profile)
	}
}

func requireExecutable(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("executable command is empty")
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("%q not found in PATH: %w", command, err)
	}
	return nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}


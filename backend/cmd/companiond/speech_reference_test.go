package main

import (
	"testing"

	"companion-server/internal/runtimeconfig"
	"companion-server/internal/speech"
)

func TestConfigureSpeechComponentsMockRequiresAllowMock(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "mock")
	if _, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false}); err == nil {
		t.Fatal("mock speech profile unexpectedly accepted when mocks are disabled")
	}
}

func TestConfigureSpeechComponentsLocalReference(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", speechProfileReferenceLocal)
	t.Setenv("FUNASR_BASE_URL", "http://127.0.0.1:10095")
	t.Setenv("FUNASR_MODEL", "custom")
	t.Setenv("EDGE_TTS_URL", "ws://127.0.0.1:10096")
	components, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false})
	if err != nil {
		t.Fatal(err)
	}
	if components.ASR == nil || components.TTS == nil || components.Codecs == nil {
		t.Fatalf("incomplete components=%+v", components)
	}
	if _, ok := components.ASR.(speech.PipelineAdapter); !ok {
		t.Fatalf("ASR type=%T; want provider-neutral speech adapter", components.ASR)
	}
}

func TestConfigureSpeechComponentsLocalReferenceRequiresExplicitFunASRModel(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", speechProfileReferenceLocal)
	t.Setenv("FUNASR_BASE_URL", "http://127.0.0.1:10095")
	t.Setenv("FUNASR_MODEL", "")
	if _, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false}); err == nil {
		t.Fatal("reference-local silently accepted a missing FunASR model")
	}
}

func TestConfigureSpeechComponentsStreamingReferenceFailsClosedThenBuilds(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", speechProfileReferenceStreaming)
	if _, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false}); err == nil {
		t.Fatal("streaming reference unexpectedly accepted without credentials")
	}

	t.Setenv("XUNFEI_ASR_URL", "ws://127.0.0.1:10097")
	t.Setenv("XUNFEI_ASR_APP_ID", "app")
	t.Setenv("XUNFEI_ASR_API_KEY", "key")
	t.Setenv("XUNFEI_ASR_API_SECRET", "secret")
	t.Setenv("HUOSHAN_TTS_URL", "ws://127.0.0.1:10098")
	t.Setenv("HUOSHAN_TTS_APP_ID", "app")
	t.Setenv("HUOSHAN_TTS_ACCESS_TOKEN", "token")
	t.Setenv("HUOSHAN_TTS_RESOURCE_ID", "seed-tts-2.0")
	t.Setenv("HUOSHAN_TTS_SPEAKER", "voice")
	components, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false})
	if err != nil {
		t.Fatal(err)
	}
	if components.ASR == nil || components.TTS == nil || components.Codecs == nil {
		t.Fatalf("incomplete components=%+v", components)
	}
}

func TestConfigureSpeechComponentsProductionRequiresExplicitProfile(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "")
	if _, err := configureSpeechComponents(runtimeconfig.Config{AllowMock: false}); err == nil {
		t.Fatal("real speech configuration silently defaulted without a profile")
	}
}

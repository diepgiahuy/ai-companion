package main

import (
	"os"
	"strings"
	"testing"

	"companion-server/internal/runtimeconfig"
)

func TestConfigureSpeechComponentsDefaultsToMockOnlyWhenAllowed(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "")
	t.Setenv("MOCK_TRANSCRIPT", "tier1")
	components, err := configureSpeechComponents(runtimeconfig.Config{AllowMock:true})
	if err != nil { t.Fatal(err) }
	if components.ASR == nil || components.TTS == nil || components.Codecs == nil { t.Fatal("mock speech components incomplete") }
}

func TestConfigureSpeechComponentsFailsClosedWithoutProfile(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "")
	_, err := configureSpeechComponents(runtimeconfig.Config{AllowMock:false})
	if err == nil || !strings.Contains(err.Error(), "COMPANION_SPEECH_PROFILE") { t.Fatalf("error=%v", err) }
}

func TestConfigureSpeechComponentsRejectsMockInProduction(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "mock")
	_, err := configureSpeechComponents(runtimeconfig.Config{AllowMock:false})
	if err == nil || !strings.Contains(err.Error(), "forbidden") { t.Fatalf("error=%v", err) }
}

func TestConfigureSpeechComponentsLocalRequiresExplicitFunASR(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "reference-local")
	_ = os.Unsetenv("FUNASR_BASE_URL")
	_ = os.Unsetenv("FUNASR_MODEL")
	_, err := configureSpeechComponents(runtimeconfig.Config{AllowMock:false})
	if err == nil || !strings.Contains(err.Error(), "FunASR") { t.Fatalf("error=%v", err) }
}

func TestConfigureSpeechComponentsStreamingRequiresCredentials(t *testing.T) {
	t.Setenv("COMPANION_SPEECH_PROFILE", "reference-streaming")
	_, err := configureSpeechComponents(runtimeconfig.Config{AllowMock:false})
	if err == nil || !strings.Contains(err.Error(), "Xunfei") { t.Fatalf("error=%v", err) }
}

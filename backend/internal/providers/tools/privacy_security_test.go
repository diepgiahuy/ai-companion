package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	"companion-server/internal/privacy"
	"companion-server/internal/store"
)

func TestVoiceMemoSaveFailsClosedUntilExplicitVoiceConsent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "voice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	privacyService := privacy.New(data)
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{
		Store: data, VoicePrivacy: privacyService, RecordingsDir: filepath.Join(dir, "recordings"),
	}); err != nil {
		t.Fatal(err)
	}
	turnCtx := pipeline.WithTurnContext(ctx, pipeline.TurnContext{
		UserID: "user-a", DeviceID: "device-a", PCM16Mono: []byte{1, 0, 2, 0, 3, 0, 4, 0}, SampleRate: 16000, Transcript: "private voice",
	})

	denied := registry.Execute(turnCtx, "voice_memo.save", capability.ToolRequest{Key: "voice-privacy-key", Arguments: `{}`})
	if !strings.Contains(denied.Content, "voice audio persistence disabled by user privacy policy") {
		t.Fatalf("missing-consent result=%s", denied.Content)
	}
	if items, err := data.ListVoiceMemos(ctx, "user-a", "", 10); err != nil || len(items) != 0 {
		t.Fatalf("denied save persisted DB rows=%+v err=%v", items, err)
	}
	if files, err := filepath.Glob(filepath.Join(dir, "recordings", "*.wav")); err != nil || len(files) != 0 {
		t.Fatalf("denied save persisted files=%v err=%v", files, err)
	}

	if err := privacyService.Set(ctx, privacy.Policy{UserID: "user-a", SaveVoiceAudio: true}); err != nil {
		t.Fatal(err)
	}
	allowed := registry.Execute(turnCtx, "voice_memo.save", capability.ToolRequest{Key: "voice-privacy-key", Arguments: `{}`})
	if !strings.Contains(allowed.Content, `"ok":true`) {
		t.Fatalf("explicit-consent result=%s", allowed.Content)
	}
	if items, err := data.ListVoiceMemos(ctx, "user-a", "", 10); err != nil || len(items) != 1 {
		t.Fatalf("allowed save rows=%+v err=%v", items, err)
	}
}

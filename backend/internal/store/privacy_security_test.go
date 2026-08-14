package store

import (
	"context"
	"path/filepath"
	"testing"

	"companion-server/internal/privacy"
)

func TestPrivacyConsentAndRevocationPersistAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "privacy.db")

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	privacyService := privacy.New(first)
	if privacyService.MemoryAllowed(ctx, "user-a") || privacyService.VoiceAudioAllowed(ctx, "user-a") {
		t.Fatal("missing policy must deny persistence before restart")
	}
	if err := privacyService.Set(ctx, privacy.Policy{UserID: "user-a", SaveVoiceAudio: true, LongTermMemoryEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	privacyService = privacy.New(second)
	if !privacyService.MemoryAllowed(ctx, "user-a") || !privacyService.VoiceAudioAllowed(ctx, "user-a") {
		t.Fatal("explicit consent did not survive SQLite restart")
	}
	if err := privacyService.Set(ctx, privacy.Policy{UserID: "user-a", SaveVoiceAudio: false, LongTermMemoryEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	privacyService = privacy.New(third)
	if privacyService.MemoryAllowed(ctx, "user-a") || privacyService.VoiceAudioAllowed(ctx, "user-a") {
		t.Fatal("consent revocation did not survive SQLite restart")
	}
}

func TestVoiceMemoOwnershipPreventsCrossUserReadAndDelete(t *testing.T) {
	ctx := context.Background()
	data, err := Open(filepath.Join(t.TempDir(), "owners.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	if err := data.CreateVoiceMemo(ctx, "user-a", "memo-key", "device-a", "/recordings/a.wav", "private memo", 1000); err != nil {
		t.Fatal(err)
	}
	owned, ok, err := data.VoiceMemoByKey(ctx, "user-a", "memo-key")
	if err != nil || !ok {
		t.Fatalf("owner lookup ok=%v err=%v", ok, err)
	}
	if _, ok, err := data.VoiceMemoByID(ctx, "user-b", owned.ID); err != nil || ok {
		t.Fatalf("cross-owner lookup ok=%v err=%v; want hidden", ok, err)
	}
	if items, err := data.ListVoiceMemos(ctx, "user-b", "", 10); err != nil || len(items) != 0 {
		t.Fatalf("cross-owner list=%+v err=%v", items, err)
	}
	if err := data.DeleteVoiceMemo(ctx, "user-b", owned.ID); err == nil {
		t.Fatal("cross-owner delete unexpectedly succeeded")
	}
	if _, ok, err := data.VoiceMemoByID(ctx, "user-a", owned.ID); err != nil || !ok {
		t.Fatalf("cross-owner delete affected owner row: ok=%v err=%v", ok, err)
	}
}

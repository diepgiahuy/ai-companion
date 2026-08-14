package tools

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	"companion-server/internal/store"
)

func TestVoiceMemoRetryReusesDeterministicBlobAfterPrecommitDBFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "voice.db")
	recordings := filepath.Join(dir, "recordings")
	data, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data, RecordingsDir: recordings}); err != nil {
		t.Fatal(err)
	}
	turn := pipeline.TurnContext{
		UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a",
		PCM16Mono: []byte{1, 0, 2, 0, 3, 0, 4, 0}, SampleRate: 16000, Transcript: "memo",
	}
	ctx := pipeline.WithTurnContext(context.Background(), turn)

	// Replay/lookup must stay healthy so the tool reaches the filesystem write.
	// A second SQLite connection installs a trigger that aborts only the later
	// authoritative voice_memos INSERT, modelling a pre-commit DB failure after
	// the deterministic WAV has already been created.
	faultDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer faultDB.Close()
	if _, err := faultDB.Exec(`CREATE TRIGGER fail_voice_memo_insert BEFORE INSERT ON voice_memos BEGIN SELECT RAISE(ABORT, 'injected voice memo insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-crash-key", Arguments: `{}`})
	if strings.Contains(failed.Content, `"ok":true`) || !strings.Contains(failed.Content, "injected voice memo insert failure") {
		t.Fatalf("save did not expose injected precommit failure: %s", failed.Content)
	}
	files, err := filepath.Glob(filepath.Join(recordings, "*.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("precommit failure files=%v; want one deterministic recoverable blob", files)
	}
	orphanPath := files[0]
	if _, err := faultDB.Exec(`DROP TRIGGER fail_voice_memo_insert`); err != nil {
		t.Fatal(err)
	}

	// Exact retry reuses/overwrites the same deterministic path and commits one
	// durable memo; it must not accumulate a second blob name.
	retried := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-crash-key", Arguments: `{}`})
	id := toolResultID(t, retried)
	if id < 1 {
		t.Fatalf("invalid committed voice memo id=%d", id)
	}
	files, err = filepath.Glob(filepath.Join(recordings, "*.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != orphanPath {
		t.Fatalf("retry created duplicate blob: files=%v original=%s", files, orphanPath)
	}
	items, err := data.ListVoiceMemos(ctx, "user-a", "device-a", 10)
	if err != nil || len(items) != 1 || items[0].ID != id || items[0].Path != orphanPath {
		t.Fatalf("committed voice memos=%+v err=%v", items, err)
	}
}

func TestVoiceMemoReplayRestoresMissingCommittedBlob(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "voice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	recordings := filepath.Join(dir, "recordings")
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data, RecordingsDir: recordings}); err != nil {
		t.Fatal(err)
	}
	turn := pipeline.TurnContext{
		UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a",
		PCM16Mono: []byte{5, 0, 6, 0, 7, 0, 8, 0}, SampleRate: 16000, Transcript: "memo",
	}
	ctx := pipeline.WithTurnContext(context.Background(), turn)
	first := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-restore-key", Arguments: `{}`})
	id := toolResultID(t, first)
	items, err := data.ListVoiceMemos(ctx, "user-a", "device-a", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("voice memos=%+v err=%v", items, err)
	}
	path := items[0].Path
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	replayed := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-restore-key", Arguments: `{}`})
	if got := toolResultID(t, replayed); got != id {
		t.Fatalf("replayed id=%d; want %d", got, id)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replay did not restore missing committed blob: %v", err)
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	conversationprovider "companion-server/internal/providers/conversation"
	"companion-server/internal/store"
)

func TestVoiceMemoDurableSaveConflictAndDeleteReplay(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	recordings := filepath.Join(t.TempDir(), "recordings")
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data, RecordingsDir: recordings}); err != nil {
		t.Fatal(err)
	}
	turn := pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a", PCM16Mono: []byte{1, 0, 2, 0, 3, 0, 4, 0}, SampleRate: 16000, Transcript: "memo"}
	ctx := pipeline.WithTurnContext(context.Background(), turn)

	first := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-raw-key", Arguments: `{}`})
	id := toolResultID(t, first)
	items, err := data.ListVoiceMemos(ctx, "user-a", "device-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("voice memos = %+v; want one id %d", items, id)
	}
	path := items[0].Path
	if strings.Contains(filepath.Base(path), "voice-raw-key") {
		t.Fatalf("voice memo path leaks raw idempotency key: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("voice memo file missing after commit: %v", err)
	}

	replayed := registry.Execute(ctx, "voice_memo.save", capability.ToolRequest{Key: "voice-raw-key", Arguments: `{}`})
	if got := toolResultID(t, replayed); got != id {
		t.Fatalf("replayed id = %d; want %d", got, id)
	}

	changed := turn
	changed.PCM16Mono = []byte{9, 0, 8, 0, 7, 0, 6, 0}
	conflictCtx := pipeline.WithTurnContext(context.Background(), changed)
	conflict := registry.Execute(conflictCtx, "voice_memo.save", capability.ToolRequest{Key: "voice-raw-key", Arguments: `{}`})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("different audio retry = %s", conflict.Content)
	}
	files, err := filepath.Glob(filepath.Join(recordings, "*.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != path {
		t.Fatalf("recording files after conflict = %v; want only %s", files, path)
	}

	deleted := registry.Execute(ctx, "voice_memo.delete", capability.ToolRequest{Key: "voice-delete-key", Arguments: `{"id":` + itoa64(id) + `}`})
	if got := toolResultID(t, deleted); got != id {
		t.Fatalf("deleted id = %d; want %d", got, id)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("voice memo file still exists after delete: %v", err)
	}
	replayedDelete := registry.Execute(ctx, "voice_memo.delete", capability.ToolRequest{Key: "voice-delete-key", Arguments: `{"id":` + itoa64(id) + `}`})
	if got := toolResultID(t, replayedDelete); got != id {
		t.Fatalf("replayed delete id = %d; want %d", got, id)
	}
	items, err = data.ListVoiceMemos(ctx, "user-a", "device-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("voice memos after replayed delete = %+v", items)
	}
}

func TestConversationClearReplayDoesNotClearPostCommitMessages(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	service := conversationctx.New(conversationprovider.NewSQLite(data), conversationctx.NewMemoryCache(time.Hour, 10))
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data, Conversation: service}); err != nil {
		t.Fatal(err)
	}
	scope := conversationctx.Scope{UserID: "user-a", ThreadID: "thread-a"}
	if err := service.Append(context.Background(), "old", scope, "user", "old message"); err != nil {
		t.Fatal(err)
	}
	turn := pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a", ThreadID: "thread-a", Transcript: "clear this conversation"}
	ctx := pipeline.WithTurnContext(context.Background(), turn)
	first := registry.Execute(ctx, "conversation.clear", capability.ToolRequest{Key: "clear-key", Arguments: `{"confirm":true}`})
	if !strings.Contains(first.Content, `"ok":true`) {
		t.Fatalf("first clear = %s", first.Content)
	}
	if err := service.Append(context.Background(), "assistant-after-clear", scope, "assistant", "cleared"); err != nil {
		t.Fatal(err)
	}
	replayed := registry.Execute(ctx, "conversation.clear", capability.ToolRequest{Key: "clear-key", Arguments: `{"confirm":true}`})
	if !strings.Contains(replayed.Content, `"ok":true`) {
		t.Fatalf("replayed clear = %s", replayed.Content)
	}
	messages, err := service.Recent(context.Background(), scope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" || messages[1].Content != "cleared" {
		t.Fatalf("messages after replayed clear = %+v; post-clear assistant message was lost or clear request duplicated", messages)
	}

	otherScope := conversationctx.Scope{UserID: "user-a", ThreadID: "thread-b"}
	if err := service.Append(context.Background(), "other-old", otherScope, "user", "keep me"); err != nil {
		t.Fatal(err)
	}
	otherTurn := turn
	otherTurn.ThreadID = "thread-b"
	otherCtx := pipeline.WithTurnContext(context.Background(), otherTurn)
	conflict := registry.Execute(otherCtx, "conversation.clear", capability.ToolRequest{Key: "clear-key", Arguments: `{"confirm":true}`})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("cross-thread same-key clear = %s", conflict.Content)
	}
	otherMessages, err := service.Recent(context.Background(), otherScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherMessages) != 1 || otherMessages[0].Content != "keep me" {
		t.Fatalf("conflicting clear mutated other thread: %+v", otherMessages)
	}
}

func toolResultID(t *testing.T, result capability.ToolResult) int64 {
	t.Helper()
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, result.Content)
	}
	if !envelope.OK {
		t.Fatalf("tool failed: %s", envelope.Error)
	}
	return envelope.ID
}

func itoa64(v int64) string {
	return fmt.Sprintf("%d", v)
}

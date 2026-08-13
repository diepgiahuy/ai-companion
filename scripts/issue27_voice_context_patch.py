#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
native = ROOT / "backend/internal/providers/tools/native.go"
text = native.read_text(encoding="utf-8")

voice_save_old = r'''\t\tdefine("voice", "voice_memo.save", "Lưu audio lượt hiện tại thành WAV", obj(map[string]any{}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
\t\t\tturn, ok := pipeline.CurrentTurn(ctx)
\t\t\tif !ok || len(turn.PCM16Mono) == 0 || turn.SampleRate <= 0 {
\t\t\t\treturn capability.Failure(fmt.Errorf("current turn audio unavailable"))
\t\t\t}
\t\t\tu := currentUser(ctx)
\t\t\tif d.VoicePrivacy != nil && !d.VoicePrivacy.VoiceAudioAllowed(ctx, u) {
\t\t\t\treturn capability.Failure(fmt.Errorf("voice audio persistence disabled by user privacy policy"))
\t\t\t}
\t\t\tif x, ok, err := d.Store.VoiceMemoByKey(ctx, u, r.Key); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t} else if ok {
\t\t\t\treturn capability.Success(map[string]any{"saved": "voice_memo", "id": x.ID, "duration_ms": x.DurationMS})
\t\t\t}
\t\t\tsum := sha256.Sum256([]byte(r.Key))
\t\t\tpath := filepath.Join(d.RecordingsDir, "memo-"+hex.EncodeToString(sum[:8])+".wav")
\t\t\tif err := recording.WritePCM16MonoWAV(path, turn.PCM16Mono, turn.SampleRate); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tms := int64(len(turn.PCM16Mono)/2) * 1000 / int64(turn.SampleRate)
\t\t\tif err := d.Store.CreateVoiceMemo(ctx, u, r.Key, turn.DeviceID, path, turn.Transcript, ms); err != nil {
\t\t\t\t_ = os.Remove(path)
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tx, _, err := d.Store.VoiceMemoByKey(ctx, u, r.Key)
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\treturn capability.Success(map[string]any{"saved": "voice_memo", "id": x.ID, "duration_ms": ms})
\t\t}),
'''.replace('\\t', '\t')
voice_save_new = r'''\t\tdefine("voice", "voice_memo.save", "Lưu audio lượt hiện tại thành WAV", obj(map[string]any{}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
\t\t\tturn, ok := pipeline.CurrentTurn(ctx)
\t\t\tif !ok || len(turn.PCM16Mono) == 0 || turn.SampleRate <= 0 {
\t\t\t\treturn capability.Failure(fmt.Errorf("current turn audio unavailable"))
\t\t\t}
\t\t\tu := currentUser(ctx)
\t\t\tif d.VoicePrivacy != nil && !d.VoicePrivacy.VoiceAudioAllowed(ctx, u) {
\t\t\t\treturn capability.Failure(fmt.Errorf("voice audio persistence disabled by user privacy policy"))
\t\t\t}
\t\t\taudioHash := sha256.Sum256(turn.PCM16Mono)
\t\t\trequest, err := durableMutationRequest(ctx, "voice_memo.save", r.Key, map[string]any{
\t\t\t\t"audio_sha256": hex.EncodeToString(audioHash[:]),
\t\t\t\t"sample_rate":  turn.SampleRate,
\t\t\t\t"device_id":    strings.TrimSpace(turn.DeviceID),
\t\t\t})
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tif memo, replayed, err := d.Store.ReplayVoiceMemoMutation(ctx, request); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t} else if replayed {
\t\t\t\tif _, statErr := os.Stat(memo.Path); os.IsNotExist(statErr) {
\t\t\t\t\tif err := recording.WritePCM16MonoWAV(memo.Path, turn.PCM16Mono, turn.SampleRate); err != nil {
\t\t\t\t\t\treturn capability.Failure(fmt.Errorf("restore committed voice memo file: %w", err))
\t\t\t\t\t}
\t\t\t\t} else if statErr != nil {
\t\t\t\t\treturn capability.Failure(fmt.Errorf("inspect committed voice memo file: %w", statErr))
\t\t\t\t}
\t\t\t\treturn capability.Success(map[string]any{"saved": "voice_memo", "id": memo.ID, "duration_ms": memo.DurationMS})
\t\t\t}
\t\t\tpathHash := sha256.Sum256([]byte(request.Actor + "\\x00" + request.Key + "\\x00" + request.RequestHash))
\t\t\tpath := filepath.Join(d.RecordingsDir, "memo-"+hex.EncodeToString(pathHash[:16])+".wav")
\t\t\tif err := recording.WritePCM16MonoWAV(path, turn.PCM16Mono, turn.SampleRate); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tms := int64(len(turn.PCM16Mono)/2) * 1000 / int64(turn.SampleRate)
\t\t\tmemo, err := d.Store.CreateVoiceMemoMutation(ctx, request, u, turn.DeviceID, path, turn.Transcript, ms)
\t\t\tif err != nil {
\t\t\t\tif idempotency.IsConflict(err) {
\t\t\t\t\t_ = os.Remove(path)
\t\t\t\t}
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\treturn capability.Success(map[string]any{"saved": "voice_memo", "id": memo.ID, "duration_ms": memo.DurationMS})
\t\t}),
'''.replace('\\t', '\t').replace('"\\x00"', '"\\x00"')
if text.count(voice_save_old) != 1:
    raise SystemExit(f"voice save block drifted: {text.count(voice_save_old)}")
text = text.replace(voice_save_old, voice_save_new)

voice_delete_old = r'''\t\tdefine("voice", "voice_memo.delete", "Xóa voice memo và file WAV", obj(map[string]any{"id": idField}, "id"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
\t\t\tvar a struct {
\t\t\t\tID int64 `json:"id"`
\t\t\t}
\t\t\tif err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tu := currentUser(ctx)
\t\t\tmemo, ok, err := d.Store.VoiceMemoByID(ctx, u, a.ID)
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tif !ok {
\t\t\t\treturn capability.Failure(fmt.Errorf("voice memo not found"))
\t\t\t}
\t\t\tif err := os.Remove(memo.Path); err != nil && !os.IsNotExist(err) {
\t\t\t\treturn capability.Failure(fmt.Errorf("delete voice memo file: %w", err))
\t\t\t}
\t\t\tif err := d.Store.DeleteVoiceMemo(ctx, u, a.ID); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\treturn capability.Success(map[string]any{"deleted": "voice_memo", "id": a.ID})
\t\t}),
'''.replace('\\t', '\t')
voice_delete_new = r'''\t\tdefine("voice", "voice_memo.delete", "Xóa voice memo và file WAV", obj(map[string]any{"id": idField}, "id"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
\t\t\tvar a struct {
\t\t\t\tID int64 `json:"id"`
\t\t\t}
\t\t\tif err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\trequest, err := durableMutationRequest(ctx, "voice_memo.delete", r.Key, map[string]any{"id": a.ID})
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tmemo, err := d.Store.DeleteVoiceMemoMutation(ctx, request, currentUser(ctx), a.ID)
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tif err := os.Remove(memo.Path); err != nil && !os.IsNotExist(err) {
\t\t\t\treturn capability.Failure(fmt.Errorf("delete committed voice memo file: %w", err))
\t\t\t}
\t\t\treturn capability.Success(map[string]any{"deleted": "voice_memo", "id": memo.ID})
\t\t}),
'''.replace('\\t', '\t')
if text.count(voice_delete_old) != 1:
    raise SystemExit(f"voice delete block drifted: {text.count(voice_delete_old)}")
text = text.replace(voice_delete_old, voice_delete_new)

clear_old = r'''\t\t\tscope := currentConversationScope(ctx)
\t\t\tif err := d.Conversation.Clear(ctx, scope); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\t// Qwen appends the current user turn before tools execute. Preserve that
\t\t\t// explicit clear request so the post-tool assistant response is not left
\t\t\t// as an orphan message after clearing earlier history.
\t\t\tif turn, ok := pipeline.CurrentTurn(ctx); ok && strings.TrimSpace(turn.Transcript) != "" {
\t\t\t\tkey := "conversation-clear:" + scope.Key() + ":" + strings.TrimSpace(turn.SessionID) + ":" + strings.TrimSpace(turn.TurnID)
\t\t\t\tif err := d.Conversation.Append(ctx, key, scope, "user", turn.Transcript); err != nil {
\t\t\t\t\treturn capability.Failure(err)
\t\t\t\t}
\t\t\t}
'''.replace('\\t', '\t')
clear_new = r'''\t\t\tscope := currentConversationScope(ctx)
\t\t\tthreadID := strings.TrimSpace(scope.ThreadID)
\t\t\tif threadID == "" {
\t\t\t\tthreadID = "default"
\t\t\t}
\t\t\trequest, err := durableMutationRequest(ctx, "conversation.clear", r.Key, map[string]any{"thread_id": threadID, "confirm": true})
\t\t\tif err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\tdurable, ok := d.Conversation.(interface {
\t\t\t\tClearMutation(context.Context, idempotency.Request, conversationctx.Scope) (bool, error)
\t\t\t})
\t\t\tif !ok {
\t\t\t\treturn capability.Failure(fmt.Errorf("durable conversation clear is unavailable"))
\t\t\t}
\t\t\tif _, err := durable.ClearMutation(ctx, request, scope); err != nil {
\t\t\t\treturn capability.Failure(err)
\t\t\t}
\t\t\t// Re-append the explicit clear request with an idempotency-derived key.
\t\t\t// On replay the DB clear callback is skipped and this append is ignored by
\t\t\t// UNIQUE(turn_key,role), preserving post-clear assistant messages.
\t\t\tif turn, ok := pipeline.CurrentTurn(ctx); ok && strings.TrimSpace(turn.Transcript) != "" {
\t\t\t\tappendHash := sha256.Sum256([]byte(request.Actor + "\\x00" + request.Operation + "\\x00" + request.Key))
\t\t\t\tkey := "conversation-clear:" + hex.EncodeToString(appendHash[:16])
\t\t\t\tif err := d.Conversation.Append(ctx, key, scope, "user", turn.Transcript); err != nil {
\t\t\t\t\treturn capability.Failure(err)
\t\t\t\t}
\t\t\t}
'''.replace('\\t', '\t').replace('"\\x00"', '"\\x00"')
if text.count(clear_old) != 1:
    raise SystemExit(f"conversation clear block drifted: {text.count(clear_old)}")
text = text.replace(clear_old, clear_new)
native.write_text(text, encoding="utf-8")

(ROOT / "backend/internal/providers/tools/idempotency_voice_context_test.go").write_text(r'''package tools

import (
	"context"
	"encoding/json"
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
'''.replace('\n\t"encoding/json"', '\n\t"encoding/json"\n\t"fmt"'), encoding="utf-8")
print("voice/context durable idempotency patch applied")

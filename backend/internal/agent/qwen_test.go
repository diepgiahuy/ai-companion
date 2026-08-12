package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	conversationprovider "companion-server/internal/providers/conversation"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/store"
)

func TestQwenToolCallIsSavedOnceAndTurnIsCached(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch requests.Add(1) {
		case 1:
			json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call-1", "type": "function",
						"function": map[string]any{
							"name":      "expense.create",
							"arguments": `{"amount_vnd":50000,"category":"food","description":"đi chợ","occurred_at":"2025-01-02T10:00:00+07:00"}`,
						},
					}},
				},
			}}})
		case 2:
			if len(payload.Messages) < 4 || payload.Messages[len(payload.Messages)-1].Role != "tool" {
				t.Fatalf("second request does not contain tool result")
			}
			if !strings.Contains(payload.Messages[len(payload.Messages)-1].Content, `"ok":true`) {
				t.Fatalf("expense tool failed but model was allowed to report success: %s", payload.Messages[len(payload.Messages)-1].Content)
			}
			json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "Đã lưu 50 nghìn tiền đi chợ."},
			}}})
		default:
			t.Fatalf("unexpected model request; cached turn should not call model again")
		}
	}))
	defer endpoint.Close()

	data, err := store.Open(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	model, err := NewQwen(endpoint.URL, "", "Qwen3-4B-Instruct-2507", "Asia/Ho_Chi_Minh", data, WithToolRegistry(testToolRegistry(t, data, "")))
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		answer, err := model.Respond(context.Background(), "turn-42", "Hôm nay đi chợ 50k")
		if err != nil {
			t.Fatal(err)
		}
		if answer != "Đã lưu 50 nghìn tiền đi chợ." {
			t.Fatalf("unexpected answer %q", answer)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("model request count = %d; want 2", requests.Load())
	}
	from, _ := time.Parse(time.RFC3339, "2025-01-01T00:00:00+07:00")
	to, _ := time.Parse(time.RFC3339, "2025-01-03T00:00:00+07:00")
	total, err := data.ExpenseTotal(context.Background(), "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 50_000 {
		t.Fatalf("expense total = %d; want 50000", total)
	}
}

func TestQwenCanReadExpenseOnLaterTurn(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	when, _ := time.Parse(time.RFC3339, "2025-01-02T10:00:00+07:00")
	if err := data.CreateExpense(context.Background(), "", "seed-expense", 50_000, "food", "đi chợ", when); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call-summary", "type": "function",
						"function": map[string]any{
							"name":      "expense.summary",
							"arguments": `{"from":"2025-01-02T00:00:00+07:00","to":"2025-01-03T00:00:00+07:00"}`,
						},
					}},
				},
			}}})
			return
		}
		toolResult := payload.Messages[len(payload.Messages)-1]
		if toolResult.Role != "tool" || !strings.Contains(toolResult.Content, `"total_vnd":50000`) {
			t.Fatalf("later turn did not receive persisted total: %+v", toolResult)
		}
		json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Hôm đó bạn đã chi 50 nghìn."},
		}}})
	}))
	defer endpoint.Close()

	model, err := NewQwen(endpoint.URL, "", "Qwen3-4B-Instruct-2507", "Asia/Ho_Chi_Minh", data, WithToolRegistry(testToolRegistry(t, data, "")))
	if err != nil {
		t.Fatal(err)
	}
	answer, err := model.Respond(context.Background(), "turn-next-day", "Hôm qua tôi đã chi bao nhiêu?")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Hôm đó bạn đã chi 50 nghìn." {
		t.Fatalf("unexpected answer %q", answer)
	}
}

func TestExpenseLogAcceptsMultipleItems(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "multi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	model, err := NewQwen("http://unused", "", "test", "Asia/Ho_Chi_Minh", data, WithToolRegistry(testToolRegistry(t, data, "")))
	if err != nil {
		t.Fatal(err)
	}
	call := toolCall{Function: functionCall{Name: "expense.log", Arguments: `{"items":[{"amount_vnd":30000,"category":"food","description":"lunch","occurred_at":"2026-08-10T12:00:00+07:00"},{"amount_vnd":20000,"category":"transport","description":"taxi","occurred_at":"2026-08-10T12:30:00+07:00"}]}`}}
	result := model.executeTool(context.Background(), "turn:0:expense.log", call)
	if !strings.Contains(result, `"ok":true`) || !strings.Contains(result, `"count":2`) {
		t.Fatalf("tool result = %s", result)
	}
	from, _ := time.Parse(time.RFC3339, "2026-08-10T00:00:00+07:00")
	to, _ := time.Parse(time.RFC3339, "2026-08-11T00:00:00+07:00")
	total, err := data.ExpenseTotal(context.Background(), "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 50_000 {
		t.Fatalf("total = %d", total)
	}
}

func TestVoiceMemoToolWritesValidWAVAndMetadata(t *testing.T) {
	root := t.TempDir()
	data, err := store.Open(filepath.Join(root, "memo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	model, err := NewQwen("http://unused", "", "test", "Asia/Ho_Chi_Minh", data,
		WithToolRegistry(testToolRegistry(t, data, filepath.Join(root, "recordings"))))
	if err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{
		DeviceID: "device-a", TurnID: "turn-memo", Transcript: "ghi âm ghi chú mua sữa",
		PCM16Mono: make([]byte, 3200), SampleRate: 16000,
	})
	result := model.executeTool(ctx, "turn-memo:0:voice_memo.save", toolCall{Function: functionCall{Name: "voice_memo.save", Arguments: `{}`}})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("tool result = %s", result)
	}
	items, err := data.ListVoiceMemos(context.Background(), "device-a", "device-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Transcript == "" {
		t.Fatalf("memos = %+v", items)
	}
	contents, err := os.ReadFile(items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 44 || string(contents[:4]) != "RIFF" || string(contents[8:12]) != "WAVE" {
		t.Fatalf("invalid wav file")
	}
}

type denyVoicePersistence struct{}

func (denyVoicePersistence) VoiceAudioAllowed(context.Context, string) bool { return false }

func TestVoiceMemoRespectsPrivacyBeforeWritingFile(t *testing.T) {
	root := t.TempDir()
	data, err := store.Open(filepath.Join(root, "privacy-memo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	registry := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(registry, toolprovider.NativeDependencies{Store: data, RecordingsDir: filepath.Join(root, "recordings"), VoicePrivacy: denyVoicePersistence{}}); err != nil {
		t.Fatal(err)
	}
	model, err := NewQwen("http://unused", "", "test", "Asia/Ho_Chi_Minh", data, WithToolRegistry(registry))
	if err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u", DeviceID: "d", PCM16Mono: make([]byte, 3200), SampleRate: 16000})
	result := model.executeTool(ctx, "turn-private:0:voice_memo.save", toolCall{Function: functionCall{Name: "voice_memo.save", Arguments: `{}`}})
	if strings.Contains(result, `"ok":true`) || !strings.Contains(result, "privacy") {
		t.Fatalf("tool result = %s", result)
	}
	entries, err := os.ReadDir(filepath.Join(root, "recordings"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("privacy-disabled voice memo wrote files: %v", entries)
	}
}

func TestTimerCreateUsesBackendClockArithmetic(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "timer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	model, err := NewQwen("http://unused", "", "test", "Asia/Ho_Chi_Minh", data, WithToolRegistry(testToolRegistry(t, data, "")))
	if err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{DeviceID: "device-a"})
	before := time.Now().UTC().Add(29*time.Minute + 59*time.Second)
	result := model.executeTool(ctx, "turn-timer:0:timer.create", toolCall{Function: functionCall{
		Name: "timer.create", Arguments: `{"title":"Hết 30 phút","delay_seconds":1800}`,
	}})
	after := time.Now().UTC().Add(30*time.Minute + time.Second)
	if !strings.Contains(result, `"ok":true`) || !strings.Contains(result, `"delay_seconds":1800`) {
		t.Fatalf("tool result = %s", result)
	}
	items, err := data.ListTimers(context.Background(), "device-a", "device-a", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("reminders = %+v", items)
	}
	if items[0].FireAt.Before(before) || items[0].FireAt.After(after) {
		t.Fatalf("timer fire_at = %s; expected about 30 minutes from now", items[0].FireAt)
	}
}

func TestExpenseQueryBudgetHistoryAndUICard(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "rich.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	when, _ := time.Parse(time.RFC3339, "2026-08-10T10:00:00+07:00")
	if err := data.CreateExpense(ctx, "device-a", "seed-week", 250000, "food", "food", when); err != nil {
		t.Fatal(err)
	}
	if err := data.SetBudget(ctx, "device-a", "weekly", 1000000); err != nil {
		t.Fatal(err)
	}
	_ = data.SaveConversationMessage(ctx, "old-turn", "device-a", "user", "Tôi muốn tiết kiệm tuần này")
	_ = data.SaveConversationMessage(ctx, "old-turn", "device-a", "assistant", "Được, mình sẽ lưu ý.")
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload chatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			found := false
			for _, m := range payload.Messages {
				if m.Content == "Tôi muốn tiết kiệm tuần này" {
					found = true
				}
			}
			if !found {
				t.Fatal("conversation history missing")
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "q1", "type": "function", "function": map[string]any{"name": "expense.query", "arguments": `{"from":"2026-08-10T00:00:00+07:00","to":"2026-08-17T00:00:00+07:00","period":"weekly"}`}}}}}}})
			return
		}
		tool := payload.Messages[len(payload.Messages)-1]
		if !strings.Contains(tool.Content, `"total_vnd":250000`) || !strings.Contains(tool.Content, `"remaining_vnd":750000`) {
			t.Fatalf("bad query %s", tool.Content)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "Tuần này bạn đã chi 250 nghìn, còn 750 nghìn. Nên giữ mức chi còn lại hợp lý."}}}})
	}))
	defer endpoint.Close()
	conversationService := conversationctx.New(conversationprovider.NewSQLite(data), conversationctx.NewMemoryCache(30*time.Minute, 10))
	model, err := NewQwen(endpoint.URL, "", "Qwen3-4B-Instruct-2507", "Asia/Ho_Chi_Minh", data,
		WithToolRegistry(testToolRegistry(t, data, "")), WithConversation(conversationService))
	if err != nil {
		t.Fatal(err)
	}
	turnCtx := pipeline.WithTurnContext(ctx, pipeline.TurnContext{DeviceID: "device-a", SessionID: "s1"})
	result, err := model.RespondRich(turnCtx, "turn-budget", "Tuần này tiêu hết bao nhiêu rồi?")
	if err != nil {
		t.Fatal(err)
	}
	if result.UI == nil || result.UI.Kind != "expense_summary" || result.UI.Progress != 25 {
		t.Fatalf("bad UI %+v", result.UI)
	}
	history, err := data.ConversationHistory(ctx, "device-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 4 || history[len(history)-1].Role != "assistant" {
		t.Fatalf("history %+v", history)
	}
}

func testToolRegistry(t *testing.T, data *store.Store, recordingsDir string) *capability.ToolRegistry {
	t.Helper()
	registry := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(registry, toolprovider.NativeDependencies{Store: data, RecordingsDir: recordingsDir}); err != nil {
		t.Fatal(err)
	}
	return registry
}

package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestQwenRealtimeManualAudioToolRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("model") != "qwen-audio-test" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-DashScope-WorkSpace") != "workspace" {
			t.Errorf("handshake model=%q auth=%q workspace=%q", r.URL.Query().Get("model"), r.Header.Get("Authorization"), r.Header.Get("X-DashScope-WorkSpace"))
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil { t.Errorf("accept: %v", err); return }
		defer conn.CloseNow()
		ctx := r.Context()

		update := readQwenJSON(t, ctx, conn)
		if update["type"] != "session.update" { t.Errorf("update=%#v", update); return }
		session := update["session"].(map[string]any)
		if value, exists := session["turn_detection"]; !exists || value != nil {
			t.Errorf("manual turn_detection=%#v exists=%v", value, exists); return
		}
		tools := session["tools"].([]any)
		function := tools[0].(map[string]any)["function"].(map[string]any)
		providerTool := function["name"].(string)
		if providerTool == "note.create" || strings.Contains(providerTool, ".") {
			t.Errorf("unsafe provider tool=%q", providerTool); return
		}

		appendEvent := readQwenJSON(t, ctx, conn)
		pcm, err := base64.StdEncoding.DecodeString(appendEvent["audio"].(string))
		if err != nil || string(pcm) != string([]byte{1, 2, 3, 4}) {
			t.Errorf("append=%#v pcm=%v err=%v", appendEvent, pcm, err); return
		}
		if commit := readQwenJSON(t, ctx, conn); commit["type"] != "input_audio_buffer.commit" {
			t.Errorf("commit=%#v", commit); return
		}
		if create := readQwenJSON(t, ctx, conn); create["type"] != "response.create" {
			t.Errorf("create=%#v", create); return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.delta","text":"xin ","stash":"chào"}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.audio.delta","delta":"AQIDBA=="}`))
		toolEvent, _ := json.Marshal(map[string]any{"type": "response.function_call_arguments.done", "call_id": "call-1", "name": providerTool, "arguments": `{"content":"hello"}`})
		_ = conn.Write(ctx, websocket.MessageText, toolEvent)

		result := readQwenJSON(t, ctx, conn)
		if result["type"] != "conversation.item.create" {
			t.Errorf("tool result=%#v", result); return
		}
		item := result["item"].(map[string]any)
		if item["type"] != "function_call_output" || item["call_id"] != "call-1" || item["output"] != `{"ok":true}` {
			t.Errorf("tool output item=%#v", item); return
		}
		if followup := readQwenJSON(t, ctx, conn); followup["type"] != "response.create" {
			t.Errorf("followup=%#v", followup); return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.done","response":{"status":"completed"}}`))
	}))
	defer server.Close()

	provider, err := NewQwenRealtime(QwenRealtimeConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Model: "qwen-audio-test", APIKey: "test-key", WorkspaceID: "workspace",
		Voice: "longanqian", TurnDetection: "manual",
		Tools: []NativeRealtimeTool{{Name: "note.create", Description: "create note", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := provider.Connect(ctx)
	if err != nil { t.Fatal(err) }
	defer session.Close()
	if err := session.AppendAudio(ctx, []byte{1, 2, 3, 4}); err != nil { t.Fatal(err) }
	if err := session.CommitAudio(ctx); err != nil { t.Fatal(err) }
	if err := session.CreateResponse(ctx); err != nil { t.Fatal(err) }

	transcript, err := session.Receive(ctx)
	if err != nil { t.Fatal(err) }
	if transcript.InputTranscript != "xin chào" || transcript.InputFinal {
		t.Fatalf("transcript=%+v", transcript)
	}
	audio, err := session.Receive(ctx)
	if err != nil { t.Fatal(err) }
	if string(audio.AudioPCM) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio=%v", audio.AudioPCM)
	}
	toolCall, err := session.Receive(ctx)
	if err != nil { t.Fatal(err) }
	if toolCall.ToolCall == nil || toolCall.ToolCall.Name != "note.create" || toolCall.ToolCall.CallID != "call-1" || toolCall.ToolCall.Arguments["content"] != "hello" {
		t.Fatalf("tool call=%+v", toolCall)
	}
	if err := session.ReturnToolResult(ctx, toolCall.ToolCall.CallID, `{"ok":true}`); err != nil { t.Fatal(err) }
	done, err := session.Receive(ctx)
	if err != nil { t.Fatal(err) }
	if !done.ResponseDone || done.ResponseStatus != "completed" {
		t.Fatalf("done=%+v", done)
	}
}

func TestQwenRealtimeFailsClosedWithoutRequiredConfig(t *testing.T) {
	if _, err := NewQwenRealtime(QwenRealtimeConfig{}); err == nil {
		t.Fatal("missing Qwen Realtime config unexpectedly accepted")
	}
	if _, err := NewQwenRealtime(QwenRealtimeConfig{URL: "http://example.com/realtime", Model: "qwen", APIKey: "key"}); err == nil {
		t.Fatal("insecure remote Qwen Realtime URL unexpectedly accepted")
	}
}

func TestQwenRealtimeRejectsOddPCM16BytesBeforeNetworkWrite(t *testing.T) {
	session := &qwenRealtimeSession{}
	if err := session.AppendAudio(context.Background(), []byte{1, 2, 3}); err == nil || !strings.Contains(err.Error(), "even number of bytes") {
		t.Fatalf("odd PCM error=%v", err)
	}
	if err := session.AppendAudio(context.Background(), nil); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("empty PCM unexpectedly failed: %v", err)
	}
}

func readQwenJSON(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	kind, raw, err := conn.Read(ctx)
	if err != nil { t.Fatalf("read Qwen client event: %v", err) }
	if kind != websocket.MessageText { t.Fatalf("Qwen client event kind=%v", kind) }
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil { t.Fatalf("decode Qwen client event: %v", err) }
	return value
}

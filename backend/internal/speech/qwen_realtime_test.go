package speech

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestQwenRealtimeToolAliasIsProviderSafeAndStable(t *testing.T) {
	first := qwenRealtimeToolAlias("expense.log")
	second := qwenRealtimeToolAlias("expense.log")
	other := qwenRealtimeToolAlias("expense-log")
	if first != second {
		t.Fatalf("alias not stable: %q != %q", first, second)
	}
	if first == other {
		t.Fatalf("distinct canonical tools collided: %q", first)
	}
	if ok, _ := regexp.MatchString(`^[A-Za-z0-9_-]+$`, first); !ok {
		t.Fatalf("unsafe alias=%q", first)
	}
}

func TestQwenRealtimeToolsPreserveCanonicalMapping(t *testing.T) {
	aliases, tools, err := qwenRealtimeTools([]NativeRealtimeTool{{Name: "memory.remember", Description: "save memory", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || len(aliases) != 1 {
		t.Fatalf("tools=%d aliases=%d", len(tools), len(aliases))
	}
	for alias, canonical := range aliases {
		if canonical != "memory.remember" || alias == canonical {
			t.Fatalf("mapping=%q -> %q", alias, canonical)
		}
	}
}

func TestQwenRealtimeConfigFailsClosed(t *testing.T) {
	if _, err := NewQwenRealtime(QwenRealtimeConfig{URL: "ws://example.com/realtime", Model: "m", APIKey: "k"}); err == nil {
		t.Fatal("plaintext remote websocket must fail")
	}
	if _, err := NewQwenRealtime(QwenRealtimeConfig{URL: "wss://example.com/realtime", Model: "m"}); err == nil {
		t.Fatal("missing API key must fail")
	}
}

func TestQwenRealtimeSessionMockFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		// Read session.update
		_, _, err = conn.Read(r.Context())
		if err != nil {
			return
		}

		// Read input_audio_buffer.append
		_, _, _ = conn.Read(r.Context())

		// Read input_audio_buffer.commit
		_, _, _ = conn.Read(r.Context())

		// Send transcript delta
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.delta","text":"xin "}`))
		// Send transcript completed
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"xin chào"}`))
		// Send response audio delta (PCM16 base64)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.audio.delta","delta":"AQAC"}`))
		// Send response done
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.done","response":{"status":"completed"}}`))
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewQwenRealtime(QwenRealtimeConfig{
		URL:    wsURL,
		Model:  "qwen-audio",
		APIKey: "mock-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	session, err := provider.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// Append audio with odd samples should fail
	if err := session.AppendAudio(context.Background(), []byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on odd PCM sample count")
	}

	// Valid audio append
	if err := session.AppendAudio(context.Background(), []byte{1, 2}); err != nil {
		t.Fatal(err)
	}

	if err := session.CommitAudio(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Receive delta
	ev1, err := session.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ev1.InputTranscript != "xin " {
		t.Fatalf("ev1=%+v", ev1)
	}

	// Receive completed
	ev2, err := session.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ev2.InputTranscript != "xin chào" || !ev2.InputFinal {
		t.Fatalf("ev2=%+v", ev2)
	}

	// Receive audio delta
	ev3, err := session.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ev3.AudioPCM) == 0 {
		t.Fatalf("ev3=%+v", ev3)
	}

	// Receive response done
	ev4, err := session.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ev4.ResponseDone || ev4.ResponseStatus != "completed" {
		t.Fatalf("ev4=%+v", ev4)
	}
}

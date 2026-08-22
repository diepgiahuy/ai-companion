package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestGeminiLiveSessionAudioToolAndLifecycle(t *testing.T) {
	alias := geminiLiveToolAlias("companion.echo")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "test-key" {
			t.Errorf("key=%q want test-key", got)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		setup := readGeminiTestJSON(t, ctx, conn)
		setupBody, ok := setup["setup"].(map[string]any)
		if !ok || setupBody["model"] != "models/test-live" {
			t.Errorf("unexpected setup: %#v", setup)
			return
		}
		tools, _ := setupBody["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("unexpected tools: %#v", setupBody["tools"])
			return
		}
		toolGroup, _ := tools[0].(map[string]any)
		declarations, _ := toolGroup["functionDeclarations"].([]any)
		if len(declarations) != 1 {
			t.Errorf("unexpected declarations: %#v", toolGroup)
			return
		}
		declaration, _ := declarations[0].(map[string]any)
		parameters, _ := declaration["parameters"].(map[string]any)
		if _, exists := parameters["additionalProperties"]; exists {
			t.Errorf("Gemini setup leaked unsupported additionalProperties: %#v", parameters)
			return
		}
		properties, _ := parameters["properties"].(map[string]any)
		valueSchema, _ := properties["value"].(map[string]any)
		if _, exists := valueSchema["additionalProperties"]; exists {
			t.Errorf("Gemini nested schema leaked unsupported additionalProperties: %#v", valueSchema)
			return
		}
		// The real Gemini endpoint was observed returning setup JSON in a binary
		// WebSocket message, so cover both legal JSON carriage modes.
		writeGeminiTestJSONType(t, ctx, conn, websocket.MessageBinary, map[string]any{"setupComplete": map[string]any{}})

		activityStart := readGeminiTestJSON(t, ctx, conn)
		if !hasGeminiRealtimeField(activityStart, "activityStart") {
			t.Errorf("missing activityStart: %#v", activityStart)
			return
		}
		audio := readGeminiTestJSON(t, ctx, conn)
		if !hasGeminiRealtimeField(audio, "audio") {
			t.Errorf("missing audio: %#v", audio)
			return
		}
		activityEnd := readGeminiTestJSON(t, ctx, conn)
		if !hasGeminiRealtimeField(activityEnd, "activityEnd") {
			t.Errorf("missing activityEnd: %#v", activityEnd)
			return
		}

		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{
				"interimInputTranscription": map[string]any{"text": "xin"},
			},
		})
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{
				"inputTranscription": map[string]any{"text": "xin chao"},
			},
		})
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"toolCall": map[string]any{
				"functionCalls": []map[string]any{{
					"name": alias,
					"id":   "call-1",
					"args": map[string]any{"value": "ok"},
				}},
			},
		})

		response := readGeminiTestJSON(t, ctx, conn)
		body, _ := response["toolResponse"].(map[string]any)
		items, _ := body["functionResponses"].([]any)
		if len(items) != 1 {
			t.Errorf("unexpected tool response: %#v", response)
			return
		}

		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{
				"modelTurn": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "audio/pcm;rate=24000",
							"data":     base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
						},
					}},
				},
			},
		})
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{"generationComplete": true},
		})
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{"turnComplete": true},
		})
	}))
	defer server.Close()

	provider, err := NewGeminiLive(GeminiLiveConfig{
		URL:    "ws" + server.URL[len("http"):],
		Model:  "test-live",
		APIKey: "test-key",
		Tools: []NativeRealtimeTool{{
			Name:        "companion.echo",
			Description: "echo",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"value": map[string]any{
						"type":                 "string",
						"additionalProperties": false,
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.AppendAudio(ctx, []byte{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitAudio(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.CreateResponse(ctx); err != nil {
		t.Fatal(err)
	}

	partial, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if partial.InputTranscript != "xin" || partial.InputFinal {
		t.Fatalf("partial=%+v", partial)
	}
	final, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.InputTranscript != "xin chao" || !final.InputFinal {
		t.Fatalf("final=%+v", final)
	}
	tool, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tool.ToolCall == nil || tool.ToolCall.Name != "companion.echo" || tool.ToolCall.CallID != "call-1" {
		t.Fatalf("tool=%+v", tool)
	}
	if err := session.ReturnToolResult(ctx, tool.ToolCall.CallID, `{"text":"done"}`); err != nil {
		t.Fatal(err)
	}
	audio, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := audio.AudioPCM; len(got) != 4 || got[0] != 1 || got[3] != 4 {
		t.Fatalf("audio=%+v", audio)
	}
	generated, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Type != "response.audio.done" {
		t.Fatalf("generation event=%+v", generated)
	}
	done, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !done.ResponseDone || done.ResponseStatus != "completed" {
		t.Fatalf("done=%+v", done)
	}
}

func TestGeminiLiveCancelUsesActivityInterruption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = readGeminiTestJSON(t, ctx, conn)
		writeGeminiTestJSON(t, ctx, conn, map[string]any{"setupComplete": map[string]any{}})
		_ = readGeminiTestJSON(t, ctx, conn)
		_ = readGeminiTestJSON(t, ctx, conn)
		_ = readGeminiTestJSON(t, ctx, conn)
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{
				"modelTurn": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "audio/pcm;rate=24000",
							"data":     base64.StdEncoding.EncodeToString([]byte{1, 2}),
						},
					}},
				},
			},
		})
		cancelMessage := readGeminiTestJSON(t, ctx, conn)
		if !hasGeminiRealtimeField(cancelMessage, "activityStart") {
			t.Errorf("cancel did not send activityStart: %#v", cancelMessage)
			return
		}
		writeGeminiTestJSON(t, ctx, conn, map[string]any{
			"serverContent": map[string]any{"interrupted": true},
		})
	}))
	defer server.Close()

	provider, err := NewGeminiLive(GeminiLiveConfig{URL: "ws" + server.URL[len("http"):], Model: "test-live", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.AppendAudio(ctx, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitAudio(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.CreateResponse(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.AudioPCM) == 0 {
		t.Fatalf("first=%+v", first)
	}
	if err := session.CancelResponse(ctx); err != nil {
		t.Fatal(err)
	}
	interrupted, err := session.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !interrupted.ResponseDone || interrupted.ResponseStatus != "cancelled" {
		t.Fatalf("interrupted=%+v", interrupted)
	}
}

func readGeminiTestJSON(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	kind, raw, err := conn.Read(ctx)
	if err != nil {
		t.Errorf("read: %v", err)
		return nil
	}
	if kind != websocket.MessageText {
		t.Errorf("kind=%v want text", kind)
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Errorf("decode: %v", err)
		return nil
	}
	return value
}

func writeGeminiTestJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	writeGeminiTestJSONType(t, ctx, conn, websocket.MessageText, value)
}

func writeGeminiTestJSONType(t *testing.T, ctx context.Context, conn *websocket.Conn, kind websocket.MessageType, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Errorf("marshal: %v", err)
		return
	}
	if err := conn.Write(ctx, kind, raw); err != nil {
		t.Errorf("write: %v", err)
	}
}

func hasGeminiRealtimeField(message map[string]any, field string) bool {
	realtime, ok := message["realtimeInput"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = realtime[field]
	return ok
}

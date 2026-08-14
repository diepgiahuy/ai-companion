package speech

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestHuoshanDoubleStreamSendsSessionTaskFinishAndStreamsPCM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-App-Key") != "app" || r.Header.Get("X-Api-Access-Key") != "token" || r.Header.Get("X-Api-Resource-Id") != "seed-tts-2.0" || r.Header.Get("X-Api-Connect-Id") == "" {
			t.Errorf("headers app=%q token=%q resource=%q connect=%q", r.Header.Get("X-Api-App-Key"), r.Header.Get("X-Api-Access-Key"), r.Header.Get("X-Api-Resource-Id"), r.Header.Get("X-Api-Connect-Id"))
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil { t.Errorf("accept: %v", err); return }
		defer conn.CloseNow()
		ctx := r.Context()

		start := readHuoshanClientEvent(t, ctx, conn)
		if start.event != huoshanEventStartSession || start.sessionID == "" {
			t.Errorf("start=%+v", start); return
		}
		var startJSON map[string]any
		if err := json.Unmarshal(start.payload, &startJSON); err != nil { t.Errorf("decode start: %v", err); return }
		params := startJSON["req_params"].(map[string]any)
		audio := params["audio_params"].(map[string]any)
		if params["speaker"] != "voice-test" || audio["format"] != "pcm" || audio["sample_rate"] != float64(24000) {
			t.Errorf("start payload=%#v", startJSON); return
		}

		task := readHuoshanClientEvent(t, ctx, conn)
		if task.event != huoshanEventTaskRequest || task.sessionID != start.sessionID {
			t.Errorf("task=%+v", task); return
		}
		var taskJSON map[string]any
		if err := json.Unmarshal(task.payload, &taskJSON); err != nil { t.Errorf("decode task: %v", err); return }
		if taskJSON["req_params"].(map[string]any)["text"] != "xin chào" {
			t.Errorf("task payload=%#v", taskJSON); return
		}

		finish := readHuoshanClientEvent(t, ctx, conn)
		if finish.event != huoshanEventFinishSession || finish.sessionID != start.sessionID || string(finish.payload) != `{}` {
			t.Errorf("finish=%+v", finish); return
		}

		if err := conn.Write(ctx, websocket.MessageBinary, huoshanServerEventPacket(huoshanAudioOnlyResponse, huoshanEventTTSResponse, start.sessionID, []byte{1, 2, 3, 4}, false)); err != nil {
			t.Errorf("write audio: %v"); return
		}
		if err := conn.Write(ctx, websocket.MessageBinary, huoshanServerEventPacket(huoshanFullServerResponse, huoshanEventSessionFinished, start.sessionID, []byte(`{"status":"ok"}`), true)); err != nil {
			t.Errorf("write finish: %v"); return
		}
	}))
	defer server.Close()

	provider, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), AppID: "app", AccessToken: "token", ResourceID: "seed-tts-2.0", Speaker: "voice-test",
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var frames [][]byte
	final := false
	err = provider.Synthesize(ctx, TTSRequest{Text: "xin chào", Format: AudioFormat{SampleRate: 24000, Channels: 1}}, func(event AudioEvent) error {
		if len(event.PCM) > 0 { frames = append(frames, append([]byte(nil), event.PCM...)) }
		if event.Final { final = true }
		return nil
	})
	if err != nil { t.Fatal(err) }
	if len(frames) != 1 || string(frames[0]) != string([]byte{1, 2, 3, 4}) || !final {
		t.Fatalf("frames=%v final=%v", frames, final)
	}
}

func TestHuoshanParserRejectsTruncatedPacket(t *testing.T) {
	if _, err := parseHuoshanResponse([]byte{0x11, 0xB4}); err == nil {
		t.Fatal("truncated Huoshan packet unexpectedly accepted")
	}
}

func TestHuoshanConfigFailsClosedWithoutCredentials(t *testing.T) {
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL: "wss://example.com"}); err == nil {
		t.Fatal("missing Huoshan credentials unexpectedly accepted")
	}
}

type huoshanClientEvent struct {
	event int
	sessionID string
	payload []byte
}

func readHuoshanClientEvent(t *testing.T, ctx context.Context, conn *websocket.Conn) huoshanClientEvent {
	t.Helper()
	kind, raw, err := conn.Read(ctx)
	if err != nil { t.Fatalf("read Huoshan client packet: %v", err) }
	if kind != websocket.MessageBinary { t.Fatalf("Huoshan client packet kind=%v", kind) }
	if len(raw) < 8 || raw[0] != 0x11 || int(raw[1]>>4) != huoshanFullClientRequest || int(raw[1]&0x0F) != huoshanFlagWithEvent {
		t.Fatalf("invalid Huoshan client header=%x", raw)
	}
	event, offset, err := readHuoshanInt32(raw, 4)
	if err != nil { t.Fatal(err) }
	session, next, err := readHuoshanContent(raw, offset)
	if err != nil { t.Fatal(err) }
	payload, _, err := readHuoshanContent(raw, next)
	if err != nil { t.Fatal(err) }
	return huoshanClientEvent{event: int(event), sessionID: string(session), payload: payload}
}

func huoshanServerEventPacket(messageType, event int, sessionID string, payload []byte, sessionMeta bool) []byte {
	packet := []byte{0x11, byte((messageType << 4) | huoshanFlagWithEvent), byte(huoshanJSON << 4), 0}
	packet = appendHuoshanInt32(packet, int32(event))
	packet = appendHuoshanContent(packet, []byte(sessionID))
	packet = appendHuoshanContent(packet, payload)
	_ = sessionMeta // layout is intentionally the same for session-meta events: session id + metadata payload.
	return packet
}

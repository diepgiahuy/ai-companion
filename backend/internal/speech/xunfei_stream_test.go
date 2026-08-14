package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestXunfeiSignedURLCarriesHMACAuthorization(t *testing.T) {
	fixed := time.Date(2026, 8, 14, 4, 0, 0, 0, time.UTC)
	signed, err := xunfeiSignedURL("wss://iat-api.xfyun.cn/v2/iat", "api-key", "api-secret", fixed)
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(signed)
	if err != nil { t.Fatal(err) }
	if parsed.Query().Get("host") != "iat-api.xfyun.cn" || !strings.Contains(parsed.Query().Get("date"), "GMT") {
		t.Fatalf("signed query=%s", parsed.RawQuery)
	}
	authRaw, err := base64.StdEncoding.DecodeString(parsed.Query().Get("authorization"))
	if err != nil { t.Fatal(err) }
	auth := string(authRaw)
	if !strings.Contains(auth, `api_key="api-key"`) || !strings.Contains(auth, `algorithm="hmac-sha256"`) || !strings.Contains(auth, `headers="host date request-line"`) || !strings.Contains(auth, `signature="`) {
		t.Fatalf("authorization=%q", auth)
	}
}

func TestXunfeiStreamFramesAndDynamicCorrection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("authorization") == "" || r.URL.Query().Get("date") == "" || r.URL.Query().Get("host") == "" {
			t.Errorf("missing signed query: %s", r.URL.RawQuery)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil { t.Errorf("accept: %v", err); return }
		defer conn.CloseNow()
		ctx := r.Context()

		kind, raw, err := conn.Read(ctx)
		if err != nil { t.Errorf("read first frame: %v", err); return }
		if kind != websocket.MessageText { t.Errorf("first kind=%v", kind); return }
		var first map[string]any
		if err := json.Unmarshal(raw, &first); err != nil { t.Errorf("decode first: %v", err); return }
		data := first["data"].(map[string]any)
		if data["status"] != float64(0) || data["format"] != "audio/L16;rate=16000" || data["encoding"] != "raw" {
			t.Errorf("first data=%#v", data)
			return
		}
		business := first["business"].(map[string]any)
		if business["language"] != "en_us" || business["dwa"] != "wpgs" {
			t.Errorf("business=%#v", business)
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(data["audio"].(string))
		if err != nil || len(decoded) != xunfeiPCMFrameBytes {
			t.Errorf("audio bytes=%d err=%v", len(decoded), err)
			return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"code":0,"data":{"status":1,"result":{"sn":0,"pgs":"apd","ws":[{"cw":[{"w":"hello "}]}]}}}`))

		kind, raw, err = conn.Read(ctx)
		if err != nil { t.Errorf("read last frame: %v", err); return }
		if kind != websocket.MessageText { t.Errorf("last kind=%v", kind); return }
		var last map[string]any
		if err := json.Unmarshal(raw, &last); err != nil { t.Errorf("decode last: %v", err); return }
		if last["data"].(map[string]any)["status"] != float64(2) {
			t.Errorf("last=%#v", last)
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"code":0,"data":{"status":2,"result":{"sn":1,"pgs":"apd","ws":[{"cw":[{"w":"world"}]}]}}}`))
	}))
	defer server.Close()

	provider, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), AppID: "app", APIKey: "key", APISecret: "secret",
		Language: "en_us", Accent: "mandarin", DynamicCorrection: true,
		Now: func() time.Time { return time.Date(2026, 8, 14, 4, 0, 0, 0, time.UTC) },
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var events []TranscriptEvent
	stream, err := provider.StartASR(ctx, ASRRequest{Format: AudioFormat{SampleRate: 16000, Channels: 1}, Locale: "en-US"}, func(event TranscriptEvent) error {
		events = append(events, event); return nil
	})
	if err != nil { t.Fatal(err) }
	defer stream.Close()
	if err := stream.Push(ctx, make([]byte, xunfeiPCMFrameBytes)); err != nil { t.Fatal(err) }
	if err := stream.CloseInput(ctx); err != nil { t.Fatal(err) }
	text, err := stream.Wait(ctx)
	if err != nil { t.Fatal(err) }
	if text != "hello world" { t.Fatalf("text=%q", text) }
	if len(events) != 2 || events[0].Final || !events[1].Final {
		t.Fatalf("events=%+v", events)
	}
}

func TestXunfeiConfigFailsClosedWithoutCredentials(t *testing.T) {
	if _, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{}); err == nil {
		t.Fatal("missing Xunfei credentials unexpectedly accepted")
	}
}

package speech

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEdgeSecMSGECStableWithinFiveMinuteWindow(t *testing.T) {
	first := edgeSecMSGEC(time.Date(2026, 8, 14, 4, 1, 0, 0, time.UTC), edgeTrustedClientToken)
	second := edgeSecMSGEC(time.Date(2026, 8, 14, 4, 4, 59, 0, time.UTC), edgeTrustedClientToken)
	third := edgeSecMSGEC(time.Date(2026, 8, 14, 4, 5, 0, 0, time.UTC), edgeTrustedClientToken)
	if first != second { t.Fatalf("same 5-minute window changed token: %q != %q", first, second) }
	if first == third { t.Fatal("next 5-minute window reused Sec-MS-GEC token") }
	if len(first) != 64 || first != strings.ToUpper(first) { t.Fatalf("token=%q", first) }
}

func TestEdgeTTSStreamsRaw24kPCM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("TrustedClientToken") == "" || r.URL.Query().Get("Sec-MS-GEC") == "" || r.URL.Query().Get("Sec-MS-GEC-Version") == "" || r.URL.Query().Get("ConnectionId") == "" {
			t.Errorf("missing Edge query: %s", r.URL.RawQuery)
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil { t.Errorf("accept: %v", err); return }
		defer conn.CloseNow()
		ctx := r.Context()

		kind, config, err := conn.Read(ctx)
		if err != nil { t.Errorf("read config: %v", err); return }
		if kind != websocket.MessageText || !strings.Contains(string(config), `"outputFormat":"raw-24khz-16bit-mono-pcm"`) || !strings.Contains(string(config), "Path:speech.config") {
			t.Errorf("config=%q kind=%v", config, kind)
			return
		}
		kind, ssml, err := conn.Read(ctx)
		if err != nil { t.Errorf("read ssml: %v", err); return }
		if kind != websocket.MessageText || !strings.Contains(string(ssml), "Path:ssml") || !strings.Contains(string(ssml), "vi-VN-HoaiMyNeural") || !strings.Contains(string(ssml), "xin &amp; chào") {
			t.Errorf("ssml=%q kind=%v", ssml, kind)
			return
		}
		binary := append([]byte("X-RequestId:test\r\nContent-Type:audio/pcm\r\nPath:audio\r\n"), []byte{1, 2, 3, 4}...)
		_ = conn.Write(ctx, websocket.MessageBinary, binary)
		_ = conn.Write(ctx, websocket.MessageText, []byte("Path:turn.end\r\n"))
	}))
	defer server.Close()

	provider, err := NewEdgeTTS(EdgeTTSConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Voice: "en-US-EmmaMultilingualNeural",
		Now: func() time.Time { return time.Date(2026, 8, 14, 4, 0, 0, 0, time.UTC) },
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var frames [][]byte
	final := false
	err = provider.Synthesize(ctx, TTSRequest{
		Text: "xin & chào", Voice: "vi-VN-HoaiMyNeural", Locale: "vi-VN",
		Format: AudioFormat{SampleRate: 24000, Channels: 1},
	}, func(event AudioEvent) error {
		if len(event.PCM) > 0 { frames = append(frames, append([]byte(nil), event.PCM...)) }
		if event.Final { final = true }
		return nil
	})
	if err != nil { t.Fatal(err) }
	if len(frames) != 1 || string(frames[0]) != string([]byte{1, 2, 3, 4}) || !final {
		t.Fatalf("frames=%v final=%v", frames, final)
	}
}

func TestEdgeTTSRejectsWrongPCMRate(t *testing.T) {
	provider, err := NewEdgeTTS(EdgeTTSConfig{URL: "ws://127.0.0.1:1234"})
	if err != nil { t.Fatal(err) }
	err = provider.Synthesize(context.Background(), TTSRequest{Text: "hello", Format: AudioFormat{SampleRate: 16000, Channels: 1}}, func(AudioEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "24000 Hz") { t.Fatalf("error=%v", err) }
}

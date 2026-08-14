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

func TestFunASRStreamsPartialAndCorrectedFinal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		kind, raw, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("read config: %v", err)
			return
		}
		if kind != websocket.MessageText {
			t.Errorf("config kind=%v", kind)
			return
		}
		var config map[string]any
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Errorf("decode config: %v", err)
			return
		}
		if config["mode"] != "2pass" || config["audio_fs"] != float64(16000) {
			t.Errorf("unexpected config: %#v", config)
			return
		}

		kind, audio, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("read audio: %v", err)
			return
		}
		if kind != websocket.MessageBinary || string(audio) != "pcm" {
			t.Errorf("audio=%q kind=%v", audio, kind)
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"mode":"2pass-online","text":"xin","is_final":false}`))

		kind, raw, err = conn.Read(ctx)
		if err != nil {
			t.Errorf("read close input: %v", err)
			return
		}
		if kind != websocket.MessageText || !strings.Contains(string(raw), `"is_speaking":false`) {
			t.Errorf("close input=%s kind=%v", raw, kind)
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"mode":"2pass-offline","text":"xin chào","is_final":true,"is_end":true}`))
	}))
	defer server.Close()

	provider, err := NewFunASR(FunASRConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var events []TranscriptEvent
	stream, err := provider.StartASR(ctx, ASRRequest{Format: AudioFormat{SampleRate: 16000, Channels: 1}, Locale: "vi-VN"}, func(event TranscriptEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Push(ctx, []byte("pcm")); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseInput(ctx); err != nil {
		t.Fatal(err)
	}
	text, err := stream.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if text != "xin chào" {
		t.Fatalf("final text=%q", text)
	}
	if len(events) != 2 || events[0].Final || !events[1].Final || events[1].Text != "xin chào" {
		t.Fatalf("events=%+v", events)
	}
}

func TestFunASRRejectsWrongSampleRate(t *testing.T) {
	provider, err := NewFunASR(FunASRConfig{URL: "ws://127.0.0.1:10095"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.StartASR(context.Background(), ASRRequest{Format: AudioFormat{SampleRate: 24000, Channels: 1}}, func(TranscriptEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "16000 Hz") {
		t.Fatalf("error=%v", err)
	}
}

func TestFunASRConfigRejectsNonWebSocketURL(t *testing.T) {
	if _, err := NewFunASR(FunASRConfig{URL: "http://127.0.0.1:10095"}); err == nil {
		t.Fatal("non-websocket URL unexpectedly accepted")
	}
}

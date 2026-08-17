package speech

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestXunfeiSignedURLIsDeterministicAndKeepsHost(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	raw, err := xunfeiSignedURL("wss://iat-api.xfyun.cn/v2/iat", "key", "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "iat-api.xfyun.cn" {
		t.Fatalf("url=%s", raw)
	}
	q := parsed.Query()
	for _, key := range []string{"authorization", "date", "host"} {
		if q.Get(key) == "" {
			t.Fatalf("missing %s", key)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(q.Get("authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), `api_key="key"`) || !strings.Contains(string(decoded), "hmac-sha256") {
		t.Fatalf("authorization=%q", decoded)
	}
}

func TestXunfeiConfigFailsClosed(t *testing.T) {
	if _, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{URL: "ws://example.com/v2/iat", AppID: "a", APIKey: "k", APISecret: "s"}); err == nil {
		t.Fatal("plaintext remote websocket must fail")
	}
	if _, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{URL: "wss://example.com/v2/iat"}); err == nil {
		t.Fatal("missing credentials must fail")
	}
}

func TestJoinXunfeiSegmentsOrdersCorrections(t *testing.T) {
	got := joinXunfeiSegments(map[int]string{2: "bạn", 0: "xin ", 1: "chào "})
	if got != "xin chào bạn" {
		t.Fatalf("got=%q", got)
	}
}

func TestXunfeiStreamASREndToEndWithMockWebSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		// Read first frame
		_, _, err = conn.Read(r.Context())
		if err != nil {
			return
		}

		// Send partial transcript
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"code":0,"message":"success","data":{"status":0,"result":{"sn":0,"ws":[{"cw":[{"w":"xin "}]}]}}}`))

		// Read second frame
		_, _, _ = conn.Read(r.Context())

		// Send second partial transcript
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"code":0,"message":"success","data":{"status":1,"result":{"sn":1,"ws":[{"cw":[{"w":"chào"}]}]}}}`))

		// Send final frame
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"code":0,"message":"success","data":{"status":2,"result":{"sn":2,"ws":[]}}}`))
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{
		URL:       wsURL,
		AppID:     "mock_app",
		APIKey:    "mock_key",
		APISecret: "mock_secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	var events []TranscriptEvent
	stream, err := provider.StartASR(context.Background(), ASRRequest{
		Format: AudioFormat{SampleRate: 16000, Channels: 1},
		Locale: "vi-VN",
	}, func(event TranscriptEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	// Push 1280 bytes of 16-bit PCM audio
	pcm := make([]byte, 1280)
	if err := stream.Push(context.Background(), pcm); err != nil {
		t.Fatal(err)
	}

	if err := stream.CloseInput(context.Background()); err != nil {
		t.Fatal(err)
	}

	text, err := stream.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if text != "xin chào" {
		t.Fatalf("text=%q, want 'xin chào'", text)
	}
	if len(events) == 0 || !events[len(events)-1].Final {
		t.Fatalf("expected final event, got events=%+v", events)
	}
}

func TestXunfeiStreamASRRejectsOddPCMSampleAndClosedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{
		URL:       wsURL,
		AppID:     "mock_app",
		APIKey:    "mock_key",
		APISecret: "mock_secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := provider.StartASR(context.Background(), ASRRequest{
		Format: AudioFormat{SampleRate: 16000, Channels: 1},
		Locale: "vi-VN",
	}, func(TranscriptEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	// Reject odd PCM length
	if err := stream.Push(context.Background(), []byte{1, 2, 3}); err == nil || !strings.Contains(err.Error(), "16-bit samples") {
		t.Fatalf("expected odd PCM error, got %v", err)
	}

	// Close stream and verify Push/CloseInput fail closed
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	if err := stream.Push(context.Background(), []byte{1, 2}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed error on Push, got %v", err)
	}
	if err := stream.CloseInput(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed error on CloseInput, got %v", err)
	}
}

func TestXunfeiStreamASRServerErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, _, _ = conn.Read(r.Context())
		// Send error code
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"code":10101,"message":"invalid authorization"}`))
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{
		URL:       wsURL,
		AppID:     "mock_app",
		APIKey:    "mock_key",
		APISecret: "mock_secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := provider.StartASR(context.Background(), ASRRequest{
		Format: AudioFormat{SampleRate: 16000, Channels: 1},
		Locale: "vi-VN",
	}, func(TranscriptEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	_ = stream.Push(context.Background(), make([]byte, 1280))
	_ = stream.CloseInput(context.Background())

	_, err = stream.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "10101") {
		t.Fatalf("expected 10101 error, got %v", err)
	}
}

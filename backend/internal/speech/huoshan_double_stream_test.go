package speech

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestHuoshanClientPacketRoundTripPrimitives(t *testing.T) {
	packet, err := buildHuoshanClientPacket(huoshanEventTaskRequest, "session-1", []byte(`{"text":"xin chào"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) < 12 {
		t.Fatalf("packet too short: %d", len(packet))
	}
	if packet[0]>>4 != huoshanProtocolVersion || packet[1]>>4 != huoshanFullClientRequest {
		t.Fatalf("bad header: %x", packet[:4])
	}
	if got := int32FromPacket(t, packet[4:8]); got != huoshanEventTaskRequest {
		t.Fatalf("event=%d", got)
	}
}

func TestParseHuoshanAudioEvent(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	raw := []byte{byte((huoshanProtocolVersion << 4) | huoshanHeaderSize), byte((huoshanAudioOnlyResponse << 4) | huoshanFlagWithEvent), 0, 0}
	raw = appendHuoshanInt32(raw, huoshanEventTTSResponse)
	raw = appendHuoshanContent(raw, []byte("session-1"))
	raw = appendHuoshanContent(raw, payload)
	response, err := parseHuoshanResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if response.Event != huoshanEventTTSResponse || response.SessionID != "session-1" || !bytes.Equal(response.Payload, payload) {
		t.Fatalf("response=%+v", response)
	}
}

func TestHuoshanConfigFailsClosed(t *testing.T) {
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL: "ws://example.com", AppID: "a", AccessToken: "t", ResourceID: "r", Speaker: "s"}); err == nil {
		t.Fatal("plaintext remote websocket must fail")
	}
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL: "wss://example.com"}); err == nil {
		t.Fatal("missing credentials must fail")
	}
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL: "ws://127.0.0.1:8000", AppID: "a", AccessToken: "t", ResourceID: "r", Speaker: ""}); err == nil {
		t.Fatal("missing speaker must fail")
	}
}

func TestHuoshanSynthesizeEndToEndWithMockWebSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		// Read start session packet
		_, _, err = conn.Read(r.Context())
		if err != nil {
			return
		}
		// Read task request packet
		_, _, _ = conn.Read(r.Context())
		// Read finish session packet
		_, _, _ = conn.Read(r.Context())

		// Send audio chunk
		audioRaw := []byte{byte((huoshanProtocolVersion << 4) | huoshanHeaderSize), byte((huoshanAudioOnlyResponse << 4) | huoshanFlagWithEvent), 0, 0}
		audioRaw = appendHuoshanInt32(audioRaw, huoshanEventTTSResponse)
		audioRaw = appendHuoshanContent(audioRaw, []byte("session-test"))
		audioRaw = appendHuoshanContent(audioRaw, []byte{1, 0, 2, 0})
		_ = conn.Write(r.Context(), websocket.MessageBinary, audioRaw)

		// Send session finished
		finishRaw := []byte{byte((huoshanProtocolVersion << 4) | huoshanHeaderSize), byte((huoshanFullServerResponse << 4) | huoshanFlagWithEvent), 0, 0}
		finishRaw = appendHuoshanInt32(finishRaw, huoshanEventSessionFinished)
		finishRaw = appendHuoshanContent(finishRaw, []byte("session-test"))
		finishRaw = appendHuoshanContent(finishRaw, []byte(`{}`))
		_ = conn.Write(r.Context(), websocket.MessageBinary, finishRaw)
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{
		URL:         wsURL,
		AppID:       "mock_app",
		AccessToken: "mock_token",
		ResourceID:  "mock_resource",
		Speaker:     "mock_speaker",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotPCM []byte
	gotFinal := false
	err = provider.Synthesize(context.Background(), TTSRequest{
		Text:   "xin chào",
		Voice:  "mock_speaker",
		Format: AudioFormat{SampleRate: 24000, Channels: 1},
	}, func(event AudioEvent) error {
		if len(event.PCM) > 0 {
			gotPCM = append(gotPCM, event.PCM...)
		}
		if event.Final {
			gotFinal = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPCM, []byte{1, 0, 2, 0}) || !gotFinal {
		t.Fatalf("gotPCM=%v, gotFinal=%v", gotPCM, gotFinal)
	}
}

func TestHuoshanSynthesizeBargeInCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			_, _, err := conn.Read(r.Context())
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{
		URL:         wsURL,
		AppID:       "mock_app",
		AccessToken: "mock_token",
		ResourceID:  "mock_resource",
		Speaker:     "mock_speaker",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	err = provider.Synthesize(ctx, TTSRequest{
		Text:   "xin chào Companion",
		Voice:  "mock_speaker",
		Format: AudioFormat{SampleRate: 24000, Channels: 1},
	}, func(AudioEvent) error { return nil })

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestHuoshanSynthesizeServerErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, _, _ = conn.Read(r.Context())

		// Send error information frame
		errRaw := []byte{byte((huoshanProtocolVersion << 4) | huoshanHeaderSize), byte((huoshanErrorInformation << 4)), 0, 0}
		errRaw = appendHuoshanInt32(errRaw, 4001) // error code
		errRaw = appendHuoshanContent(errRaw, []byte("quota exceeded"))
		_ = conn.Write(r.Context(), websocket.MessageBinary, errRaw)
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	provider, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{
		URL:         wsURL,
		AppID:       "mock_app",
		AccessToken: "mock_token",
		ResourceID:  "mock_resource",
		Speaker:     "mock_speaker",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = provider.Synthesize(context.Background(), TTSRequest{
		Text:   "xin chào",
		Voice:  "mock_speaker",
		Format: AudioFormat{SampleRate: 24000, Channels: 1},
	}, func(AudioEvent) error { return nil })

	if err == nil || !strings.Contains(err.Error(), "4001") {
		t.Fatalf("expected error code 4001, got %v", err)
	}
}

func int32FromPacket(t *testing.T, raw []byte) int32 {
	t.Helper()
	value, _, err := readHuoshanInt32(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

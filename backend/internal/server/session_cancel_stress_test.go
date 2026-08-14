package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"

	"github.com/coder/websocket"
	opus "gopkg.in/hraban/opus.v2"
)

type cancelStressASR struct {
	started  chan string
	canceled chan string
}

func (a *cancelStressASR) Transcribe(ctx context.Context, _ []byte) (string, error) {
	id := fmt.Sprintf("call-%d", time.Now().UnixNano())
	select {
	case a.started <- id:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	<-ctx.Done()
	select {
	case a.canceled <- id:
	default:
	}
	return "", ctx.Err()
}

func TestRepeatedReconnectAbortCancelsActiveASRAndUnregistersSession(t *testing.T) {
	const iterations = 25
	asr := &cancelStressASR{
		started:  make(chan string, iterations),
		canceled: make(chan string, iterations),
	}
	service := newAuthenticatedTestServer(pipeline.Components{
		ASR: asr, Agent: pipeline.MockAgent{Reply: "unused"}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{},
	})
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	encoder, err := opus.NewEncoder(protocol.UplinkSampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, protocol.MaximumOpusPacketBytes)
	n, err := encoder.Encode(make([]int16, protocol.UplinkSamplesPerFrame), packet)
	if err != nil {
		t.Fatal(err)
	}
	packet = packet[:n]

	for i := 0; i < iterations; i++ {
		deviceID := fmt.Sprintf("cancel-stress-device-%02d", i)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions(deviceID))
		if err != nil {
			cancel()
			t.Fatalf("iteration %d dial: %v", i, err)
		}

		audio := protocol.DefaultAudioParams()
		writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
		hello := readJSON(t, ctx, connection)
		if hello.Type != protocol.SessionReadyType || hello.SessionID == "" {
			connection.CloseNow()
			cancel()
			t.Fatalf("iteration %d invalid hello: %+v", i, hello)
		}
		turnID := fmt.Sprintf("turn-%02d", i)
		writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID})
		if err := connection.Write(ctx, websocket.MessageBinary, packet); err != nil {
			connection.CloseNow()
			cancel()
			t.Fatalf("iteration %d audio: %v", i, err)
		}
		writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: turnID})

		var callID string
		select {
		case callID = <-asr.started:
		case <-ctx.Done():
			connection.CloseNow()
			cancel()
			t.Fatalf("iteration %d ASR never started: %v", i, ctx.Err())
		}

		writeJSON(t, ctx, connection, testEnvelope{Type: protocol.TurnAbortType, SessionID: hello.SessionID, TurnID: turnID, Reason: "stress_abort"})
		select {
		case canceledID := <-asr.canceled:
			if canceledID != callID {
				connection.CloseNow()
				cancel()
				t.Fatalf("iteration %d canceled call=%q want %q", i, canceledID, callID)
			}
		case <-ctx.Done():
			connection.CloseNow()
			cancel()
			t.Fatalf("iteration %d ASR did not observe cancellation: %v", i, ctx.Err())
		}

		_ = connection.Close(websocket.StatusNormalClosure, "iteration done")
		cancel()
		deadline := time.Now().Add(time.Second)
		for service.hub.get(deviceID) != nil && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if service.hub.get(deviceID) != nil {
			t.Fatalf("iteration %d session remained registered after disconnect", i)
		}
	}
}

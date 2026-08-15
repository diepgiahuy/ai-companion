package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"companion-server/internal/idempotency"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	"companion-server/internal/voicemail"

	"github.com/coder/websocket"
)

func TestVoiceMailRateLimitIsBoundedPerActorWindow(t *testing.T) {
	service := New(pipeline.Components{}, nil)
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		if !service.allowVoiceMailRequest("actor-a", now) {
			t.Fatalf("request %d rejected", i+1)
		}
	}
	if service.allowVoiceMailRequest("actor-a", now) {
		t.Fatal("request above limit accepted")
	}
	if !service.allowVoiceMailRequest("actor-b", now) {
		t.Fatal("different actor shared limit")
	}
	if !service.allowVoiceMailRequest("actor-a", now.Add(time.Minute)) {
		t.Fatal("new window remained blocked")
	}
}

type voiceMailRepositoryStub struct{ claimed, played bool }

func (r *voiceMailRepositoryStub) CreateUpload(context.Context, idempotency.Request, voicemail.Create, time.Time) (voicemail.Item, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) ItemForSender(context.Context, string, string) (voicemail.Item, bool, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) CompleteUpload(context.Context, idempotency.Request, string, string, time.Time) (voicemail.Item, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) ListUnread(context.Context, string, string, time.Time, int) ([]voicemail.Item, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) ClaimVoiceMail(_ context.Context, request idempotency.Request, userID, deviceID, id, playbackID string, _, lease time.Time) (voicemail.Item, error) {
	if request.Operation != "voice_mail.claim" || userID != "default" || deviceID != "device-vm" {
		return voicemail.Item{}, context.Canceled
	}
	r.claimed = true
	return voicemail.Item{ID: id, PlaybackID: playbackID, LeaseExpiresAt: &lease, State: voicemail.Claimed}, nil
}
func (r *voiceMailRepositoryStub) CompleteVoiceMailPlayback(_ context.Context, request idempotency.Request, userID, id, playbackID string, succeeded bool, _ time.Time) (voicemail.Item, error) {
	if request.Operation != "voice_mail.playback" || userID != "default" || !succeeded {
		return voicemail.Item{}, context.Canceled
	}
	r.played = true
	return voicemail.Item{ID: id, PlaybackID: playbackID, State: voicemail.Deleted}, nil
}
func (r *voiceMailRepositoryStub) ItemForPlayback(context.Context, string, string, string, time.Time) (voicemail.Item, bool, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) RequestDelete(context.Context, idempotency.Request, string, string, time.Time) (voicemail.Item, error) {
	panic("unexpected")
}
func (r *voiceMailRepositoryStub) MarkDeleted(context.Context, string, time.Time) error { return nil }
func (r *voiceMailRepositoryStub) ClaimCleanup(context.Context, time.Time, int) ([]voicemail.Item, error) {
	return nil, nil
}

func TestVoiceMailProtocolV2ClaimAndPlaybackDispatch(t *testing.T) {
	repo := &voiceMailRepositoryStub{}
	blobs, err := voicemail.NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mail, err := voicemail.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	service := newAuthenticatedTestServer(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, WithVoiceMail(mail))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("device-vm"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")
	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	ready := readJSON(t, ctx, connection)
	if ready.Type != protocol.SessionReadyType || ready.SessionID == "" {
		t.Fatalf("ready=%+v", ready)
	}

	claimEnvelope := protocol.Envelope{Version: protocol.Version, Type: protocol.VoiceMailClaimType, MessageID: "vm-claim-message", SessionID: ready.SessionID, IdempotencyKey: "vm-claim-key", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano)}
	claim, err := protocol.EncodeInteraction(claimEnvelope, protocol.VoiceMailClaim{VoiceMailID: "mail-1", PlaybackID: "play-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, claim); err != nil {
		t.Fatal(err)
	}
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	response, payload, err := protocol.DecodeInteraction(raw)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok := payload.(*protocol.VoiceMailClaimed)
	if !ok || response.Type != protocol.VoiceMailClaimedType || claimed.PlaybackID != "play-1" {
		t.Fatalf("response=%+v payload=%+v", response, payload)
	}

	playEnvelope := protocol.Envelope{Version: protocol.Version, Type: protocol.VoiceMailPlaybackResultType, MessageID: "vm-play-message", SessionID: ready.SessionID, IdempotencyKey: "vm-play-key", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano)}
	play, err := protocol.EncodeInteraction(playEnvelope, protocol.VoiceMailPlaybackResult{VoiceMailID: "mail-1", PlaybackID: "play-1", Result: protocol.PlaybackSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, play); err != nil {
		t.Fatal(err)
	}
	_, raw, err = connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	response, payload, err = protocol.DecodeInteraction(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := payload.(*protocol.VoiceMailConsumed); !ok || response.Type != protocol.VoiceMailConsumedType || !repo.claimed || !repo.played {
		t.Fatalf("response=%+v payload=%+v claimed=%v played=%v", response, payload, repo.claimed, repo.played)
	}
}

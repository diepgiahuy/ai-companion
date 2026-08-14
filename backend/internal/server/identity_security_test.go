package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"

	"github.com/coder/websocket"
)

type enrolledIdentityAuthenticator struct{}

func (enrolledIdentityAuthenticator) AuthenticateDevice(_ context.Context, deviceID, credential string) (domain.Identity, bool, error) {
	if deviceID != "device-trusted" || credential != "trusted-credential" {
		return domain.Identity{DeviceID: deviceID}, false, nil
	}
	return domain.Identity{UserID: "owner-user", DeviceID: deviceID, TenantID: "owner-tenant", Plan: "owner-plan"}, true, nil
}

func TestEnrolledIdentityOverridesForgedOwnershipHeaders(t *testing.T) {
	service := New(pipeline.Components{
		ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{},
	}, nil, WithDeviceAuthenticator(enrolledIdentityAuthenticator{}), WithIdentityResolver(HeaderIdentityResolver{DefaultUserID: "fallback"}))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	headers := http.Header{}
	headers.Set("Device-Id", "device-trusted")
	headers.Set("Authorization", "Bearer trusted-credential")
	headers.Set("User-Id", "attacker-user")
	headers.Set("Tenant-Id", "attacker-tenant")
	headers.Set("Plan", "attacker-plan")
	headers.Set("Thread-Id", "user-selected-thread")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, connection)
	if hello.Type != protocol.SessionReadyType {
		t.Fatalf("hello=%+v", hello)
	}

	identities := service.hub.identities()
	if len(identities) != 1 {
		t.Fatalf("identities=%+v", identities)
	}
	got := identities[0]
	if got.UserID != "owner-user" || got.DeviceID != "device-trusted" || got.TenantID != "owner-tenant" || got.Plan != "owner-plan" {
		t.Fatalf("forged ownership headers escaped enrolled identity: %+v", got)
	}
	registered := service.hub.get("device-trusted")
	if registered == nil || registered.threadID != "user-selected-thread" {
		if registered == nil {
			t.Fatal("registered session missing")
		}
		t.Fatalf("thread selection should remain a conversation concern, got=%q", registered.threadID)
	}
}

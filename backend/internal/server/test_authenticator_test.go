package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"companion-server/internal/domain"
	"companion-server/internal/pipeline"

	"github.com/coder/websocket"
)

type testTokenAuthenticator struct{ token string }

func (a testTokenAuthenticator) AuthenticateDevice(_ context.Context, deviceID, rawToken string) (domain.Identity, bool, error) {
	expected := strings.TrimSpace(a.token)
	if expected == "" {
		expected = "test-device-credential"
	}
	ok := strings.TrimSpace(deviceID) != "" && rawToken == expected
	return domain.Identity{UserID: "default", DeviceID: strings.TrimSpace(deviceID), TenantID: "test", Plan: "test"}, ok, nil
}

// newTestServer intentionally lives in _test.go. Production Server.New has no
// shared-token compatibility parameter; tests that do not need SQLite-backed
// enrollment use this explicit authenticator double instead.
func newTestServer(components pipeline.Components, token string, logger *slog.Logger, options ...Option) *Server {
	options = append(options, WithDeviceAuthenticator(testTokenAuthenticator{token: token}))
	return New(components, logger, options...)
}

func testWebsocketDial(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if options == nil {
		options = &websocket.DialOptions{}
	} else {
		copy := *options
		options = &copy
	}
	if options.HTTPHeader == nil {
		options.HTTPHeader = make(http.Header)
	} else {
		options.HTTPHeader = options.HTTPHeader.Clone()
	}
	if strings.TrimSpace(options.HTTPHeader.Get("Device-Id")) == "" {
		options.HTTPHeader.Set("Device-Id", "test-device")
	}
	if strings.TrimSpace(options.HTTPHeader.Get("Authorization")) == "" {
		options.HTTPHeader.Set("Authorization", "Bearer test-device-credential")
	}
	return websocket.Dial(ctx, url, options)
}

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
)

type rejectingDeviceAuthenticator struct{}

func (rejectingDeviceAuthenticator) AuthenticateDevice(_ context.Context, deviceID, _ string) (domain.Identity, bool, error) {
	return domain.Identity{DeviceID: deviceID}, false, nil
}

func TestDeviceEndpointFailsClosedWithoutAuthenticator(t *testing.T) {
	service := New(pipeline.Components{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/v2/device", nil)
	req.Header.Set("Device-Id", "unenrolled-device")
	req.Header.Set("Authorization", "Bearer arbitrary-credential")
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestDeviceEndpointRejectsInvalidCredentialBeforeUpgrade(t *testing.T) {
	service := New(
		pipeline.Components{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithDeviceAuthenticator(rejectingDeviceAuthenticator{}),
	)
	req := httptest.NewRequest(http.MethodGet, "/v2/device", nil)
	req.Header.Set("Device-Id", "enrolled-device")
	req.Header.Set("Authorization", "Bearer wrong-credential")
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

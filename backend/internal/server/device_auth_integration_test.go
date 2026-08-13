package server

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
	"companion-server/internal/store"
)

func TestEnrolledDeviceCredentialAuthenticatesAndRevokes(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "device-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	identity := domain.Identity{UserID: "owner-1", DeviceID: "device-1", TenantID: "tenant-1", Plan: "pro"}
	const credential = "device-credential-0123456789"
	if err := data.EnrollDevice(context.Background(), identity, credential); err != nil {
		t.Fatal(err)
	}

	service := New(pipeline.Components{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithDeviceAuthenticator(data))
	req := httptest.NewRequest("GET", "/v2/device", nil)
	req.Header.Set("Device-Id", identity.DeviceID)
	req.Header.Set("Authorization", "Bearer "+credential)
	got, ok := service.authenticateDeviceRequest(context.Background(), req)
	if !ok {
		t.Fatal("enrolled credential was rejected")
	}
	if got != identity {
		t.Fatalf("identity=%+v want=%+v", got, identity)
	}

	bad := httptest.NewRequest("GET", "/v2/device", nil)
	bad.Header.Set("Device-Id", identity.DeviceID)
	bad.Header.Set("Authorization", "Bearer wrong-device-credential")
	if _, ok := service.authenticateDeviceRequest(context.Background(), bad); ok {
		t.Fatal("wrong credential authenticated")
	}

	if err := data.RevokeDevice(context.Background(), identity.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.authenticateDeviceRequest(context.Background(), req); ok {
		t.Fatal("revoked credential authenticated")
	}
}

func TestDeviceAuthFailsClosedWithoutAuthenticator(t *testing.T) {
	service := New(pipeline.Components{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest("GET", "/v2/device", nil)
	req.Header.Set("Device-Id", "device-1")
	req.Header.Set("Authorization", "Bearer any-credential")
	if _, ok := service.authenticateDeviceRequest(context.Background(), req); ok {
		t.Fatal("server without enrolled-device authenticator must fail closed")
	}
}

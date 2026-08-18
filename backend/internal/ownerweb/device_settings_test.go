package ownerweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/store"
)

type ownerSettingsRepo struct {
	twin controlplane.Twin
	meta controlplane.SettingsMetadata
}

func (r *ownerSettingsRepo) GetTwin(_ context.Context, userID, deviceID string) (controlplane.Twin, error) {
	if r.twin.UserID != userID || r.twin.DeviceID != deviceID {
		return controlplane.Twin{}, fmt.Errorf("device not found")
	}
	return r.twin, nil
}

func (r *ownerSettingsRepo) SetDesired(_ context.Context, userID, deviceID string, config controlplane.RuntimeConfig) (controlplane.Twin, error) {
	if r.twin.UserID != userID || r.twin.DeviceID != deviceID {
		return controlplane.Twin{}, fmt.Errorf("device not found")
	}
	r.twin.Desired = config
	r.twin.DesiredVersion++
	r.twin.UpdatedAt = time.Now().UTC()
	r.meta.DesiredVersion = r.twin.DesiredVersion
	r.meta.DesiredAt = r.twin.UpdatedAt
	return r.twin, nil
}

func (r *ownerSettingsRepo) Report(_ context.Context, userID, deviceID string, version int64, config controlplane.RuntimeConfig) error {
	if r.twin.UserID != userID || r.twin.DeviceID != deviceID {
		return fmt.Errorf("device not found")
	}
	r.twin.Reported = config
	r.twin.ReportedVersion = version
	r.meta.ReportedVersion = version
	r.meta.LastReportVersion = version
	r.meta.LastReportState = controlplane.SettingsApplied
	now := time.Now().UTC()
	r.meta.ReportedAt = &now
	return nil
}

func (r *ownerSettingsRepo) RecordConfigReport(ctx context.Context, userID, deviceID string, result controlplane.ConfigReportResult) error {
	if result.Applied {
		return r.Report(ctx, userID, deviceID, result.Version, result.Config)
	}
	if r.twin.UserID != userID || r.twin.DeviceID != deviceID {
		return fmt.Errorf("device not found")
	}
	r.meta.LastReportVersion = result.Version
	r.meta.LastReportState = controlplane.SettingsRejected
	r.meta.FailureCode = result.FailureCode
	reportedAt := result.ReportedAt.UTC()
	if result.ReportedAt.IsZero() {
		reportedAt = time.Now().UTC()
	}
	r.meta.ReportedAt = &reportedAt
	return nil
}

func (r *ownerSettingsRepo) SettingsMetadata(_ context.Context, userID, deviceID string) (controlplane.SettingsMetadata, error) {
	if r.twin.UserID != userID || r.twin.DeviceID != deviceID {
		return controlplane.SettingsMetadata{}, fmt.Errorf("device not found")
	}
	return r.meta, nil
}

var _ controlplane.Repository = (*ownerSettingsRepo)(nil)
var _ controlplane.SettingsReportRepository = (*ownerSettingsRepo)(nil)

func openOwnerSettingsStore(t *testing.T) *store.Store {
	t.Helper()
	data, err := store.Open(filepath.Join(t.TempDir(), "owner_settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}

func enrollOwnerDevice(t *testing.T, data *store.Store, userID, deviceID string) {
	t.Helper()
	if err := data.EnrollDevice(context.Background(), domain.Identity{UserID: userID, DeviceID: deviceID}, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
}

func addOwnerSession(req *http.Request, session string) {
	req.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: session})
}

func TestOwnerDeviceStatusUsesAuthoritativeSettingsTruth(t *testing.T) {
	data := openOwnerSettingsStore(t)
	enrollOwnerDevice(t, data, "alice", "device-a")
	enrollOwnerDevice(t, data, "bob", "device-b")

	now := time.Now().UTC()
	interval := 21_600
	repo := &ownerSettingsRepo{
		twin: controlplane.Twin{
			DeviceID: "device-a", UserID: "alice",
			Desired: controlplane.RuntimeConfig{OTAPollIntervalSeconds: &interval},
			DesiredVersion: 2, ReportedVersion: 1, UpdatedAt: now,
		},
		meta: controlplane.SettingsMetadata{
			DesiredVersion: 2, ReportedVersion: 1, LastReportVersion: 1,
			LastReportState: controlplane.SettingsApplied, DesiredAt: now,
		},
	}
	control := controlplane.New(repo, controlplane.RuntimeConfig{})
	authService, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{
		Store: data, Auth: authService, ControlPlane: control,
		DeviceOnline: func(deviceID string) bool { return deviceID == "device-a" },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device?device_id=device-a", nil)
	addOwnerSession(req, session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		DeviceID         string                      `json:"device_id"`
		ConnectionStatus string                      `json:"connection_status"`
		SettingsStatus   controlplane.SettingsStatus `json:"settings_status"`
		DesiredVersion   int64                       `json:"desired_version"`
		ReportedVersion  int64                       `json:"reported_version"`
		OTAPollInterval  string                      `json:"ota_poll_interval"`
		FirmwareVersion  string                      `json:"firmware_version"`
		WiFiRSSIDBm      *int                        `json:"wifi_rssi_dbm"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "device-a" || got.ConnectionStatus != "online" {
		t.Fatalf("unexpected device projection: %+v", got)
	}
	if got.SettingsStatus.State != controlplane.SettingsRequested || !got.SettingsStatus.Online {
		t.Fatalf("settings status=%+v want requested+online", got.SettingsStatus)
	}
	if got.DesiredVersion != 2 || got.ReportedVersion != 1 || got.OTAPollInterval != "6h0m0s" {
		t.Fatalf("unexpected settings versions/projection: %+v", got)
	}
	if got.FirmwareVersion != "unknown" || got.WiFiRSSIDBm != nil {
		t.Fatalf("Owner Hub fabricated telemetry: %+v", got)
	}

	wrongOwner := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device?device_id=device-b", nil)
	addOwnerSession(wrongOwner, session)
	wrongOwnerW := httptest.NewRecorder()
	handler.ServeHTTP(wrongOwnerW, wrongOwner)
	if wrongOwnerW.Code != http.StatusNotFound {
		t.Fatalf("cross-owner device read status=%d want=404", wrongOwnerW.Code)
	}
}

func TestOwnerDeviceConfigUsesCanonicalUpdaterWithCSRFAndOwnership(t *testing.T) {
	data := openOwnerSettingsStore(t)
	enrollOwnerDevice(t, data, "alice", "device-a")
	enrollOwnerDevice(t, data, "bob", "device-b")
	authService, session, csrf, cleanup := newTestAuthService(t, "alice")
	defer cleanup()

	calls := 0
	var gotUser, gotDevice string
	var gotPatch controlplane.RuntimeConfig
	updater := func(_ context.Context, userID, deviceID string, patch controlplane.RuntimeConfig) (controlplane.Twin, controlplane.SettingsStatus, error) {
		calls++
		gotUser, gotDevice, gotPatch = userID, deviceID, patch
		return controlplane.Twin{DeviceID: deviceID, UserID: userID, Desired: patch, DesiredVersion: 1},
			controlplane.SettingsStatus{State: controlplane.SettingsRequested, Online: true, DesiredVersion: 1}, nil
	}
	handler := NewHandler(Dependencies{Store: data, Auth: authService, UpdateDeviceSettings: updater})

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-a","ota_poll_interval":"6h"}`))
	addOwnerSession(withoutCSRF, session)
	withoutCSRFW := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFW, withoutCSRF)
	if withoutCSRFW.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("missing-CSRF mutation status=%d calls=%d", withoutCSRFW.Code, calls)
	}

	valid := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-a","ota_poll_interval":"6h"}`))
	addOwnerSession(valid, session)
	valid.Header.Set("X-CSRF-Token", csrf)
	validW := httptest.NewRecorder()
	handler.ServeHTTP(validW, valid)
	if validW.Code != http.StatusOK {
		t.Fatalf("valid settings mutation status=%d body=%s", validW.Code, validW.Body.String())
	}
	if calls != 1 || gotUser != "alice" || gotDevice != "device-a" || gotPatch.OTAPollIntervalSeconds == nil || *gotPatch.OTAPollIntervalSeconds != 21_600 {
		t.Fatalf("canonical updater call mismatch calls=%d user=%q device=%q patch=%+v", calls, gotUser, gotDevice, gotPatch)
	}

	crossOwner := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-b","ota_poll_interval":"6h"}`))
	addOwnerSession(crossOwner, session)
	crossOwner.Header.Set("X-CSRF-Token", csrf)
	crossOwnerW := httptest.NewRecorder()
	handler.ServeHTTP(crossOwnerW, crossOwner)
	if crossOwnerW.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("cross-owner mutation status=%d calls=%d want 404/no call", crossOwnerW.Code, calls)
	}

	unknownField := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-a","ota_poll_interval":"6h","locale":"vi-VN"}`))
	addOwnerSession(unknownField, session)
	unknownField.Header.Set("X-CSRF-Token", csrf)
	unknownFieldW := httptest.NewRecorder()
	handler.ServeHTTP(unknownFieldW, unknownField)
	if unknownFieldW.Code != http.StatusBadRequest || calls != 1 {
		t.Fatalf("unknown settings field status=%d calls=%d want 400/no call", unknownFieldW.Code, calls)
	}
}

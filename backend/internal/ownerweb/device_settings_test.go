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

func TestOwnerDeviceStatusUsesSafeAuthoritativeProjection(t *testing.T) {
	data := openOwnerSettingsStore(t)
	enrollOwnerDevice(t, data, "alice", "device-a")
	enrollOwnerDevice(t, data, "bob", "device-b")

	now := time.Now().UTC()
	interval := 21_600
	vad := 450
	silence := 800
	minSpeech := 250
	idle := 5_000
	alarm := 10_000
	smart := true
	wakeThreshold := 0.65
	reportedThreshold := 0.60
	repo := &ownerSettingsRepo{
		twin: controlplane.Twin{
			DeviceID: "device-a",
			UserID:   "alice",
			Desired: controlplane.RuntimeConfig{
				SmartVADEnabled:        &smart,
				VADThreshold:           &vad,
				VADSilenceMS:           &silence,
				VADMinSpeechMS:         &minSpeech,
				IdleAfterMS:            &idle,
				AlarmVisibleMS:         &alarm,
				OTAPollIntervalSeconds: &interval,
				WakeModel:              "wn9_hiesp",
				WakeThreshold:          &wakeThreshold,
				Locale:                 "vi-VN",
				Timezone:               "Asia/Ho_Chi_Minh",
				VoiceKey:               "private-backend-field",
			},
			Reported: controlplane.RuntimeConfig{
				SmartVADEnabled:        &smart,
				VADThreshold:           &vad,
				VADSilenceMS:           &silence,
				VADMinSpeechMS:         &minSpeech,
				OTAPollIntervalSeconds: &interval,
				WakeModel:              "wn9_hiesp",
				WakeThreshold:          &reportedThreshold,
			},
			DesiredVersion:  2,
			ReportedVersion: 1,
			UpdatedAt:       now,
		},
		meta: controlplane.SettingsMetadata{
			DesiredVersion:    2,
			ReportedVersion:   1,
			LastReportVersion: 1,
			LastReportState:   controlplane.SettingsApplied,
			DesiredAt:         now,
		},
	}
	control := controlplane.New(repo, controlplane.RuntimeConfig{})
	authService, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{
		Store:        data,
		Auth:         authService,
		ControlPlane: control,
		DeviceOnline: func(deviceID string) bool { return deviceID == "device-a" },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device?device_id=device-a", nil)
	addOwnerSession(req, session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got ownerDeviceProjection
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.HasDevice || got.DeviceID != "device-a" || got.ConnectionStatus != "online" {
		t.Fatalf("unexpected projection: %+v", got)
	}
	if got.SettingsStatus.State != controlplane.SettingsRequested || !got.SettingsStatus.Online {
		t.Fatalf("settings status=%+v", got.SettingsStatus)
	}
	if got.DesiredVersion != 2 || got.ReportedVersion != 1 {
		t.Fatalf("unexpected versions: %+v", got)
	}
	if got.Desired.OTAPollIntervalSeconds == nil || *got.Desired.OTAPollIntervalSeconds != interval {
		t.Fatalf("unexpected OTA projection: %+v", got.Desired)
	}
	if got.Desired.SmartVADEnabled == nil || !*got.Desired.SmartVADEnabled {
		t.Fatalf("unexpected Smart VAD projection: %+v", got.Desired)
	}
	if got.Desired.WakeModel != "wn9_hiesp" || got.Desired.WakeThreshold == nil || *got.Desired.WakeThreshold != wakeThreshold {
		t.Fatalf("unexpected desired wake projection: %+v", got.Desired)
	}
	if got.Reported.WakeThreshold == nil || *got.Reported.WakeThreshold != reportedThreshold {
		t.Fatalf("unexpected reported projection: %+v", got.Reported)
	}
	if got.FirmwareVersion != nil || got.WiFiRSSIDBm != nil {
		t.Fatalf("fabricated telemetry: %+v", got)
	}
	for _, forbidden := range []string{`"user_id"`, `"locale"`, `"timezone"`, `"voice_key"`, "private-backend-field"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, w.Body.String())
		}
	}

	wrongOwner := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device?device_id=device-b", nil)
	addOwnerSession(wrongOwner, session)
	wrongOwnerW := httptest.NewRecorder()
	handler.ServeHTTP(wrongOwnerW, wrongOwner)
	if wrongOwnerW.Code != http.StatusNotFound {
		t.Fatalf("cross-owner status=%d", wrongOwnerW.Code)
	}
}

func TestOwnerDeviceStatusWithoutOwnedDeviceIsExplicit(t *testing.T) {
	data := openOwnerSettingsStore(t)
	control := controlplane.New(&ownerSettingsRepo{}, controlplane.RuntimeConfig{})
	authService, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: authService, ControlPlane: control})

	req := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device", nil)
	addOwnerSession(req, session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got ownerDeviceProjection
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.HasDevice || got.DeviceID != "" || got.ConnectionStatus != "offline" || got.SettingsStatus.State != controlplane.SettingsUnknown {
		t.Fatalf("unexpected no-device projection: %+v", got)
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
		gotUser = userID
		gotDevice = deviceID
		gotPatch = patch
		desired := patch
		desired.Locale = "must-not-leak"
		return controlplane.Twin{
			DeviceID:       deviceID,
			UserID:         userID,
			Desired:        desired,
			DesiredVersion: int64(calls),
			UpdatedAt:      time.Now().UTC(),
		}, controlplane.SettingsStatus{
			State:          controlplane.SettingsRequested,
			Online:         true,
			DesiredVersion: int64(calls),
		}, nil
	}
	handler := NewHandler(Dependencies{Store: data, Auth: authService, UpdateDeviceSettings: updater})

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-a","ota_poll_interval":"6h"}`))
	addOwnerSession(withoutCSRF, session)
	withoutCSRFW := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFW, withoutCSRF)
	if withoutCSRFW.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("missing CSRF status=%d calls=%d", withoutCSRFW.Code, calls)
	}

	valid := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-a","ota_poll_interval":"6h","smart_vad_enabled":false,"vad_threshold":500,"vad_silence_ms":900,"vad_min_speech_ms":300,"wake_model":"disabled","wake_threshold":0.75}`))
	addOwnerSession(valid, session)
	valid.Header.Set("X-CSRF-Token", csrf)
	validW := httptest.NewRecorder()
	handler.ServeHTTP(validW, valid)
	if validW.Code != http.StatusOK {
		t.Fatalf("valid status=%d body=%s", validW.Code, validW.Body.String())
	}
	if calls != 1 || gotUser != "alice" || gotDevice != "device-a" {
		t.Fatalf("updater identity mismatch calls=%d user=%q device=%q", calls, gotUser, gotDevice)
	}
	if gotPatch.OTAPollIntervalSeconds == nil || *gotPatch.OTAPollIntervalSeconds != 21_600 ||
		gotPatch.SmartVADEnabled == nil || *gotPatch.SmartVADEnabled ||
		gotPatch.VADThreshold == nil || *gotPatch.VADThreshold != 500 ||
		gotPatch.VADSilenceMS == nil || *gotPatch.VADSilenceMS != 900 ||
		gotPatch.VADMinSpeechMS == nil || *gotPatch.VADMinSpeechMS != 300 ||
		gotPatch.WakeModel != "disabled" ||
		gotPatch.WakeThreshold == nil || *gotPatch.WakeThreshold != 0.75 {
		t.Fatalf("updater patch mismatch: %+v", gotPatch)
	}
	if strings.Contains(validW.Body.String(), "must-not-leak") || strings.Contains(validW.Body.String(), `"user_id"`) {
		t.Fatalf("mutation response leaked internal fields: %s", validW.Body.String())
	}

	crossOwner := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(`{"device_id":"device-b","ota_poll_interval":"6h"}`))
	addOwnerSession(crossOwner, session)
	crossOwner.Header.Set("X-CSRF-Token", csrf)
	crossOwnerW := httptest.NewRecorder()
	handler.ServeHTTP(crossOwnerW, crossOwner)
	if crossOwnerW.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("cross-owner status=%d calls=%d", crossOwnerW.Code, calls)
	}

	invalidRequests := map[string]string{
		"unknown field":      `{"device_id":"device-a","locale":"vi-VN"}`,
		"unsupported model":  `{"device_id":"device-a","wake_model":"custom_phrase"}`,
		"bad wake threshold": `{"device_id":"device-a","wake_threshold":0.20}`,
		"bad vad silence":    `{"device_id":"device-a","vad_silence_ms":20}`,
	}
	for name, payload := range invalidRequests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/config", strings.NewReader(payload))
			addOwnerSession(req, session)
			req.Header.Set("X-CSRF-Token", csrf)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/devicecap"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
)

type fakeTwinRepo struct {
	mu        sync.Mutex
	twins     map[string]controlplane.Twin
	metadata  map[string]controlplane.SettingsMetadata
	version   int64
	overrides map[string]controlplane.RuntimeConfig
}

func newFakeTwinRepo() *fakeTwinRepo {
	return &fakeTwinRepo{
		twins:     make(map[string]controlplane.Twin),
		metadata:  make(map[string]controlplane.SettingsMetadata),
		overrides: make(map[string]controlplane.RuntimeConfig),
	}
}

func (r *fakeTwinRepo) GetTwin(ctx context.Context, userID, deviceID string) (controlplane.Twin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	twin, ok := r.twins[deviceID]
	if !ok {
		twin = controlplane.Twin{DeviceID: deviceID, UserID: userID}
		r.twins[deviceID] = twin
	}
	if twin.UserID != userID {
		return controlplane.Twin{}, fmt.Errorf("device owner mismatch")
	}
	twin.Status = controlplane.DeriveTwinStatus(twin, false, false)
	return twin, nil
}

func (r *fakeTwinRepo) SetDesired(ctx context.Context, userID, deviceID string, config controlplane.RuntimeConfig) (controlplane.Twin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	twin, ok := r.twins[deviceID]
	if !ok {
		twin = controlplane.Twin{DeviceID: deviceID, UserID: userID}
	}
	if twin.UserID != userID {
		return controlplane.Twin{}, fmt.Errorf("device owner mismatch")
	}
	r.version++
	twin.Desired = config
	twin.DesiredVersion = r.version
	now := time.Now().UTC()
	twin.UpdatedAt = now
	r.twins[deviceID] = twin
	meta := r.metadata[deviceID]
	meta.DesiredVersion = twin.DesiredVersion
	meta.ReportedVersion = twin.ReportedVersion
	meta.DesiredAt = now
	r.metadata[deviceID] = meta
	return twin, nil
}

func (r *fakeTwinRepo) Report(ctx context.Context, userID, deviceID string, version int64, config controlplane.RuntimeConfig) error {
	return r.RecordConfigReport(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: version, Applied: true, Config: config, ReportedAt: time.Now().UTC(),
	})
}

func (r *fakeTwinRepo) RecordConfigReport(_ context.Context, userID, deviceID string, result controlplane.ConfigReportResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	twin, ok := r.twins[deviceID]
	if !ok {
		twin = controlplane.Twin{DeviceID: deviceID, UserID: userID}
	}
	if twin.UserID != userID {
		return fmt.Errorf("device owner mismatch")
	}
	if result.Version > twin.DesiredVersion {
		return fmt.Errorf("reported config version ahead of desired")
	}
	meta := r.metadata[deviceID]
	if result.Version < meta.LastReportVersion {
		return nil
	}
	if !result.Applied && result.Version <= twin.ReportedVersion {
		return nil
	}
	if result.Applied && result.Version < twin.ReportedVersion {
		return nil
	}
	reportedAt := result.ReportedAt.UTC()
	if result.ReportedAt.IsZero() {
		reportedAt = time.Now().UTC()
	}
	if result.Applied {
		twin.Reported = result.Config
		twin.ReportedVersion = result.Version
		twin.UpdatedAt = reportedAt
		r.twins[deviceID] = twin
		meta.ReportedVersion = result.Version
		meta.LastReportState = controlplane.SettingsApplied
		meta.FailureCode = ""
	} else {
		meta.LastReportState = controlplane.SettingsRejected
		meta.FailureCode = result.FailureCode
	}
	meta.DesiredVersion = twin.DesiredVersion
	if meta.DesiredAt.IsZero() {
		meta.DesiredAt = twin.UpdatedAt
	}
	meta.LastReportVersion = result.Version
	meta.ReportedAt = &reportedAt
	r.metadata[deviceID] = meta
	return nil
}

func (r *fakeTwinRepo) SettingsMetadata(_ context.Context, userID, deviceID string) (controlplane.SettingsMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	twin, ok := r.twins[deviceID]
	if !ok {
		return controlplane.SettingsMetadata{}, fmt.Errorf("device not found")
	}
	if twin.UserID != userID {
		return controlplane.SettingsMetadata{}, fmt.Errorf("device owner mismatch")
	}
	meta := r.metadata[deviceID]
	meta.DesiredVersion = twin.DesiredVersion
	meta.ReportedVersion = twin.ReportedVersion
	if meta.DesiredAt.IsZero() {
		meta.DesiredAt = twin.UpdatedAt
	}
	return meta, nil
}

var _ controlplane.SettingsReportRepository = (*fakeTwinRepo)(nil)

func newSettingsTestSession(t *testing.T, srv *Server, deviceID, userID string) *session {
	t.Helper()
	s := &session{
		id:            "session-" + deviceID,
		deviceID:      deviceID,
		userID:        userID,
		hub:           srv.hub,
		controlPlane:  srv.controlPlane,
		controlWrites: make(chan outbound, 16),
		mediaWrites:   make(chan outbound, 4),
		seenInbound:   map[string]inboundRecord{},
		generation:    1,
	}
	srv.hub.register(deviceID, s)
	advertise, err := protocol.Encode(protocol.CapabilityAdvertiseType, protocol.Metadata{
		MessageID: "advertise-settings", SessionID: s.id,
	}, protocol.CapabilityAdvertisePayload{Capabilities: []protocol.CapabilityDescriptor{{
		Name: devicecap.SettingsName, Version: devicecap.SettingsVersion, Kind: "command",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	handled, err := s.handleCapabilityControl(context.Background(), advertise)
	if err != nil || !handled {
		t.Fatalf("advertise handled=%v err=%v", handled, err)
	}
	if !s.Supports(devicecap.SettingsName, devicecap.SettingsVersion) {
		t.Fatal("advertised settings capability unavailable")
	}
	t.Cleanup(func() { srv.hub.unregister(deviceID, s) })
	return s
}

func TestDeviceSettingsHappyApply(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	sess := newSettingsTestSession(t, srv, "dev-apply", "user-apply")

	patchBody := []byte(`{"vad_threshold":700,"locale":"vi-VN"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/dev-apply/twin?user_id=user-apply", bytes.NewReader(patchBody))
	req.SetPathValue("deviceID", "dev-apply")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.handleTwinPatch(rec, req) }()

	var callOut outbound
	select {
	case callOut = <-sess.controlWrites:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capability.call")
	}
	callEnvelope, err := protocol.Decode(callOut.data)
	if err != nil { t.Fatal(err) }
	if callEnvelope.Type != protocol.CapabilityCallType {
		t.Fatalf("type = %s, want %s", callEnvelope.Type, protocol.CapabilityCallType)
	}
	callPayload, err := protocol.DecodePayload[protocol.CapabilityCallPayload](callEnvelope)
	if err != nil { t.Fatal(err) }
	if callPayload.Name != devicecap.SettingsName || callPayload.Version != devicecap.SettingsVersion {
		t.Fatalf("call name/version = %s@%s", callPayload.Name, callPayload.Version)
	}
	var args devicecap.SettingsArgs
	if err := json.Unmarshal(callPayload.Arguments, &args); err != nil { t.Fatal(err) }
	if args.Version != 1 || args.Settings.VADThreshold == nil || *args.Settings.VADThreshold != 700 || args.Settings.Locale != "" || args.Settings.WakeModel != "" {
		t.Fatalf("device args = %+v", args)
	}

	resultVal, _ := json.Marshal(devicecap.SettingsResult{Applied: true, Version: args.Version, Settings: &args.Settings})
	resultMsg, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "res-1", SessionID: sess.id, CorrelationID: callEnvelope.CorrelationID, GenerationID: callEnvelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: resultVal})
	if err != nil { t.Fatal(err) }
	handled, err := sess.handleCapabilityControl(context.Background(), resultMsg)
	if err != nil || !handled { t.Fatalf("result handled=%v err=%v", handled, err) }
	<-done

	if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	var twin controlplane.Twin
	if err := json.Unmarshal(rec.Body.Bytes(), &twin); err != nil { t.Fatal(err) }
	if twin.Status != controlplane.TwinStatusApplied || twin.DesiredVersion != 1 || twin.ReportedVersion != 1 {
		t.Fatalf("twin=%+v", twin)
	}
	if twin.Desired.Locale != "vi-VN" || twin.Reported.Locale != "" {
		t.Fatalf("backend-owned locale leaked into device report: desired=%q reported=%q", twin.Desired.Locale, twin.Reported.Locale)
	}
	if twin.Desired.VADThreshold == nil || twin.Reported.VADThreshold == nil || *twin.Desired.VADThreshold != 700 || *twin.Reported.VADThreshold != 700 {
		t.Fatalf("threshold desired/reported mismatch: desired=%+v reported=%+v", twin.Desired, twin.Reported)
	}
}

func TestDeviceSettingsDeviceRejection(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	sess := newSettingsTestSession(t, srv, "dev-reject", "user-reject")

	patchBody := []byte(`{"vad_threshold":700}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/dev-reject/twin?user_id=user-reject", bytes.NewReader(patchBody))
	req.SetPathValue("deviceID", "dev-reject")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.handleTwinPatch(rec, req) }()

	var callOut outbound
	select {
	case callOut = <-sess.controlWrites:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capability.call")
	}
	callEnvelope, err := protocol.Decode(callOut.data)
	if err != nil { t.Fatal(err) }
	resultVal, _ := json.Marshal(devicecap.SettingsResult{Applied: false, Version: 1, Error: "device_rejected_for_test"})
	resultMsg, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "res-reject", SessionID: sess.id, CorrelationID: callEnvelope.CorrelationID, GenerationID: callEnvelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: resultVal})
	if err != nil { t.Fatal(err) }
	handled, err := sess.handleCapabilityControl(context.Background(), resultMsg)
	if err != nil || !handled { t.Fatalf("result handled=%v err=%v", handled, err) }
	<-done

	var twin controlplane.Twin
	if err := json.Unmarshal(rec.Body.Bytes(), &twin); err != nil { t.Fatal(err) }
	if twin.Status != controlplane.TwinStatusRejected || twin.DesiredVersion != 1 || twin.ReportedVersion != 0 {
		t.Fatalf("twin=%+v", twin)
	}
}

func TestDeviceSettingsWakeModelAndThresholdDispatchedInPlan07B(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	sess := newSettingsTestSession(t, srv, "dev-wake-plan07b", "user-wake-plan07b")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/dev-wake-plan07b/twin?user_id=user-wake-plan07b", bytes.NewReader([]byte(`{"wake_model":"hey_bin","wake_threshold":0.72}`)))
	req.SetPathValue("deviceID", "dev-wake-plan07b")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.handleTwinPatch(rec, req) }()

	var callOut outbound
	select {
	case callOut = <-sess.controlWrites:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capability.call")
	}
	callEnvelope, err := protocol.Decode(callOut.data)
	if err != nil {
		t.Fatal(err)
	}
	callPayload, err := protocol.DecodePayload[protocol.CapabilityCallPayload](callEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	var args devicecap.SettingsArgs
	if err := json.Unmarshal(callPayload.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args.Settings.WakeModel != controlplane.WakeModelHeyBin || args.Settings.WakeThreshold == nil || *args.Settings.WakeThreshold != 0.72 {
		t.Fatalf("wake settings in args mismatch: %+v", args)
	}

	resultVal, _ := json.Marshal(devicecap.SettingsResult{Applied: true, Version: args.Version, Settings: &args.Settings})
	resultMsg, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "res-wake", SessionID: sess.id, CorrelationID: callEnvelope.CorrelationID, GenerationID: callEnvelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: resultVal})
	if err != nil {
		t.Fatal(err)
	}
	handled, err := sess.handleCapabilityControl(context.Background(), resultMsg)
	if err != nil || !handled {
		t.Fatalf("result handled=%v err=%v", handled, err)
	}
	<-done

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var twin controlplane.Twin
	if err := json.Unmarshal(rec.Body.Bytes(), &twin); err != nil {
		t.Fatal(err)
	}
	if twin.Status != controlplane.TwinStatusApplied || twin.Desired.WakeModel != controlplane.WakeModelHeyBin || twin.Reported.WakeModel != controlplane.WakeModelHeyBin {
		t.Fatalf("wake twin mismatch=%+v", twin)
	}
}

func TestDeviceSettingsOfflineDevice(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	patchBody := []byte(`{"locale":"en-US"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/dev-offline/twin?user_id=user-offline", bytes.NewReader(patchBody))
	req.SetPathValue("deviceID", "dev-offline")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	srv.handleTwinPatch(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	var twin controlplane.Twin
	if err := json.Unmarshal(rec.Body.Bytes(), &twin); err != nil { t.Fatal(err) }
	if twin.Status != controlplane.TwinStatusOffline || twin.DesiredVersion != 1 || twin.ReportedVersion != 0 { t.Fatalf("twin=%+v", twin) }
}

func TestDeviceSettingsWrongOwner(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	_, err := repo.SetDesired(context.Background(), "owner-a", "dev-owned", controlplane.RuntimeConfig{Locale: "vi-VN"})
	if err != nil { t.Fatal(err) }
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/dev-owned/twin?user_id=owner-b", bytes.NewReader([]byte(`{"locale":"en-US"}`)))
	patchReq.SetPathValue("deviceID", "dev-owned")
	patchReq.Header.Set("Authorization", "Bearer admin-secret")
	patchRec := httptest.NewRecorder()
	srv.handleTwinPatch(patchRec, patchReq)
	if patchRec.Code != http.StatusBadRequest { t.Fatalf("patch status=%d", patchRec.Code) }
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/devices/dev-owned/twin?user_id=owner-b", nil)
	getReq.SetPathValue("deviceID", "dev-owned")
	getReq.Header.Set("Authorization", "Bearer admin-secret")
	getRec := httptest.NewRecorder()
	srv.handleTwinGet(getRec, getReq)
	if getRec.Code != http.StatusBadRequest { t.Fatalf("get status=%d", getRec.Code) }
}

func TestDeviceSettingsReconnectionReconciliation(t *testing.T) {
	repo := newFakeTwinRepo()
	router := devicecap.NewRouter()
	cp := controlplane.New(repo, controlplane.RuntimeConfig{})
	srv := New(pipeline.Components{}, nil, WithAdminToken("admin-secret"), WithControlPlane(cp), WithDeviceCapabilities(router))
	threshold := 720
	seededTwin, err := repo.SetDesired(context.Background(), "user-recon", "dev-recon", controlplane.RuntimeConfig{VADThreshold: &threshold, Locale: "vi-VN"})
	if err != nil { t.Fatal(err) }
	if seededTwin.DesiredVersion != 1 || seededTwin.ReportedVersion != 0 { t.Fatalf("seeded=%+v", seededTwin) }

	s := &session{
		id: "session-dev-recon", deviceID: "dev-recon", userID: "user-recon", hub: srv.hub, controlPlane: srv.controlPlane,
		controlWrites: make(chan outbound, 16), mediaWrites: make(chan outbound, 4), seenInbound: map[string]inboundRecord{}, generation: 1,
	}
	srv.hub.register("dev-recon", s)
	defer srv.hub.unregister("dev-recon", s)
	advertise, err := protocol.Encode(protocol.CapabilityAdvertiseType, protocol.Metadata{MessageID: "advertise-reconcile", SessionID: s.id}, protocol.CapabilityAdvertisePayload{Capabilities: []protocol.CapabilityDescriptor{{Name: devicecap.SettingsName, Version: devicecap.SettingsVersion, Kind: "command"}}})
	if err != nil { t.Fatal(err) }
	handled, err := s.handleCapabilityControl(context.Background(), advertise)
	if err != nil || !handled { t.Fatalf("advertise handled=%v err=%v", handled, err) }

	var callOut outbound
	select {
	case callOut = <-s.controlWrites:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconciliation capability.call")
	}
	callEnvelope, err := protocol.Decode(callOut.data)
	if err != nil { t.Fatal(err) }
	var args devicecap.SettingsArgs
	payload, _ := protocol.DecodePayload[protocol.CapabilityCallPayload](callEnvelope)
	if err := json.Unmarshal(payload.Arguments, &args); err != nil { t.Fatal(err) }
	if args.Version != 1 || args.Settings.VADThreshold == nil || *args.Settings.VADThreshold != threshold || args.Settings.Locale != "" {
		t.Fatalf("reconciled args=%+v", args)
	}
	resultVal, _ := json.Marshal(devicecap.SettingsResult{Applied: true, Version: 1, Settings: &args.Settings})
	resultMsg, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{MessageID: "res-recon-1", SessionID: s.id, CorrelationID: callEnvelope.CorrelationID, GenerationID: callEnvelope.GenerationID}, protocol.CapabilityResultPayload{OK: true, Value: resultVal})
	if err != nil { t.Fatal(err) }
	handled, err = s.handleCapabilityControl(context.Background(), resultMsg)
	if err != nil || !handled { t.Fatalf("result handled=%v err=%v", handled, err) }

	var twin controlplane.Twin
	deadline := time.Now().Add(time.Second)
	for {
		twin, err = repo.GetTwin(context.Background(), "user-recon", "dev-recon")
		if err == nil && twin.ReportedVersion == 1 { break }
		if time.Now().After(deadline) { t.Fatalf("reconciled timeout: err=%v twin=%+v", err, twin) }
		time.Sleep(10 * time.Millisecond)
	}
	if twin.Reported.VADThreshold == nil || *twin.Reported.VADThreshold != threshold || twin.Reported.Locale != "" {
		t.Fatalf("reconciled twin=%+v", twin)
	}
}

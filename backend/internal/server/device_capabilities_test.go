package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"companion-server/internal/devicecap"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
)

func capabilityTestSession(t *testing.T) (*session, *devicecap.Router) {
	t.Helper()
	router := devicecap.NewRouter()
	service := New(pipeline.Components{}, nil, WithDeviceCapabilities(router))
	s := &session{
		id: "session-cap-test", deviceID: "device-a", userID: "user-a", hub: service.hub,
		controlWrites: make(chan outbound, 16), mediaWrites: make(chan outbound, 4),
		seenInbound: map[string]inboundRecord{}, generation: 7,
	}
	advertise, err := protocol.Encode(protocol.CapabilityAdvertiseType, protocol.Metadata{
		MessageID: "advertise-1", SessionID: s.id,
	}, protocol.CapabilityAdvertisePayload{Capabilities: []protocol.CapabilityDescriptor{{
		Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion, Kind: "command",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	handled, err := s.handleCapabilityControl(context.Background(), advertise)
	if err != nil || !handled {
		t.Fatalf("advertise handled=%v err=%v", handled, err)
	}
	if !s.Supports(devicecap.VolumeSetName, devicecap.VolumeSetVersion) {
		t.Fatal("advertised capability unavailable")
	}
	t.Cleanup(func() { detachDeviceCapabilities(s) })
	return s, router
}

func TestDeviceCapabilityCallCorrelatesSuccessfulResult(t *testing.T) {
	s, router := capabilityTestSession(t)
	resultCh := make(chan devicecap.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := router.Call(context.Background(), s.deviceID, devicecap.Call{
			Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion,
			Arguments: json.RawMessage(`{"volume":42}`), TurnID: "turn-cap", Deadline: time.Now().Add(time.Second),
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	call := <-s.controlWrites
	envelope, err := protocol.Decode(call.data)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Type != protocol.CapabilityCallType || envelope.CorrelationID == "" || envelope.GenerationID != 7 || envelope.TurnID != "turn-cap" {
		t.Fatalf("call envelope=%+v", envelope)
	}
	payload, err := protocol.DecodePayload[protocol.CapabilityCallPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Name != devicecap.VolumeSetName || payload.Version != devicecap.VolumeSetVersion {
		t.Fatalf("call payload=%+v", payload)
	}

	response, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "result-1", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: envelope.TurnID, GenerationID: envelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"applied":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := s.handleCapabilityControl(context.Background(), response); err != nil || !handled {
		t.Fatalf("result handled=%v err=%v", handled, err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-resultCh:
		if string(result.Value) != `{"applied":true}` {
			t.Fatalf("value=%s", result.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("capability result timed out")
	}
}

func TestDeviceCapabilityNonCancelableCallReturnsContextErrorWithoutCancelFrame(t *testing.T) {
	s, router := capabilityTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := router.Call(ctx, s.deviceID, devicecap.Call{
			Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion,
			Arguments: json.RawMessage(`{"volume":10}`), TurnID: "turn-cap", Deadline: time.Now().Add(2 * time.Second),
		})
		errCh <- err
	}()
	call := <-s.controlWrites
	callEnvelope, err := protocol.Decode(call.data)
	if err != nil {
		t.Fatal(err)
	}
	if callEnvelope.Type != protocol.CapabilityCallType {
		t.Fatalf("call envelope=%+v", callEnvelope)
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("call err=%v", err)
	}
	select {
	case frame := <-s.controlWrites:
		envelope, decodeErr := protocol.Decode(frame.data)
		t.Fatalf("non-cancelable volume call emitted frame=%+v decodeErr=%v", envelope, decodeErr)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDeviceCapabilityRejectsMalformedSuccessfulResult(t *testing.T) {
	s, router := capabilityTestSession(t)
	errCh := make(chan error, 1)
	go func() {
		_, err := router.Call(context.Background(), s.deviceID, devicecap.Call{
			Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion,
			Arguments: json.RawMessage(`{"volume":20}`), TurnID: "turn-cap", Deadline: time.Now().Add(time.Second),
		})
		errCh <- err
	}()
	call := <-s.controlWrites
	envelope, err := protocol.Decode(call.data)
	if err != nil {
		t.Fatal(err)
	}
	malformed, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "bad-result", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: envelope.TurnID, GenerationID: envelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"unexpected":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := s.handleCapabilityControl(context.Background(), malformed); err != nil || !handled {
		t.Fatalf("malformed result dispatch handled=%v err=%v", handled, err)
	}
	if err := <-errCh; !errors.Is(err, devicecap.ErrContractViolation) {
		t.Fatalf("malformed result err=%v", err)
	}
}

func TestCapabilityAdvertisementUsesCatalogAndPreservesPreviousSetOnInvalidReplacement(t *testing.T) {
	s, _ := capabilityTestSession(t)
	invalid, err := protocol.Encode(protocol.CapabilityAdvertiseType, protocol.Metadata{
		MessageID: "advertise-invalid", SessionID: s.id,
	}, protocol.CapabilityAdvertisePayload{Capabilities: []protocol.CapabilityDescriptor{{
		Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion, Kind: "read",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := s.handleCapabilityControl(context.Background(), invalid); !handled || err == nil {
		t.Fatalf("invalid replacement handled=%v err=%v", handled, err)
	}
	if !s.Supports(devicecap.VolumeSetName, devicecap.VolumeSetVersion) {
		t.Fatal("invalid replacement mutated previous accepted capability set")
	}
}

func TestDeviceCapabilityRejectsStaleGenerationAndDisconnectsEndpoint(t *testing.T) {
	s, router := capabilityTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := router.Call(ctx, s.deviceID, devicecap.Call{
			Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion,
			Arguments: json.RawMessage(`{"volume":20}`), TurnID: "turn-cap", Deadline: time.Now().Add(2 * time.Second),
		})
		errCh <- err
	}()
	call := <-s.controlWrites
	envelope, err := protocol.Decode(call.data)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.generation++
	s.mu.Unlock()
	stale, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "stale-result", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: envelope.TurnID, GenerationID: envelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"applied":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := s.handleCapabilityControl(context.Background(), stale); !handled || err == nil {
		t.Fatalf("stale result handled=%v err=%v", handled, err)
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("call err=%v", err)
	}

	detachDeviceCapabilities(s)
	if s.Supports(devicecap.VolumeSetName, devicecap.VolumeSetVersion) {
		t.Fatal("detached session still supports capability")
	}
	if _, err := router.Call(context.Background(), s.deviceID, devicecap.Call{Name: devicecap.VolumeSetName, Version: devicecap.VolumeSetVersion}); !errors.Is(err, devicecap.ErrOffline) {
		t.Fatalf("router after disconnect err=%v", err)
	}
}

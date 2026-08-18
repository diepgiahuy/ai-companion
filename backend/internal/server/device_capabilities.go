package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/devicecap"
	"companion-server/internal/protocol"
)

type capabilityPending struct {
	generation    uint64
	turnID        string
	sessionScoped bool
	result        chan protocol.CapabilityResultPayload
}

type sessionCapabilityState struct {
	router     *devicecap.Router
	mu         sync.Mutex
	advertised map[string]protocol.CapabilityDescriptor
	pending    map[string]*capabilityPending
	closed     bool
}

// WithDeviceCapabilities binds the process-local authenticated device router to
// this Server's own session hub. No package-global registry is used, so router
// ownership and session capability state end with the Server/session lifecycle.
func WithDeviceCapabilities(router *devicecap.Router) Option {
	return func(s *Server) {
		if s == nil || s.hub == nil || router == nil {
			return
		}
		s.hub.setCapabilityRouter(router)
	}
}

func capabilityKey(name, version string) string {
	return strings.TrimSpace(name) + "@" + strings.TrimSpace(version)
}

func allowedDeviceCapability(descriptor protocol.CapabilityDescriptor) bool {
	if descriptor.Version != "1" || descriptor.Kind != "command" {
		return false
	}
	switch descriptor.Name {
	case devicecap.VolumeSetName, devicecap.UserConfirmationName, devicecap.SettingsName:
		return true
	default:
		return false
	}
}

func capabilityState(s *session, create bool) *sessionCapabilityState {
	if s == nil || s.hub == nil {
		return nil
	}
	return s.hub.capabilityState(s, create)
}

func detachDeviceCapabilities(s *session) {
	if s == nil || s.hub == nil {
		return
	}
	s.hub.detachCapabilities(s)
}

func (s *session) Supports(name, version string) bool {
	state := capabilityState(s, false)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return false
	}
	_, ok := state.advertised[capabilityKey(name, version)]
	return ok
}

func (s *session) Call(ctx context.Context, call devicecap.Call) (devicecap.Result, error) {
	state := capabilityState(s, false)
	if state == nil {
		return devicecap.Result{}, devicecap.ErrOffline
	}
	if !s.Supports(call.Name, call.Version) {
		return devicecap.Result{}, devicecap.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return devicecap.Result{}, err
	}
	turnID := strings.TrimSpace(call.TurnID)
	if len(turnID) > 128 {
		return devicecap.Result{}, fmt.Errorf("device capability turn_id exceeds size limit")
	}

	// Settings reconciliation is intentionally session-scoped. It must work
	// immediately after capability advertisement, before the first conversational
	// turn creates a media generation. Turn-owned capabilities keep the existing
	// generation binding so stale results cannot cross a barge-in/cancel boundary.
	sessionScoped := call.Name == devicecap.SettingsName && turnID == ""
	var generation uint64
	if !sessionScoped {
		s.mu.Lock()
		generation = s.generation
		s.mu.Unlock()
		if generation == 0 {
			return devicecap.Result{}, fmt.Errorf("turn-scoped device capability requires an active generation")
		}
	}

	deadline := call.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(3 * time.Second)
	}
	remaining := time.Until(deadline)
	if remaining < 50*time.Millisecond {
		return devicecap.Result{}, context.DeadlineExceeded
	}
	if remaining > 5*time.Second {
		remaining = 5 * time.Second
		deadline = time.Now().Add(remaining)
	}
	deadlineMS := int(remaining.Milliseconds())
	if deadlineMS < 50 {
		deadlineMS = 50
	}
	if deadlineMS > 5000 {
		deadlineMS = 5000
	}

	correlationID := "cap-" + s.nextMessageID()
	waiter := &capabilityPending{
		generation: generation, turnID: turnID, sessionScoped: sessionScoped,
		result: make(chan protocol.CapabilityResultPayload, 1),
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return devicecap.Result{}, devicecap.ErrOffline
	}
	state.pending[correlationID] = waiter
	state.mu.Unlock()
	removePending := func() {
		state.mu.Lock()
		if state.pending[correlationID] == waiter {
			delete(state.pending, correlationID)
		}
		state.mu.Unlock()
	}
	defer removePending()

	payload := protocol.CapabilityCallPayload{Name: call.Name, Version: call.Version, Arguments: call.Arguments, DeadlineMS: deadlineMS}
	if err := s.sendJSONMeta(ctx, protocol.CapabilityCallType, protocol.Metadata{CorrelationID: correlationID, TurnID: turnID, GenerationID: generation}, payload); err != nil {
		return devicecap.Result{}, err
	}

	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	select {
	case <-waitCtx.Done():
		cancelCtx, cancelSend := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = s.sendJSONMeta(cancelCtx, protocol.CapabilityCancelType, protocol.Metadata{CorrelationID: correlationID, TurnID: turnID, GenerationID: generation}, protocol.CapabilityCancelPayload{Reason: capabilityCancelReason(waitCtx.Err())})
		cancelSend()
		return devicecap.Result{}, waitCtx.Err()
	case result, ok := <-waiter.result:
		if !ok {
			return devicecap.Result{}, devicecap.ErrOffline
		}
		if !result.OK {
			return devicecap.Result{}, capabilityRemoteError(result.Error)
		}
		return devicecap.Result{Value: append(json.RawMessage(nil), result.Value...)}, nil
	}
}

func capabilityCancelReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "turn_canceled"
}

func capabilityRemoteError(code string) error {
	switch code {
	case protocol.CapabilityErrorUnsupported:
		return devicecap.ErrUnsupported
	case protocol.CapabilityErrorTimeout:
		return context.DeadlineExceeded
	case protocol.CapabilityErrorCanceled:
		return context.Canceled
	case protocol.CapabilityErrorStaleGeneration:
		return fmt.Errorf("device capability stale generation")
	case protocol.CapabilityErrorBusy:
		return fmt.Errorf("device capability busy")
	case protocol.CapabilityErrorInvalidArgument:
		return fmt.Errorf("device capability invalid argument")
	default:
		return fmt.Errorf("device capability failed")
	}
}

func (s *session) handleCapabilityControl(ctx context.Context, data []byte) (bool, error) {
	// Pairing is another authenticated session extension. Dispatch it from the
	// same pre-generic control seam so the core turn/control switch remains small;
	// pairing itself lives in pairing_control.go and is not a device capability.
	if handled, err := s.handlePairingControl(ctx, data); handled {
		return true, err
	}
	message, err := protocol.Decode(data)
	if err != nil {
		return false, nil
	}
	if message.Type != protocol.CapabilityAdvertiseType && message.Type != protocol.CapabilityResultType {
		return false, nil
	}
	if message.SessionID != s.id {
		return true, fmt.Errorf("session_id does not match")
	}
	switch message.Type {
	case protocol.CapabilityAdvertiseType:
		payload, err := protocol.DecodePayload[protocol.CapabilityAdvertisePayload](message)
		if err != nil {
			return true, err
		}
		if message.GenerationID != 0 || message.TurnID != "" || message.CorrelationID != "" {
			return true, &protocol.ProtocolError{Code: protocol.InvalidEnvelopeCode, Detail: "capability advertisement must be session-scoped"}
		}
		return true, s.processInbound(message.MessageID, data, func() error {
			state := capabilityState(s, true)
			if state == nil {
				return fmt.Errorf("device capability router unavailable")
			}
			advertised := make(map[string]protocol.CapabilityDescriptor, len(payload.Capabilities))
			for _, descriptor := range payload.Capabilities {
				if !allowedDeviceCapability(descriptor) {
					return fmt.Errorf("unsupported advertised capability %s@%s", descriptor.Name, descriptor.Version)
				}
				advertised[capabilityKey(descriptor.Name, descriptor.Version)] = descriptor
			}
			state.mu.Lock()
			if state.closed {
				state.mu.Unlock()
				return devicecap.ErrOffline
			}
			state.advertised = advertised
			state.mu.Unlock()
			if s.Supports(devicecap.SettingsName, devicecap.SettingsVersion) {
				go s.reconcileSettings(context.Background())
			}
			return nil
		})
	case protocol.CapabilityResultType:
		payload, err := protocol.DecodePayload[protocol.CapabilityResultPayload](message)
		if err != nil {
			return true, err
		}
		if strings.TrimSpace(message.CorrelationID) == "" {
			return true, &protocol.ProtocolError{Code: protocol.InvalidEnvelopeCode, Detail: "capability result requires correlation_id"}
		}
		return true, s.processInbound(message.MessageID, data, func() error {
			state := capabilityState(s, false)
			if state == nil {
				return devicecap.ErrOffline
			}
			state.mu.Lock()
			waiter := state.pending[message.CorrelationID]
			if waiter == nil {
				state.mu.Unlock()
				return fmt.Errorf("unknown capability correlation_id")
			}
			if message.GenerationID != waiter.generation || strings.TrimSpace(message.TurnID) != waiter.turnID {
				state.mu.Unlock()
				return fmt.Errorf("stale capability turn or generation")
			}
			delete(state.pending, message.CorrelationID)
			state.mu.Unlock()
			if !waiter.sessionScoped && !s.generationCurrent(waiter.generation) {
				return fmt.Errorf("stale capability generation")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case waiter.result <- payload:
				return nil
			default:
				return fmt.Errorf("duplicate capability result")
			}
		})
	}
	return false, nil
}

func decodeSettingsResult(raw json.RawMessage) (devicecap.SettingsResult, error) {
	var result devicecap.SettingsResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return result, fmt.Errorf("multiple settings result values")
		}
		return result, err
	}
	return result, nil
}

func (s *session) reconcileSettings(ctx context.Context) {
	if s == nil || s.controlPlane == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	twin, err := s.controlPlane.ManifestFor(ctx, controlplane.ResolutionContext{
		UserID:   s.userID,
		DeviceID: s.deviceID,
		TenantID: s.tenantID,
		Plan:     s.plan,
	})
	if err != nil {
		return
	}
	// A durable reported_version is evidence about the last successful runtime,
	// not proof that a newly booted/reconnected process still holds the config.
	// Reassert every non-zero desired revision on each authenticated session.
	// Exact-version duplicate apply is idempotent on the device side.
	if twin.DesiredVersion <= 0 {
		return
	}
	if !s.Supports(devicecap.SettingsName, devicecap.SettingsVersion) {
		return
	}
	args, err := json.Marshal(devicecap.SettingsArgs{
		Version:  twin.DesiredVersion,
		Settings: twin.Desired,
	})
	if err != nil {
		return
	}
	res, err := s.Call(ctx, devicecap.Call{
		Name:      devicecap.SettingsName,
		Version:   devicecap.SettingsVersion,
		Arguments: args,
		Deadline:  time.Now().Add(3 * time.Second),
	})
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("settings reconciliation call failed", "device_id", s.deviceID, "error", err)
		}
		return
	}
	result, err := decodeSettingsResult(res.Value)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("invalid settings reconciliation result", "device_id", s.deviceID, "error", err)
		}
		return
	}
	if result.Version != twin.DesiredVersion {
		if s.logger != nil {
			s.logger.Warn("device acknowledged unexpected settings version", "device_id", s.deviceID, "want", twin.DesiredVersion, "got", result.Version)
		}
		return
	}
	failureCode := strings.TrimSpace(result.Error)
	if !result.Applied && failureCode == "" {
		failureCode = "device_rejected"
	}
	report := controlplane.ConfigReportResult{
		Version:     result.Version,
		Applied:     result.Applied,
		Config:      twin.Desired,
		FailureCode: failureCode,
		ReportedAt:  time.Now().UTC(),
	}
	if err := s.controlPlane.ReportResult(ctx, s.userID, s.deviceID, report); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to persist reconciled settings outcome", "device_id", s.deviceID, "version", result.Version, "applied", result.Applied, "error", err)
		}
		return
	}
	if !result.Applied && s.logger != nil {
		s.logger.Warn("device rejected settings reconciliation", "device_id", s.deviceID, "version", result.Version, "error", failureCode)
	}
}

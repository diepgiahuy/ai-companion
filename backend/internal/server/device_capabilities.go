package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"companion-server/internal/devicecap"
	"companion-server/internal/protocol"
)

type capabilityPending struct {
	generation uint64
	result     chan protocol.CapabilityResultPayload
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
	return descriptor.Name == devicecap.VolumeSetName &&
		descriptor.Version == devicecap.VolumeSetVersion &&
		descriptor.Kind == "command"
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

	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	if generation == 0 {
		return devicecap.Result{}, fmt.Errorf("device capability requires an active generation")
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
	waiter := &capabilityPending{generation: generation, result: make(chan protocol.CapabilityResultPayload, 1)}
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
	if err := s.sendJSONMeta(ctx, protocol.CapabilityCallType, protocol.Metadata{CorrelationID: correlationID, GenerationID: generation}, payload); err != nil {
		return devicecap.Result{}, err
	}

	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	select {
	case <-waitCtx.Done():
		cancelCtx, cancelSend := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = s.sendJSONMeta(cancelCtx, protocol.CapabilityCancelType, protocol.Metadata{CorrelationID: correlationID, GenerationID: generation}, protocol.CapabilityCancelPayload{Reason: capabilityCancelReason(waitCtx.Err())})
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
			if message.GenerationID != waiter.generation {
				state.mu.Unlock()
				return fmt.Errorf("stale capability generation")
			}
			delete(state.pending, message.CorrelationID)
			state.mu.Unlock()
			if !s.generationCurrent(waiter.generation) {
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

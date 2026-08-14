package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	CapabilityAdvertiseType MessageType = "capability.advertise"
	CapabilityCallType      MessageType = "capability.call"
	CapabilityResultType    MessageType = "capability.result"
	CapabilityCancelType    MessageType = "capability.cancel"
)

const (
	CapabilityErrorUnsupported    = "unsupported"
	CapabilityErrorInvalidArgument = "invalid_argument"
	CapabilityErrorBusy           = "busy"
	CapabilityErrorTimeout        = "timeout"
	CapabilityErrorCanceled       = "canceled"
	CapabilityErrorInternal       = "internal"
	CapabilityErrorStaleGeneration = "stale_generation"
)

type CapabilityDescriptor struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

func (d CapabilityDescriptor) Validate() error {
	if err := validateOpaqueID("capability name", d.Name, 96); err != nil {
		return err
	}
	if err := validateOpaqueID("capability version", d.Version, 32); err != nil {
		return err
	}
	switch d.Kind {
	case "command", "read":
		return nil
	default:
		return fmt.Errorf("unsupported capability kind %q", d.Kind)
	}
}

type CapabilityAdvertisePayload struct {
	Capabilities []CapabilityDescriptor `json:"capabilities"`
}

func (p CapabilityAdvertisePayload) Validate() error {
	if len(p.Capabilities) == 0 || len(p.Capabilities) > 32 {
		return fmt.Errorf("capabilities must contain 1..32 descriptors")
	}
	seen := map[string]bool{}
	for _, descriptor := range p.Capabilities {
		if err := descriptor.Validate(); err != nil {
			return err
		}
		key := descriptor.Name + "@" + descriptor.Version
		if seen[key] {
			return fmt.Errorf("duplicate capability descriptor %q", key)
		}
		seen[key] = true
	}
	return nil
}

type CapabilityCallPayload struct {
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Arguments  json.RawMessage `json:"arguments"`
	DeadlineMS int             `json:"deadline_ms"`
}

func (p CapabilityCallPayload) Validate() error {
	if err := (CapabilityDescriptor{Name: p.Name, Version: p.Version, Kind: "command"}).Validate(); err != nil {
		return err
	}
	if p.DeadlineMS < 50 || p.DeadlineMS > 5000 {
		return fmt.Errorf("capability deadline_ms must be within 50..5000")
	}
	trimmed := bytes.TrimSpace(p.Arguments)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || !json.Valid(trimmed) {
		return fmt.Errorf("capability arguments must be a JSON object")
	}
	return nil
}

type CapabilityResultPayload struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

func (p CapabilityResultPayload) Validate() error {
	if p.OK {
		if p.Error != "" {
			return fmt.Errorf("successful capability result must not include error")
		}
		if len(p.Value) == 0 || !json.Valid(p.Value) {
			return fmt.Errorf("successful capability result requires valid JSON value")
		}
		return nil
	}
	if len(p.Value) != 0 {
		return fmt.Errorf("failed capability result must not include value")
	}
	switch p.Error {
	case CapabilityErrorUnsupported, CapabilityErrorInvalidArgument, CapabilityErrorBusy,
		CapabilityErrorTimeout, CapabilityErrorCanceled, CapabilityErrorInternal,
		CapabilityErrorStaleGeneration:
		return nil
	default:
		return fmt.Errorf("unsupported capability error %q", p.Error)
	}
}

type CapabilityCancelPayload struct {
	Reason string `json:"reason"`
}

func (p CapabilityCancelPayload) Validate() error {
	if strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 64 {
		return fmt.Errorf("capability cancel reason must be 1..64 bytes")
	}
	return nil
}

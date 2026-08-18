package devicecap

import (
	"encoding/json"
	"fmt"
	"strings"

	"companion-server/internal/capability"
)

type ContractScope string

type ContractAudience string

const (
	ContractScopeSession ContractScope = "session"
	ContractScopeTurn    ContractScope = "turn"

	ContractAudienceModel    ContractAudience = "model"
	ContractAudienceInternal ContractAudience = "internal"
	ContractAudiencePolicy   ContractAudience = "policy"
)

type Contract struct {
	Name           string
	Version        string
	Kind           string
	Scope          ContractScope
	Cancelable     bool
	Audience       ContractAudience
	InputSchema    map[string]any
	ResultSchema   map[string]any
	ToolDefinition *capability.ToolDefinition
}

func (c Contract) key() string {
	return strings.TrimSpace(c.Name) + "@" + strings.TrimSpace(c.Version)
}

func (c Contract) ValidateInput(raw json.RawMessage) error {
	if err := capability.ValidateJSON(c.InputSchema, raw); err != nil {
		return fmt.Errorf("%s input rejected: %w", c.key(), err)
	}
	return nil
}

func (c Contract) ValidateResult(raw json.RawMessage) error {
	if err := capability.ValidateJSON(c.ResultSchema, raw); err != nil {
		return fmt.Errorf("%s result rejected: %w", c.key(), err)
	}
	return nil
}

type ContractCatalog struct {
	byKey map[string]Contract
}

func NewContractCatalog(contracts ...Contract) (*ContractCatalog, error) {
	catalog := &ContractCatalog{byKey: make(map[string]Contract, len(contracts))}
	for _, contract := range contracts {
		contract.Name = strings.TrimSpace(contract.Name)
		contract.Version = strings.TrimSpace(contract.Version)
		contract.Kind = strings.TrimSpace(contract.Kind)
		if contract.Name == "" || contract.Version == "" || contract.Kind == "" {
			return nil, fmt.Errorf("device capability contract identity is required")
		}
		if contract.Kind != "command" {
			return nil, fmt.Errorf("device capability contract %s@%s has inactive kind %q", contract.Name, contract.Version, contract.Kind)
		}
		if contract.Scope != ContractScopeSession && contract.Scope != ContractScopeTurn {
			return nil, fmt.Errorf("device capability contract %s@%s has invalid scope %q", contract.Name, contract.Version, contract.Scope)
		}
		if contract.Audience != ContractAudienceModel && contract.Audience != ContractAudienceInternal && contract.Audience != ContractAudiencePolicy {
			return nil, fmt.Errorf("device capability contract %s@%s has invalid audience %q", contract.Name, contract.Version, contract.Audience)
		}
		if contract.InputSchema == nil || contract.ResultSchema == nil {
			return nil, fmt.Errorf("device capability contract %s@%s requires input and result schemas", contract.Name, contract.Version)
		}
		if contract.Audience == ContractAudienceModel {
			if contract.ToolDefinition == nil || contract.ToolDefinition.Name != contract.Name {
				return nil, fmt.Errorf("model device capability contract %s@%s requires matching ToolDefinition", contract.Name, contract.Version)
			}
			contract.ToolDefinition.Parameters = contract.InputSchema
		} else if contract.ToolDefinition != nil {
			return nil, fmt.Errorf("non-model device capability contract %s@%s must not expose ToolDefinition", contract.Name, contract.Version)
		}
		key := contract.key()
		if _, exists := catalog.byKey[key]; exists {
			return nil, fmt.Errorf("duplicate device capability contract %q", key)
		}
		catalog.byKey[key] = contract
	}
	return catalog, nil
}

func DefaultContractCatalog() *ContractCatalog {
	catalog, err := NewContractCatalog(defaultContracts()...)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c *ContractCatalog) Lookup(name, version string) (Contract, bool) {
	if c == nil {
		return Contract{}, false
	}
	contract, ok := c.byKey[strings.TrimSpace(name)+"@"+strings.TrimSpace(version)]
	return contract, ok
}

func defaultContracts() []Contract {
	volumeInput := objectSchema(
		map[string]any{
			"volume": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
		[]string{"volume"},
	)
	deviceSettings := objectSchema(
		map[string]any{
			"smart_vad_enabled": map[string]any{"type": "boolean"},
			"vad_threshold":     map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"vad_silence_ms":    map[string]any{"type": "integer", "minimum": 100, "maximum": 5000},
			"vad_min_speech_ms": map[string]any{"type": "integer", "minimum": 50, "maximum": 5000},
			"idle_after_ms":     map[string]any{"type": "integer", "minimum": 1000, "maximum": 3600000},
			"alarm_visible_ms":  map[string]any{"type": "integer", "minimum": 1000, "maximum": 3600000},
			"ota_poll_interval_s": map[string]any{
				"type": "integer", "minimum": 3600, "maximum": 604800,
			},
		},
		nil,
	)

	return []Contract{
		{
			Name:        VolumeSetName,
			Version:     VolumeSetVersion,
			Kind:        "command",
			Scope:       ContractScopeTurn,
			Cancelable:  false,
			Audience:    ContractAudienceModel,
			InputSchema: volumeInput,
			ResultSchema: objectSchema(
				map[string]any{
					"applied": map[string]any{"type": "boolean"},
					"volume":  map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
				},
				[]string{"applied"},
			),
			ToolDefinition: &capability.ToolDefinition{
				Name:        VolumeSetName,
				Description: "Set the authenticated current device speaker volume from 0 to 100.",
				Pack:        "device",
				Risk:        "write",
			},
		},
		{
			Name:       UserConfirmationName,
			Version:    UserConfirmationVersion,
			Kind:       "command",
			Scope:      ContractScopeTurn,
			Cancelable: true,
			Audience:   ContractAudiencePolicy,
			InputSchema: objectSchema(
				map[string]any{
					"tool_name": map[string]any{"type": "string", "minLength": 1, "maxLength": 96},
					"prompt":    map[string]any{"type": "string", "minLength": 1, "maxLength": 192},
				},
				[]string{"tool_name", "prompt"},
			),
			ResultSchema: objectSchema(
				map[string]any{"approved": map[string]any{"type": "boolean"}},
				[]string{"approved"},
			),
		},
		{
			Name:       SettingsName,
			Version:    SettingsVersion,
			Kind:       "command",
			Scope:      ContractScopeSession,
			Cancelable: false,
			Audience:   ContractAudienceInternal,
			InputSchema: objectSchema(
				map[string]any{
					"version":  map[string]any{"type": "integer", "minimum": 1},
					"settings": deviceSettings,
				},
				[]string{"version", "settings"},
			),
			ResultSchema: objectSchema(
				map[string]any{
					"applied":  map[string]any{"type": "boolean"},
					"version":  map[string]any{"type": "integer", "minimum": 1},
					"settings": deviceSettings,
					"error":    map[string]any{"type": "string", "maxLength": 64},
				},
				[]string{"applied", "version"},
			),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

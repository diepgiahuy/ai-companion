package devicecap

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type contractEndpoint struct {
	result Result
	calls  int
}

func (e *contractEndpoint) Supports(name, version string) bool {
	return name == VolumeSetName && version == VolumeSetVersion
}

func (e *contractEndpoint) Call(context.Context, Call) (Result, error) {
	e.calls++
	return e.result, nil
}

func TestDefaultContractCatalogOwnsCurrentContracts(t *testing.T) {
	catalog := DefaultContractCatalog()

	volume, ok := catalog.Lookup(VolumeSetName, VolumeSetVersion)
	if !ok {
		t.Fatal("volume contract missing")
	}
	if volume.Kind != "command" || volume.Scope != ContractScopeTurn || volume.Cancelable || volume.Audience != ContractAudienceModel {
		t.Fatalf("volume contract=%+v", volume)
	}
	if volume.ToolDefinition == nil || !reflect.DeepEqual(volume.ToolDefinition.Parameters, volume.InputSchema) {
		t.Fatal("volume ToolDefinition does not derive from contract input schema")
	}

	confirmation, ok := catalog.Lookup(UserConfirmationName, UserConfirmationVersion)
	if !ok || confirmation.Scope != ContractScopeTurn || !confirmation.Cancelable || confirmation.Audience != ContractAudiencePolicy {
		t.Fatalf("confirmation contract=%+v ok=%v", confirmation, ok)
	}

	settings, ok := catalog.Lookup(SettingsName, SettingsVersion)
	if !ok || settings.Scope != ContractScopeSession || settings.Cancelable || settings.Audience != ContractAudienceInternal {
		t.Fatalf("settings contract=%+v ok=%v", settings, ok)
	}

	if _, ok := catalog.Lookup("device.battery.read", "1"); ok {
		t.Fatal("reserved read vocabulary was activated without a contract")
	}
}

func TestContractCatalogRejectsDuplicateIdentity(t *testing.T) {
	schema := map[string]any{"type": "object"}
	contract := Contract{
		Name: "device.test", Version: "1", Kind: "command",
		Scope: ContractScopeSession, Audience: ContractAudienceInternal,
		InputSchema: schema, ResultSchema: schema,
	}
	if _, err := NewContractCatalog(contract, contract); err == nil {
		t.Fatal("duplicate name@version contract accepted")
	}
}

func TestCurrentContractsRejectSchemaDrift(t *testing.T) {
	catalog := DefaultContractCatalog()
	volume, _ := catalog.Lookup(VolumeSetName, VolumeSetVersion)
	if err := volume.ValidateInput(json.RawMessage(`{"volume":42}`)); err != nil {
		t.Fatal(err)
	}
	if err := volume.ValidateInput(json.RawMessage(`{"volume":101}`)); err == nil {
		t.Fatal("out-of-range volume accepted")
	}
	if err := volume.ValidateResult(json.RawMessage(`{"applied":true,"extra":1}`)); err == nil {
		t.Fatal("unknown volume result field accepted")
	}

	settings, _ := catalog.Lookup(SettingsName, SettingsVersion)
	if err := settings.ValidateInput(json.RawMessage(`{"version":2,"settings":{"vad_threshold":700}}`)); err != nil {
		t.Fatal(err)
	}
	if err := settings.ValidateInput(json.RawMessage(`{"version":2,"settings":{"wake_model":"wn9"}}`)); err == nil {
		t.Fatal("wake_model escaped #198 boundary")
	}
	if err := settings.ValidateResult(json.RawMessage(`{"applied":true,"version":2,"settings":{"vad_threshold":700}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRouterValidatesInputAndSuccessfulResult(t *testing.T) {
	router := NewRouter()
	endpoint := &contractEndpoint{result: Result{Value: json.RawMessage(`{"applied":true}`)}}
	if err := router.Register("device-a", endpoint); err != nil {
		t.Fatal(err)
	}

	if _, err := router.Call(context.Background(), "device-a", Call{
		Name: VolumeSetName, Version: VolumeSetVersion, Arguments: json.RawMessage(`{"volume":101}`),
	}); !errors.Is(err, ErrContractViolation) {
		t.Fatalf("invalid input err=%v", err)
	}
	if endpoint.calls != 0 {
		t.Fatalf("invalid input reached endpoint calls=%d", endpoint.calls)
	}

	endpoint.result = Result{Value: json.RawMessage(`{"unexpected":true}`)}
	if _, err := router.Call(context.Background(), "device-a", Call{
		Name: VolumeSetName, Version: VolumeSetVersion, Arguments: json.RawMessage(`{"volume":42}`),
	}); !errors.Is(err, ErrContractViolation) {
		t.Fatalf("invalid result err=%v", err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("valid input did not reach endpoint calls=%d", endpoint.calls)
	}
}

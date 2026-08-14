package protocol

import (
	"encoding/json"
	"testing"
)

func TestCapabilityMessagesRoundTripAndValidate(t *testing.T) {
	cases := []struct {
		typeName MessageType
		payload  any
	}{
		{CapabilityAdvertiseType, CapabilityAdvertisePayload{Capabilities: []CapabilityDescriptor{{Name: "device.volume.set", Version: "1", Kind: "command"}}}},
		{CapabilityCallType, CapabilityCallPayload{Name: "device.volume.set", Version: "1", Arguments: json.RawMessage(`{"volume":42}`), DeadlineMS: 3000}},
		{CapabilityResultType, CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"volume":42}`)}},
		{CapabilityCancelType, CapabilityCancelPayload{Reason: "turn_aborted"}},
	}
	for _, tc := range cases {
		raw, err := Encode(tc.typeName, Metadata{MessageID: "m-1", SessionID: "s-1", CorrelationID: "c-1", GenerationID: 3}, tc.payload)
		if err != nil { t.Fatalf("%s encode: %v", tc.typeName, err) }
		envelope, err := Decode(raw)
		if err != nil { t.Fatalf("%s decode: %v", tc.typeName, err) }
		if envelope.Type != tc.typeName || envelope.CorrelationID != "c-1" || envelope.GenerationID != 3 {
			t.Fatalf("%s envelope=%+v", tc.typeName, envelope)
		}
	}
}

func TestCapabilityAdvertiseRejectsDuplicateAndInjectedShape(t *testing.T) {
	payload := CapabilityAdvertisePayload{Capabilities: []CapabilityDescriptor{
		{Name: "device.volume.set", Version: "1", Kind: "command"},
		{Name: "device.volume.set", Version: "1", Kind: "command"},
	}}
	if err := payload.Validate(); err == nil { t.Fatal("duplicate descriptor accepted") }

	raw := []byte(`{"version":2,"type":"capability.advertise","message_id":"m","session_id":"s","payload":{"capabilities":[{"name":"device.volume.set","version":"1","kind":"command","schema":{"type":"object"}}]}}`)
	envelope, err := Decode(raw)
	if err != nil { t.Fatal(err) }
	if _, err := DecodePayload[CapabilityAdvertisePayload](envelope); err == nil {
		t.Fatal("device-provided schema was accepted")
	}
}

func TestCapabilityCallAndResultFailClosed(t *testing.T) {
	if err := (CapabilityCallPayload{Name: "device.volume.set", Version: "1", Arguments: json.RawMessage(`{"volume":42}`), DeadlineMS: 10}).Validate(); err == nil {
		t.Fatal("too-small deadline accepted")
	}
	if err := (CapabilityCallPayload{Name: "device.volume.set", Version: "1", Arguments: json.RawMessage(`[]`), DeadlineMS: 3000}).Validate(); err == nil {
		t.Fatal("non-object arguments accepted")
	}
	if err := (CapabilityResultPayload{OK: false, Error: "arbitrary_provider_error"}).Validate(); err == nil {
		t.Fatal("unknown error code accepted")
	}
	if err := (CapabilityResultPayload{OK: true}).Validate(); err == nil {
		t.Fatal("successful result without value accepted")
	}
}

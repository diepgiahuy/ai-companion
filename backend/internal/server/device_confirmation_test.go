package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/devicecap"
	"companion-server/internal/protocol"
)

func advertiseConfirmationCapability(t *testing.T, s *session) {
	t.Helper()
	advertise, err := protocol.Encode(protocol.CapabilityAdvertiseType, protocol.Metadata{
		MessageID: "advertise-confirmation", SessionID: s.id,
	}, protocol.CapabilityAdvertisePayload{Capabilities: []protocol.CapabilityDescriptor{{
		Name: devicecap.UserConfirmationName, Version: devicecap.UserConfirmationVersion, Kind: "command",
	}}})
	if err != nil { t.Fatal(err) }
	if handled, err := s.handleCapabilityControl(context.Background(), advertise); err != nil || !handled {
		t.Fatalf("advertise handled=%v err=%v", handled, err)
	}
}

func TestDeviceConfirmationRequiresExactTurnAndGeneration(t *testing.T) {
	s, router := capabilityTestSession(t)
	advertiseConfirmationCapability(t, s)
	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		approved, err := router.RequestConfirmation(context.Background(), capability.ConfirmationTarget{
			UserID: "user-a", DeviceID: s.deviceID, TurnID: "turn-confirm",
		}, capability.ConfirmationIntent{
			ToolName: "note.delete", Description: "Delete note 12",
			ArgumentsHash: "server-only", ExpiresAt: time.Now().Add(2 * time.Second),
		})
		if err != nil { errCh <- err; return }
		resultCh <- approved
	}()

	call := <-s.controlWrites
	envelope, err := protocol.Decode(call.data)
	if err != nil { t.Fatal(err) }
	if envelope.Type != protocol.CapabilityCallType || envelope.TurnID != "turn-confirm" || envelope.GenerationID != 7 || envelope.CorrelationID == "" {
		t.Fatalf("call envelope=%+v", envelope)
	}
	payload, err := protocol.DecodePayload[protocol.CapabilityCallPayload](envelope)
	if err != nil { t.Fatal(err) }
	if payload.Name != devicecap.UserConfirmationName || payload.Version != devicecap.UserConfirmationVersion {
		t.Fatalf("payload=%+v", payload)
	}
	var args map[string]any
	if err := json.Unmarshal(payload.Arguments, &args); err != nil { t.Fatal(err) }
	if len(args) != 2 || args["tool_name"] != "note.delete" || args["prompt"] != "Delete note 12" {
		t.Fatalf("confirmation args=%v", args)
	}

	wrongTurn, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "wrong-turn", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: "turn-other", GenerationID: envelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"approved":true}`)})
	if err != nil { t.Fatal(err) }
	if handled, err := s.handleCapabilityControl(context.Background(), wrongTurn); !handled || err == nil {
		t.Fatalf("wrong-turn result handled=%v err=%v", handled, err)
	}

	wrongGeneration, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "wrong-generation", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: envelope.TurnID, GenerationID: envelope.GenerationID + 1,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"approved":true}`)})
	if err != nil { t.Fatal(err) }
	if handled, err := s.handleCapabilityControl(context.Background(), wrongGeneration); !handled || err == nil {
		t.Fatalf("wrong-generation result handled=%v err=%v", handled, err)
	}

	correct, err := protocol.Encode(protocol.CapabilityResultType, protocol.Metadata{
		MessageID: "correct-confirmation", SessionID: s.id, CorrelationID: envelope.CorrelationID,
		TurnID: envelope.TurnID, GenerationID: envelope.GenerationID,
	}, protocol.CapabilityResultPayload{OK: true, Value: json.RawMessage(`{"approved":true}`)})
	if err != nil { t.Fatal(err) }
	if handled, err := s.handleCapabilityControl(context.Background(), correct); err != nil || !handled {
		t.Fatalf("correct result handled=%v err=%v", handled, err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case approved := <-resultCh:
		if !approved { t.Fatal("correct approval was rejected") }
	case <-time.After(time.Second):
		t.Fatal("confirmation result timed out")
	}
}

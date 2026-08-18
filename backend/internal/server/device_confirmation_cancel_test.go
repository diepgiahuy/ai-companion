package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/protocol"
)

func TestDeviceConfirmationCancelableCallEmitsCorrelatedCancel(t *testing.T) {
	s, router := capabilityTestSession(t)
	advertiseConfirmationCapability(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := router.RequestConfirmation(ctx, capability.ConfirmationTarget{
			UserID: "user-a", DeviceID: s.deviceID, TurnID: "turn-confirm",
		}, capability.ConfirmationIntent{
			ToolName: "note.delete", Description: "Delete note 12",
			ArgumentsHash: "server-only", ExpiresAt: time.Now().Add(2 * time.Second),
		})
		errCh <- err
	}()

	call := <-s.controlWrites
	callEnvelope, err := protocol.Decode(call.data)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancelFrame := <-s.controlWrites
	cancelEnvelope, err := protocol.Decode(cancelFrame.data)
	if err != nil {
		t.Fatal(err)
	}
	if cancelEnvelope.Type != protocol.CapabilityCancelType ||
		cancelEnvelope.CorrelationID != callEnvelope.CorrelationID ||
		cancelEnvelope.TurnID != callEnvelope.TurnID ||
		cancelEnvelope.GenerationID != callEnvelope.GenerationID {
		t.Fatalf("cancel envelope=%+v call=%+v", cancelEnvelope, callEnvelope)
	}
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("confirmation err=%v", err)
	}
}

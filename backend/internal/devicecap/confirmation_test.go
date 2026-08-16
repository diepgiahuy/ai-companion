package devicecap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"companion-server/internal/capability"
)

type confirmationEndpoint struct {
	last      Call
	value     json.RawMessage
	supported bool
}

func (e *confirmationEndpoint) Supports(name, version string) bool {
	return e.supported && name == UserConfirmationName && version == UserConfirmationVersion
}
func (e *confirmationEndpoint) Call(_ context.Context, call Call) (Result, error) {
	e.last = call
	return Result{Value: e.value}, nil
}

func TestConfirmationRequesterUsesAuthenticatedDeviceTurnAndHidesArgumentsHash(t *testing.T) {
	router := NewRouter()
	endpoint := &confirmationEndpoint{supported: true, value: json.RawMessage(`{"approved":true}`)}
	if err := router.Register("device-a", endpoint); err != nil { t.Fatal(err) }
	expires := time.Now().Add(2 * time.Second)
	approved, err := router.RequestConfirmation(context.Background(), capability.ConfirmationTarget{
		UserID: "user-a", DeviceID: "device-a", TurnID: "turn-a",
	}, capability.ConfirmationIntent{
		ToolName: "note.delete", Description: "Delete note 12",
		ArgumentsHash: "server-secret-binding", ExpiresAt: expires,
	})
	if err != nil { t.Fatal(err) }
	if !approved { t.Fatal("approved result was not returned") }
	if endpoint.last.Name != UserConfirmationName || endpoint.last.Version != UserConfirmationVersion || endpoint.last.TurnID != "turn-a" {
		t.Fatalf("call=%+v", endpoint.last)
	}
	var args map[string]any
	if err := json.Unmarshal(endpoint.last.Arguments, &args); err != nil { t.Fatal(err) }
	if args["tool_name"] != "note.delete" || args["prompt"] != "Delete note 12" {
		t.Fatalf("args=%v", args)
	}
	if _, leaked := args["arguments_hash"]; leaked {
		t.Fatalf("server-side arguments hash leaked to device: %v", args)
	}
	if len(args) != 2 { t.Fatalf("unexpected confirmation args=%v", args) }
}

func TestConfirmationRequesterRejectsMalformedResultAndUnscopedTarget(t *testing.T) {
	router := NewRouter()
	endpoint := &confirmationEndpoint{supported: true, value: json.RawMessage(`{"approved":true,"extra":1}`)}
	if err := router.Register("device-a", endpoint); err != nil { t.Fatal(err) }
	intent := capability.ConfirmationIntent{ToolName: "note.delete", Description: "Delete note", ExpiresAt: time.Now().Add(time.Second)}
	if _, err := router.RequestConfirmation(context.Background(), capability.ConfirmationTarget{DeviceID: "device-a", TurnID: "turn-a"}, intent); err == nil {
		t.Fatal("result with unknown field accepted")
	}
	if _, err := router.RequestConfirmation(context.Background(), capability.ConfirmationTarget{DeviceID: "device-a"}, intent); err == nil {
		t.Fatal("missing turn_id accepted")
	}
}

package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

type confirmationRequesterStub struct {
	approved bool
	err      error
	calls    int
	target   capability.ConfirmationTarget
	intent   capability.ConfirmationIntent
}

func (s *confirmationRequesterStub) RequestConfirmation(_ context.Context, target capability.ConfirmationTarget, intent capability.ConfirmationIntent) (bool, error) {
	s.calls++
	s.target = target
	s.intent = intent
	return s.approved, s.err
}

func TestAuthorizerRequestsExactDestructiveConfirmation(t *testing.T) {
	now := time.Date(2026, 8, 16, 7, 0, 0, 0, time.UTC)
	requester := &confirmationRequesterStub{approved: true}
	a := Authorizer{Confirmations: requester, Now: func() time.Time { return now }}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{
		UserID: "user-a", DeviceID: "device-a", TurnID: "turn-a",
	})
	args := `{ "id": 12, "z": 2 }`
	if err := a.Authorize(ctx, capability.ToolDefinition{
		Name: "note.delete", Description: "Delete note 12", Risk: "destructive",
	}, capability.ToolRequest{Arguments: args}); err != nil {
		t.Fatalf("approved destructive action rejected: %v", err)
	}
	if requester.calls != 1 {
		t.Fatalf("requester calls=%d want=1", requester.calls)
	}
	if requester.target.UserID != "user-a" || requester.target.DeviceID != "device-a" || requester.target.TurnID != "turn-a" {
		t.Fatalf("target=%+v", requester.target)
	}
	wantHash, err := CanonicalArgumentsHash(args)
	if err != nil { t.Fatal(err) }
	if requester.intent.ToolName != "note.delete" || requester.intent.Description != "Delete note 12" || requester.intent.ArgumentsHash != wantHash {
		t.Fatalf("intent=%+v", requester.intent)
	}
	if !requester.intent.ExpiresAt.Equal(now.Add(destructiveConfirmationTTL)) {
		t.Fatalf("expires=%v want=%v", requester.intent.ExpiresAt, now.Add(destructiveConfirmationTTL))
	}
}

func TestAuthorizerFailsClosedOnRejectErrorOrMissingAuthenticatedTurn(t *testing.T) {
	now := time.Date(2026, 8, 16, 7, 0, 0, 0, time.UTC)
	def := capability.ToolDefinition{Name: "expense.delete", Risk: "destructive"}
	req := capability.ToolRequest{Arguments: `{"id":1}`}

	rejected := &confirmationRequesterStub{}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u", DeviceID: "d", TurnID: "t"})
	if err := (Authorizer{Confirmations: rejected, Now: func() time.Time { return now }}).Authorize(ctx, def, req); err == nil {
		t.Fatal("rejected confirmation authorized destructive action")
	}
	if rejected.calls != 1 { t.Fatalf("reject calls=%d", rejected.calls) }

	failed := &confirmationRequesterStub{err: errors.New("offline")}
	if err := (Authorizer{Confirmations: failed, Now: func() time.Time { return now }}).Authorize(ctx, def, req); err == nil {
		t.Fatal("confirmation transport failure authorized destructive action")
	}
	if failed.calls != 1 { t.Fatalf("failure calls=%d", failed.calls) }

	missing := &confirmationRequesterStub{approved: true}
	missingCtx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u", DeviceID: "d"})
	if err := (Authorizer{Confirmations: missing, Now: func() time.Time { return now }}).Authorize(missingCtx, def, req); err == nil {
		t.Fatal("missing turn_id authorized destructive action")
	}
	if missing.calls != 0 { t.Fatalf("missing turn unexpectedly called requester %d times", missing.calls) }
}

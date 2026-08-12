package policy

import (
	"context"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/pipeline"
)

type featureStub bool

func (f featureStub) Enabled(context.Context, string, controlplane.EvalContext, bool) bool {
	return bool(f)
}

type privacyStub bool

func (p privacyStub) MemoryAllowed(context.Context, string) bool { return bool(p) }

type entitlementStub bool

func (e entitlementStub) Allowed(context.Context, string, string) bool { return bool(e) }

func TestAuthorizerSeparatesFeatureEntitlementPrivacyAndConfirmation(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u1", DeviceID: "d1"})
	a := Authorizer{Features: featureStub(true), Entitlements: entitlementStub(true), Privacy: privacyStub(true), Now: func() time.Time { return now }}
	if err := a.Authorize(ctx, capability.ToolDefinition{FeatureKey: "market.live", Entitlement: "market", Risk: "read"}, capability.ToolRequest{}); err != nil {
		t.Fatal(err)
	}
	def := capability.ToolDefinition{Name: "note.delete", Risk: "destructive"}
	req := capability.ToolRequest{Arguments: `{"id":12}`}
	if err := a.Authorize(ctx, def, req); err == nil {
		t.Fatal("destructive tool must require scoped confirmation")
	}
	confirmation, err := NewDestructiveConfirmation("u1", "note.delete", `{ "id": 12 }`, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	confirmed := WithDestructiveConfirmation(ctx, confirmation)
	if err := a.Authorize(confirmed, def, req); err != nil {
		t.Fatalf("matching confirmation rejected: %v", err)
	}
	if err := a.Authorize(confirmed, capability.ToolDefinition{Name: "expense.delete", Risk: "destructive"}, req); err == nil {
		t.Fatal("confirmation was reusable for another tool")
	}
	if err := a.Authorize(confirmed, def, capability.ToolRequest{Arguments: `{"id":13}`}); err == nil {
		t.Fatal("confirmation was reusable for different arguments")
	}
	if err := (Authorizer{Features: featureStub(false)}).Authorize(ctx, capability.ToolDefinition{FeatureKey: "market.live"}, capability.ToolRequest{}); err == nil {
		t.Fatal("disabled feature should block")
	}
	if err := (Authorizer{Privacy: privacyStub(false)}).Authorize(ctx, capability.ToolDefinition{FeatureKey: "memory.long_term"}, capability.ToolRequest{}); err == nil {
		t.Fatal("privacy policy should block memory")
	}
}

func TestDestructiveConfirmationCanonicalizesJSONAndExpires(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	confirmation, err := NewDestructiveConfirmation("u1", "note.delete", `{"z":2,"id":12}`, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u1"})
	ctx = WithDestructiveConfirmation(ctx, confirmation)
	def := capability.ToolDefinition{Name: "note.delete", Risk: "destructive"}
	req := capability.ToolRequest{Arguments: ` { "id": 12, "z": 2 } `}
	if err := (Authorizer{Now: func() time.Time { return now }}).Authorize(ctx, def, req); err != nil {
		t.Fatalf("semantically identical JSON should match: %v", err)
	}
	if err := (Authorizer{Now: func() time.Time { return now.Add(2 * time.Second) }}).Authorize(ctx, def, req); err == nil {
		t.Fatal("expired confirmation was accepted")
	}
}

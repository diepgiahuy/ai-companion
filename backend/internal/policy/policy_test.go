package policy

import (
	"context"
	"testing"

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

func TestAuthorizerSeparatesFeatureEntitlementPrivacyAndIntent(t *testing.T) {
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "u1", DeviceID: "d1"})
	a := Authorizer{Features: featureStub(true), Entitlements: entitlementStub(true), Privacy: privacyStub(true)}
	if err := a.Authorize(ctx, capability.ToolDefinition{FeatureKey: "market.live", Entitlement: "market", Risk: "read"}, capability.ToolRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Authorize(ctx, capability.ToolDefinition{Risk: "destructive"}, capability.ToolRequest{}); err == nil {
		t.Fatal("destructive tool must require explicit intent")
	}
	ctx = WithExplicitDestructive(ctx, true)
	if err := a.Authorize(ctx, capability.ToolDefinition{Risk: "destructive"}, capability.ToolRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := (Authorizer{Features: featureStub(false)}).Authorize(ctx, capability.ToolDefinition{FeatureKey: "market.live"}, capability.ToolRequest{}); err == nil {
		t.Fatal("disabled feature should block")
	}
	if err := (Authorizer{Privacy: privacyStub(false)}).Authorize(ctx, capability.ToolDefinition{FeatureKey: "memory.long_term"}, capability.ToolRequest{}); err == nil {
		t.Fatal("privacy policy should block memory")
	}
}

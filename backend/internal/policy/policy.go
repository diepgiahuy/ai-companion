package policy

import (
	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/pipeline"
	"context"
	"fmt"
	"strings"
)

type Entitlements interface {
	Allowed(context.Context, string, string) bool
}
type UserPrivacy interface {
	MemoryAllowed(context.Context, string) bool
}
type FeatureEvaluator interface {
	Enabled(context.Context, string, controlplane.EvalContext, bool) bool
}
type Authorizer struct {
	Entitlements Entitlements
	Features     FeatureEvaluator
	Privacy      UserPrivacy
}

func (a Authorizer) Authorize(ctx context.Context, d capability.ToolDefinition, _ capability.ToolRequest) error {
	turn, _ := pipeline.CurrentTurn(ctx)
	if d.FeatureKey != "" && a.Features != nil && !a.Features.Enabled(ctx, d.FeatureKey, controlplane.EvalContext{UserID: turn.UserID, DeviceID: turn.DeviceID, TenantID: turn.TenantID, Plan: turn.Plan, Locale: turn.Locale}, false) {
		return fmt.Errorf("feature %s disabled", d.FeatureKey)
	}
	if d.Entitlement != "" && a.Entitlements != nil && !a.Entitlements.Allowed(ctx, turn.UserID, d.Entitlement) {
		return fmt.Errorf("missing entitlement %s", d.Entitlement)
	}
	if d.FeatureKey == "memory.long_term" && a.Privacy != nil && !a.Privacy.MemoryAllowed(ctx, turn.UserID) {
		return fmt.Errorf("long-term memory disabled by user privacy policy")
	}
	if d.Risk == "destructive" { // host policy still requires an explicit destructive turn marker supplied by the router/runtime.
		if !ExplicitDestructive(ctx) {
			return fmt.Errorf("explicit destructive intent required")
		}
	}
	return nil
}

type destructiveKey struct{}

func WithExplicitDestructive(ctx context.Context, ok bool) context.Context {
	return context.WithValue(ctx, destructiveKey{}, ok)
}
func ExplicitDestructive(ctx context.Context) bool {
	v, _ := ctx.Value(destructiveKey{}).(bool)
	return v
}
func LooksDestructive(s string) bool {
	s = strings.ToLower(s)
	for _, x := range []string{"xóa", "xoá", "delete", "clear", "hủy", "huỷ", "cancel", "factory reset"} {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

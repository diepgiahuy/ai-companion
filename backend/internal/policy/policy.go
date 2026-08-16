package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/pipeline"
)

const destructiveConfirmationTTL = 15 * time.Second

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
	Now          func() time.Time
}

func (a Authorizer) Authorize(ctx context.Context, d capability.ToolDefinition, req capability.ToolRequest) error {
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
	if d.Risk == "destructive" {
		if err := a.authorizeDestructive(ctx, turn, d, req); err != nil {
			return err
		}
	}
	return nil
}

type DestructiveConfirmation struct {
	UserID        string
	ToolName      string
	ArgumentsHash string
	ExpiresAt     time.Time
}

type destructiveConfirmationKey struct{}

// NewDestructiveConfirmation scopes approval to one owner, one exact tool, and
// one canonical argument payload. Trusted in-process callers may inject this
// value after an explicit approval. Product model ingress instead uses the
// ConfirmationRequester boundary below so the model never receives authority.
func NewDestructiveConfirmation(userID, toolName, arguments string, expiresAt time.Time) (DestructiveConfirmation, error) {
	userID = strings.TrimSpace(userID)
	toolName = strings.TrimSpace(toolName)
	if userID == "" || toolName == "" {
		return DestructiveConfirmation{}, fmt.Errorf("confirmation user_id and tool_name are required")
	}
	if expiresAt.IsZero() {
		return DestructiveConfirmation{}, fmt.Errorf("confirmation expiry is required")
	}
	hash, err := CanonicalArgumentsHash(arguments)
	if err != nil {
		return DestructiveConfirmation{}, err
	}
	return DestructiveConfirmation{UserID: userID, ToolName: toolName, ArgumentsHash: hash, ExpiresAt: expiresAt}, nil
}

func WithDestructiveConfirmation(ctx context.Context, confirmation DestructiveConfirmation) context.Context {
	return context.WithValue(ctx, destructiveConfirmationKey{}, confirmation)
}

func DestructiveConfirmationFromContext(ctx context.Context) (DestructiveConfirmation, bool) {
	confirmation, ok := ctx.Value(destructiveConfirmationKey{}).(DestructiveConfirmation)
	return confirmation, ok
}

func CanonicalArgumentsHash(arguments string) (string, error) {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		arguments = "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return "", fmt.Errorf("canonicalize destructive tool arguments: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal destructive tool arguments: %w", err)
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func (a Authorizer) authorizeDestructive(ctx context.Context, turn pipeline.TurnContext, d capability.ToolDefinition, req capability.ToolRequest) error {
	userID := strings.TrimSpace(turn.UserID)
	if userID == "" {
		userID = strings.TrimSpace(turn.DeviceID)
	}
	hash, err := CanonicalArgumentsHash(req.Arguments)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}

	if confirmation, ok := DestructiveConfirmationFromContext(ctx); ok {
		return validateDestructiveConfirmation(confirmation, userID, d.Name, hash, now)
	}

	requester, ok := capability.ConfirmationRequesterFromContext(ctx)
	if !ok {
		return fmt.Errorf("destructive action requires explicit user confirmation")
	}
	prompt := strings.TrimSpace(d.Description)
	if prompt == "" {
		prompt = "Confirm " + d.Name
	}
	expiresAt := now.Add(destructiveConfirmationTTL)
	approved, err := requester.RequestConfirmation(ctx, capability.ConfirmationIntent{
		ToolName:      d.Name,
		Description:   prompt,
		ArgumentsHash: hash,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		return fmt.Errorf("destructive confirmation failed: %w", err)
	}
	if !approved {
		return fmt.Errorf("destructive action not approved")
	}
	return nil
}

func validateDestructiveConfirmation(confirmation DestructiveConfirmation, userID, toolName, argumentsHash string, now time.Time) error {
	if confirmation.UserID != userID {
		return fmt.Errorf("destructive confirmation owner mismatch")
	}
	if confirmation.ToolName != toolName {
		return fmt.Errorf("destructive confirmation tool mismatch")
	}
	if confirmation.ArgumentsHash != argumentsHash {
		return fmt.Errorf("destructive confirmation arguments mismatch")
	}
	if !confirmation.ExpiresAt.After(now) {
		return fmt.Errorf("destructive confirmation expired")
	}
	return nil
}
